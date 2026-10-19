package easyecom_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gluzo/integration-gateway/app/domain/order"
	"github.com/gluzo/integration-gateway/app/event"
	"github.com/gluzo/integration-gateway/app/integrations/easyecom"
)

// Minimal reproduction of the supplied 652462545 payloads. Customer data is
// synthetic; routing, items and prices deliberately differ between sources.
const contactWebhook = `{"order_id":652462545,"invoice_id":778264750,"customer_name":" Test Customer ","contact_num":9999999999,"email":"test@example.com","address_line_1":" 123 Test Street ","address_line_2":"Sector 1","city":"Noida","state":"Uttar Pradesh","pin_code":201301,"country":"India","warehouseId":304788,"payment_mode":"COD","total_amount":1,"suborders":[{"sku":"WEBHOOK-SKU","suborder_quantity":9,"selling_price":1}]}`

func TestFetchOrderFillsMissingContactsWithoutReplacingCurrentOrder(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "missing API contacts"
		if populated {
			name = "populated API contacts win"
		}
		t.Run(name, func(t *testing.T) {
			apiOrder := map[string]any{
				"order_id": 652462545, "invoice_id": 778264750, "reference_code": "TEST-100040", "warehouse_id": 304643,
				"payment_mode": "PrePaid", "order_status": "Open", "total_amount": 1049.0001,
				"customer_name": nil, "contact_num": nil, "email": nil, "address_line_1": nil, "address_line_2": "   ",
				"city": "Current API City", "state": "Uttar Pradesh", "pin_code": "201301", "country": "India",
				"order_items": []any{map[string]any{"sku": "212544", "suborder_quantity": 1, "selling_price": 1049}},
			}
			if populated {
				apiOrder["customer_name"], apiOrder["contact_num"], apiOrder["email"] = "API Customer", "8888888888", "api@example.com"
				apiOrder["address_line_1"], apiOrder["address_line_2"] = "Current API Street", "Current API Unit"
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/orders/V2/getOrderDetails" {
					t.Errorf("unexpected API call: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "Successful", "data": []any{apiOrder}})
			}))
			defer srv.Close()
			client, err := easyecom.NewClient(easyecom.Config{BaseURL: srv.URL, APIKey: "test-key", JWTToken: "test-jwt"})
			if err != nil {
				t.Fatal(err)
			}
			o, err := easyecom.NewSource(client, nil).FetchOrder(context.Background(), event.Event{ExternalOrderID: "652462545", InvoiceNumber: "778264750", ReferenceCode: "TEST-100040", Payload: []byte(contactWebhook)})
			if err != nil {
				t.Fatal(err)
			}
			want := order.Customer{Name: "Test Customer", Phone: "9999999999", Email: "test@example.com"}
			street, unit := "123 Test Street", "Sector 1"
			if populated {
				want = order.Customer{Name: "API Customer", Phone: "8888888888", Email: "api@example.com"}
				street, unit = "Current API Street", "Current API Unit"
			}
			if o.Customer != want {
				t.Fatalf("customer = %+v, want %+v", o.Customer, want)
			}
			for _, address := range []order.Address{o.ShippingAddress, o.BillingAddress} {
				if address.Name != want.Name || address.Phone != want.Phone || address.Email != want.Email || address.Line1 != street || address.Line2 != unit || address.City != "Current API City" {
					t.Fatalf("address = %+v", address)
				}
			}
			if calls != 1 || o.ExternalID != "652462545" || o.InvoiceNumber != "778264750" || o.ReferenceCode != "TEST-100040" || o.WarehouseID != "304643" || o.PaymentMode != order.PaymentPrepaid || o.TotalAmount != 1049.0001 || len(o.Items) != 1 || o.Items[0].SKU != "212544" || o.Items[0].Quantity != 1 || o.Items[0].UnitPrice != 1049 {
				t.Fatalf("API business data changed: %+v, calls %d", o, calls)
			}
		})
	}
}

func TestContactFallbackRequiresMatchingIdentityAndUsablePayload(t *testing.T) {
	_, src := newSource(t)
	base := order.Order{ExternalID: "652462545", Items: []order.Item{{SKU: "CURRENT", Quantity: 1}}}
	for _, tt := range []struct{ name, orderID, eventID, payload string }{
		{"missing payload", base.ExternalID, base.ExternalID, ""},
		{"malformed payload", base.ExternalID, base.ExternalID, `{`},
		{"missing payload identity", base.ExternalID, base.ExternalID, `{"customer_name":"Unverified"}`},
		{"different payload order", base.ExternalID, base.ExternalID, `{"order_id":123,"customer_name":"Wrong customer"}`},
		{"invoice match cannot override different order ID", base.ExternalID, base.ExternalID, `{"order_id":123,"invoice_id":652462545,"customer_name":"Wrong customer"}`},
		{"different fetched order", "999", base.ExternalID, contactWebhook},
		{"different event order", base.ExternalID, "999", contactWebhook},
		{"empty event identity", base.ExternalID, "", contactWebhook},
		{"empty fetched identity", "", base.ExternalID, contactWebhook},
		{"blank webhook contacts", base.ExternalID, base.ExternalID, `{"order_id":652462545,"customer_name":" ","contact_num":null,"address_line_1":" "}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := base
			input.ExternalID = tt.orderID
			got := src.EnrichOrderContact(input, event.Event{ExternalOrderID: tt.eventID, Payload: []byte(tt.payload)})
			if !reflect.DeepEqual(got, input) {
				t.Fatalf("unverified data enriched order: %+v", got)
			}
		})
	}
}

func TestContactFallbackFillsAddressFieldsAndIsIdempotent(t *testing.T) {
	_, src := newSource(t)
	input := order.Order{ExternalID: "652462545", Customer: order.Customer{Name: "API Name", TaxID: "KEEP-TAX-ID"}, ShippingAddress: order.Address{Line1: "API Street", City: "API City"}, Items: []order.Item{{SKU: "KEEP", Quantity: 2, UnitPrice: 5}}}
	ev := event.Event{ExternalOrderID: input.ExternalID, Payload: []byte(contactWebhook)}
	got := src.EnrichOrderContact(input, ev)
	if got.Customer.Name != "API Name" || got.Customer.TaxID != "KEEP-TAX-ID" || got.ShippingAddress.Line1 != "API Street" || got.ShippingAddress.City != "API City" {
		t.Fatalf("current fields overwritten: %+v", got)
	}
	if got.ShippingAddress.State != "Uttar Pradesh" || got.ShippingAddress.PostalCode != "201301" || got.ShippingAddress.Country != "India" || got.BillingAddress.City != "Noida" || got.BillingAddress.Line1 != "123 Test Street" {
		t.Fatalf("missing address fallback: %+v", got)
	}
	if !reflect.DeepEqual(src.EnrichOrderContact(got, ev), got) {
		t.Fatal("reapplying contact fallback changed the order")
	}
	if input.Customer.Phone != "" || input.ShippingAddress.Phone != "" || !reflect.DeepEqual(input.Items, got.Items) {
		t.Fatal("input or items were mutated")
	}
}

func TestWebhookFallbackUsesItsBillingAddressForMissingBillingFields(t *testing.T) {
	_, src := newSource(t)
	input := order.Order{ExternalID: "652462545", ShippingAddress: order.Address{Line1: "Current Shipping"}, BillingAddress: order.Address{City: "Current Billing City"}}
	ev := event.Event{ExternalOrderID: input.ExternalID, Payload: []byte(`{"order_id":652462545,"customer_name":"Shipping Name","address_line_1":"Webhook Shipping","city":"Noida","billing_name":"Billing Name","billing_address_1":"Webhook Billing","billing_city":"Mumbai","billing_mobile":8888888888}`)}
	got := src.EnrichOrderContact(input, ev)
	if got.ShippingAddress.Line1 != "Current Shipping" || got.BillingAddress.Line1 != "Webhook Billing" || got.BillingAddress.Name != "Billing Name" || got.BillingAddress.City != "Current Billing City" || got.BillingAddress.Phone != "8888888888" {
		t.Fatalf("billing overwritten by shipping: %+v", got)
	}
}
