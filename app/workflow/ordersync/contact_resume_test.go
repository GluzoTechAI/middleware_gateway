package ordersync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gluzo/integration-gateway/app/apperror"
	"github.com/gluzo/integration-gateway/app/domain/order"
	"github.com/gluzo/integration-gateway/app/integrations/easyecom"
	vinculummapper "github.com/gluzo/integration-gateway/app/integrations/vinculum/mapper"
	"github.com/gluzo/integration-gateway/app/vendor"
	"github.com/gluzo/integration-gateway/app/workflow"
	"github.com/gluzo/integration-gateway/app/workflow/ordersync"
)

// Exercise the real shipping guard, while keeping vendor submission local.
type shippingGuardVendor struct{ fakeVendor }

func (v *shippingGuardVendor) PrepareOrder(_ context.Context, o order.Order, route vendor.Route) (json.RawMessage, error) {
	req, err := vinculummapper.ToCreateOrder(o, route, vinculummapper.OrderOptions{PrepaidOrderType: "Prepaid", PrepaidPaymentType: "Credit Card", InitialStatus: "Confirmed", Timezone: "Asia/Kolkata"})
	if err != nil {
		return nil, err
	}
	return json.Marshal(req)
}

func TestSavedMapOrderResumesWithWebhookContactsWithoutRefetch(t *testing.T) {
	for _, matchingWebhook := range []bool{true, false} {
		name := "matching webhook recovers saved run"
		if !matchingWebhook {
			name = "different order remains blocked"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("MAP_ORDER must not refetch the order: %s", r.URL.Path)
				w.WriteHeader(500)
			}))
			defer srv.Close()
			client, err := easyecom.NewClient(easyecom.Config{BaseURL: srv.URL, APIKey: "test-key", JWTToken: "test-jwt"})
			if err != nil {
				t.Fatal(err)
			}
			v := &shippingGuardVendor{fakeVendor: fakeVendor{created: true}}
			vendors := vendor.NewRegistry()
			if err := vendors.Register(v); err != nil {
				t.Fatal(err)
			}
			wf, err := ordersync.New(ordersync.Dependencies{Resolver: f.resolver, Origins: map[string]vendor.Origin{"easyecom": easyecom.NewSource(client, nil)}, Vendors: vendors, SKUs: f.skus})
			if err != nil {
				t.Fatal(err)
			}
			st := f.state("12345")
			st.Event.ExternalOrderID = "652462545"
			st.Event.Payload = []byte(`{"order_id":652462545,"customer_name":"Test Customer","email":"test@example.com","address_line_1":"123 Test Street","contact_num":"9999999999","city":"Noida","state":"Uttar Pradesh","pin_code":"201301","country":"India"}`)
			if !matchingWebhook {
				st.Event.Payload = []byte(strings.ReplaceAll(string(st.Event.Payload), "652462545", "999"))
			}
			st.Order = &order.Order{ExternalID: "652462545", WarehouseID: "304643", PaymentMode: order.PaymentPrepaid, TotalAmount: 1049, OrderedAt: time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), Currency: "INR",
				ShippingAddress: order.Address{City: "Noida", State: "Uttar Pradesh", PostalCode: "201301", Country: "India"},
				Items:           []order.Item{{SKU: "A", Quantity: 1, UnitPrice: 1049}},
			}
			st.Route = &workflow.RouteInfo{IntegrationID: f.integ.String(), IntegrationName: "easyecom-vinculum", SourcePlatform: "easyecom", DestinationPlatform: "vinculum", DestinationReference: "BLR"}
			st.Status, st.CurrentAction, st.NextAction, st.LastSuccessfulAction = workflow.StatusFailed, 2, ordersync.ActionMapOrder, ordersync.ActionFetchOrder
			st.LastError = &apperror.Info{Category: apperror.Mapping, Message: "missing recipient name, address line 1, phone"}
			st.Actions = []workflow.ActionRecord{
				{Name: ordersync.ActionResolveIntegration, Status: workflow.ActionSucceeded, Attempt: 1},
				{Name: ordersync.ActionFetchOrder, Status: workflow.ActionSucceeded, Attempt: 1},
				{Name: ordersync.ActionMapOrder, Status: workflow.ActionFailed, Attempt: 1, LastError: st.LastError},
				{Name: ordersync.ActionSubmitVendorOrder, Status: workflow.ActionPending},
			}
			// Save/reload to reproduce the persisted state that manual resume loads.
			ctx := context.Background()
			if err := f.repo.Save(ctx, st); err != nil {
				t.Fatal(err)
			}
			st, err = f.repo.Get(ctx, st.CorrelationID)
			if err != nil {
				t.Fatal(err)
			}
			st.ResetCurrentAttempts()
			err = f.exec.Run(ctx, wf, st)
			if !matchingWebhook {
				if apperror.CategoryOf(err) != apperror.Mapping || v.submitCalls != 0 || st.Order.Customer.Name != "" {
					t.Fatalf("unrelated recipient was accepted: %+v, %v", st, err)
				}
				return
			}
			if err != nil || st.Status != workflow.StatusCompleted || v.submitCalls != 1 {
				t.Fatalf("resume: %+v, %v, submits %d", st, err, v.submitCalls)
			}
			var document map[string]any
			if err := json.Unmarshal(v.submitted, &document); err != nil {
				t.Fatal(err)
			}
			document = document["order"].(map[string]any)
			if document["orderNo"] != "652462545" || document["customerName"] != "Test Customer" || document["shipAddress1"] != "123 Test Street" || document["shipPhone1"] != "9999999999" {
				t.Fatalf("prepared shipping fields: %s", v.submitted)
			}
			line := document["items"].([]any)[0].(map[string]any)
			if line["sku"] != "BCPL-A" || line["unitPrice"] != "1049" || line["orderQty"] != "1" {
				t.Fatalf("order lines changed: %+v", line)
			}
			saved, err := f.repo.Get(ctx, st.CorrelationID)
			if err != nil || saved.Order.ShippingAddress.Line1 != "123 Test Street" || saved.Order.Items[0].SKU != "A" {
				t.Fatalf("enriched origin order not persisted: %+v, %v", saved, err)
			}
			for _, entry := range f.recorder.Entries() {
				if entry.Action == ordersync.ActionFetchOrder || entry.Action == ordersync.ActionResolveIntegration {
					t.Fatalf("completed action was rerun: %+v", entry)
				}
			}
		})
	}
}
