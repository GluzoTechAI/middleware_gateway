package vinculum_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gluzo/integration-gateway/app/apperror"
	"github.com/gluzo/integration-gateway/app/config"
	"github.com/gluzo/integration-gateway/app/domain/order"
	"github.com/gluzo/integration-gateway/app/dotenv"
	"github.com/gluzo/integration-gateway/app/integrations/vinculum"
	"github.com/gluzo/integration-gateway/app/integrations/vinculum/mapper"
)

// Synthetic master values for HTTP fixtures, never production defaults.
func testOrderOptions() vinculum.OrderOptions {
	return vinculum.OrderOptions{PrepaidOrderType: "Prepaid", CODOrderType: "COD", PrepaidPaymentType: "Credit Card", CODPaymentType: "Cash", InitialStatus: "Confirmed", Timezone: "Asia/Kolkata"}
}

func requireOrderEnvelope(t *testing.T, root map[string]any) map[string]any {
	t.Helper()
	body, ok := root["order"].(map[string]any)
	if !ok || len(root) != 1 {
		t.Errorf("expected only outer order object: %+v", root)
		return map[string]any{}
	}
	if _, ok := body["orderAmount"].(string); !ok {
		t.Errorf("orderAmount must be a decimal total: %+v", body)
	}
	lines, ok := body["items"].([]any)
	if !ok || len(lines) == 0 {
		t.Errorf("items must contain lines: %+v", body)
		return body
	}
	for _, value := range lines {
		line := value.(map[string]any)
		for _, key := range []string{"lineno", "orderQty", "unitPrice"} {
			if _, ok := line[key].(string); !ok {
				t.Errorf("%s must follow the documented string representation: %+v", key, line)
			}
		}
	}
	for _, key := range []string{"shipTo", "shipMobile", "shipPincode", "shipEmail"} {
		if _, ok := body[key]; ok {
			t.Errorf("obsolete field %s present", key)
		}
	}
	return body
}

func TestOrderWireContractPreservesTotalBillingAndTimezone(t *testing.T) {
	f := newFakeVinculum(t)
	o := sampleOrder()
	o.TotalAmount = 1049.0001
	o.Currency = "USD"
	o.BillingAddress = order.Address{Name: "Billing Customer", Line1: "Billing Street", City: "Mumbai", State: "Maharashtra", Country: "India", PostalCode: "400001", Phone: "8888888888", Email: "billing@example.com"}
	doc, err := f.vendor(t).PrepareOrder(context.Background(), o, vendorRoute())
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(doc, &root); err != nil {
		t.Fatal(err)
	}
	body := requireOrderEnvelope(t, root)
	for key, want := range map[string]string{"orderNo": o.ExternalID, "reqType": "create", "orderAmount": "1049.0001", "orderDate": "30/09/2026 17:00:00", "orderCurrency": "USD", "orderType": "COD", "paymentType": "Cash", "status": "Confirmed", "customerName": "Asha Verma", "shipPinCode": "411001", "shipPhone1": "9876501234", "shipEmail1": "asha@example.com", "billName": "Billing Customer", "billAddress1": "Billing Street", "billEmail1": "billing@example.com"} {
		if body[key] != want {
			t.Errorf("%s=%v, want %s", key, body[key], want)
		}
	}
	line := body["items"].([]any)[0].(map[string]any)
	if _, ok := line["mrp"]; ok {
		t.Error("selling price was invented as MRP")
	}
	if _, ok := line["mode"]; ok {
		t.Error("fulfilment mode was guessed")
	}
	if f.orderCalls.Load() != 0 {
		t.Error("mapping called API")
	}
}

// Exercise the deployable template through real config parsing and the adapter,
// with sanitized customer data and a local HTTP server. This is the combination
// the user reported creating a BCPL order on 20 October 2026, not an inferred
// association between prepaid source orders and card payments.
func TestBCPLPrepaidUsesAcceptedSettingsAndStableUniqueKey(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	variables, err := dotenv.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, variable := range variables {
		env[variable.Key] = variable.Value
	}
	env["DATABASE_URL"] = "postgres://test:test@localhost:5432/test"
	env["REDIS_URL"] = "redis://localhost:6379/0"
	cfg, err := config.LoadFrom(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Vinculum
	if c.CODOrderType != "" || c.CODPaymentType != "" {
		t.Fatal("prepaid test must not establish COD settings")
	}
	f := newFakeVinculum(t)
	client, err := vinculum.NewClient(vinculum.Config{
		BaseURL: f.srv.URL, APIOwner: testOwner, APIKey: testKey, SellableBucket: "Good",
		OrderOptions: vinculum.OrderOptions{PrepaidOrderType: c.PrepaidOrderType, PrepaidPaymentType: c.PrepaidPaymentType, InitialStatus: c.OrderStatus, Timezone: c.OrderTimezone},
	})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vinculum.NewVendor(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := sampleOrder()
	o.ExternalID, o.PaymentMode, o.Currency, o.TotalAmount = "NEW-STAGING-1", order.PaymentPrepaid, "INR", 100
	o.Items = []order.Item{{ExternalID: "source-line", SKU: "3562700373145", Quantity: 1, UnitPrice: 1049, TaxRate: 18}}
	route := vendorRoute()
	route.VendorReference = c.Location
	ctx := context.Background()
	doc, err := v.PrepareOrder(ctx, o, route)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(doc, &root); err != nil {
		t.Fatal(err)
	}
	head := requireOrderEnvelope(t, root)
	for key, want := range map[string]string{"orderLocation": "PBW", "orderType": "Prepaid", "paymentType": "COD", "status": "Confirmed", "orderNo": o.ExternalID, "uniqueKey": o.ExternalID, "reqType": "create", "orderDate": "30/09/2026 17:00:00", "orderCurrency": "INR", "orderAmount": "100"} {
		if head[key] != want {
			t.Errorf("%s=%v, want %s", key, head[key], want)
		}
	}
	line := head["items"].([]any)[0].(map[string]any)
	for _, key := range []string{"mode", "taxInclusive"} {
		if _, present := line[key]; present {
			t.Errorf("accepted BCPL template must omit unconfirmed %s", key)
		}
	}
	if line["unitPrice"] != "1049" || line["orderQty"] != "1" || line["taxPercentage"] != "18" {
		t.Fatalf("source prices or quantities changed: %+v", line)
	}
	repeat, err := v.PrepareOrder(ctx, o, route)
	if err != nil || string(repeat) != string(doc) {
		t.Fatalf("request changed across retries: %v", err)
	}
	ack, err := v.SubmitOrder(ctx, doc, o, route)
	if err != nil || !ack.Created {
		t.Fatalf("local vendor rejected corrected request: %+v %v", ack, err)
	}
	o.ExternalID = "NEW-STAGING-2"
	second, err := v.PrepareOrder(ctx, o, route)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodePrepared(t, second).UniqueKey; got != o.ExternalID {
		t.Fatalf("new order reused uniqueKey %q", got)
	}
	o.PaymentMode = order.PaymentCashOnDelivery
	if _, err = v.PrepareOrder(ctx, o, route); err == nil {
		t.Fatal("unverified cash-on-delivery settings were inferred")
	}
}

func TestOrderMappingRejectsMissingConfigurationAndInvalidData(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*order.Order, *vinculum.OrderOptions)
		want   string
	}{
		{"missing masters", func(o *order.Order, c *vinculum.OrderOptions) { *c = vinculum.OrderOptions{} }, "VINCULUM_ORDER_TYPE"},
		{"missing timezone", func(o *order.Order, c *vinculum.OrderOptions) { c.Timezone = "" }, "VINCULUM_ORDER_TIMEZONE"},
		{"invalid timezone", func(o *order.Order, c *vinculum.OrderOptions) { c.Timezone = "Wrong/Zone" }, "VINCULUM_ORDER_TIMEZONE"},
		{"missing date", func(o *order.Order, c *vinculum.OrderOptions) { o.OrderedAt = time.Time{} }, "orderDate"},
		{"unknown payment", func(o *order.Order, c *vinculum.OrderOptions) { o.PaymentMode = "unknown" }, "payment mode"},
		{"missing email", func(o *order.Order, c *vinculum.OrderOptions) { o.Customer.Email = "" }, "shipEmail1"},
		{"partial billing", func(o *order.Order, c *vinculum.OrderOptions) {
			o.BillingAddress = order.Address{Line1: "Different billing"}
		}, "billCity"},
		{"long sku", func(o *order.Order, c *vinculum.OrderOptions) { o.Items[0].SKU = strings.Repeat("X", 51) }, "sku"},
		{"invalid total", func(o *order.Order, c *vinculum.OrderOptions) { o.TotalAmount = math.NaN() }, "orderAmount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, c := sampleOrder(), testOrderOptions()
			tc.change(&o, &c)
			_, err := mapper.ToCreateOrder(o, vendorRoute(), c)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %s: %v", tc.want, err)
			}
		})
	}
}

func TestCreateOrderRequiresSuccessfulAcknowledgementAndVendorID(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantID string
		wantErr            bool
	}{
		{"wms", `{"responseCode":0,"requestStatus":{"status":"Success","errorCode":0,"outputKey":"VIN-42"}}`, "VIN-42", false},
		{"dropship", `{"responseCode":0,"requestStatus":{"status":"Success","dsOrderNo":12345}}`, "12345", false},
		{"inner failure", `{"responseCode":0,"requestStatus":{"status":"Failed","errorCode":1009,"errorDesc":"Bad order"}}`, "", true},
		{"missing status", `{"responseCode":0,"requestStatus":{"outputKey":"VIN-42"}}`, "", true},
		{"missing id", `{"responseCode":0,"requestStatus":{"status":"Success"}}`, "", true},
		{"old acknowledgement", `{"responseCode":0,"orderNo":"9876543"}`, "", true},
		{"reported live rejection", `{"responseCode":1009,"responseMessage":"Unrecognized field orderNo ","requestStatus":null}`, "", true},
		{"duplicate inner result", `{"responseCode":0,"requestStatus":{"status":"Failed","errorCode":101,"errorDesc":"Duplicate order already exists"}}`, "9876543", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c, err := vinculum.NewClient(vinculum.Config{BaseURL: srv.URL, APIOwner: testOwner, APIKey: testKey, SellableBucket: "Good", OrderOptions: testOrderOptions()})
			if err != nil {
				t.Fatal(err)
			}
			v, err := vinculum.NewVendor(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := v.PrepareOrder(context.Background(), sampleOrder(), vendorRoute())
			if err != nil {
				t.Fatal(err)
			}
			ack, err := v.SubmitOrder(context.Background(), doc, sampleOrder(), vendorRoute())
			if tc.wantErr {
				if apperror.CategoryOf(err) != apperror.ExternalAPI {
					t.Fatalf("expected API error: %+v %v", ack, err)
				}
				return
			}
			if err != nil || ack.VendorOrderID != tc.wantID {
				t.Fatalf("ack=%+v err=%v", ack, err)
			}
		})
	}
}

func TestGenericCreateErrorIncludesNestedVendorRejection(t *testing.T) {
	const detail = "SKU [BPIN00043237] Primary vendor is not MarketPlace type"
	for _, outer := range []string{"Generic Error", "", detail} {
		t.Run("outer="+outer, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"responseCode": 9, "responseMessage": outer, "requestStatus": map[string]any{"status": "Error", "errorDesc": detail, "errorCode": nil, "outputKey": nil, "requestKey": "652797307"}})
			}))
			defer srv.Close()
			c, err := vinculum.NewClient(vinculum.Config{BaseURL: srv.URL, APIOwner: testOwner, APIKey: testKey, SellableBucket: "Good", OrderOptions: testOrderOptions()})
			if err != nil {
				t.Fatal(err)
			}
			v, err := vinculum.NewVendor(c, nil)
			if err != nil {
				t.Fatal(err)
			}
			o := sampleOrder()
			doc, err := v.PrepareOrder(context.Background(), o, vendorRoute())
			if err != nil {
				t.Fatal(err)
			}
			ack, err := v.SubmitOrder(context.Background(), doc, o, vendorRoute())
			var apiErr *apperror.Error
			if !errors.As(err, &apiErr) || apiErr.Category != apperror.ExternalAPI || apiErr.ExternalCode != "9" || apiErr.Operation != "CreateOrder" || apiErr.Retryable || ack.Created {
				t.Fatalf("rejection lost: %+v %+v %v", ack, apiErr, err)
			}
			want := detail
			if outer != "" && outer != detail {
				want = outer + ": " + detail
			}
			if apiErr.ExternalMessage != want || !strings.Contains(err.Error(), detail) {
				t.Fatalf("nested detail hidden: %v (%q)", err, apiErr.ExternalMessage)
			}
		})
	}
}
