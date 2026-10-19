package mapper_test

import (
	"encoding/json"
	"testing"

	dtoorder "github.com/gluzo/integration-gateway/app/integrations/easyecom/dto/order"
	"github.com/gluzo/integration-gateway/app/integrations/easyecom/mapper"
)

func TestOrderPreservesDistinctBillingAndInvoiceCurrency(t *testing.T) {
	var src dtoorder.Order
	err := json.Unmarshal([]byte(`{"order_id":123,"order_date":"2026-10-15 15:30:00","customer_name":"Shipping Customer","email":"customer@example.com","contact_num":"9999999999","address_line_1":"Shipping Street","city":"Noida","state":"Uttar Pradesh","country":"India","pin_code":201301,"billing_name":"Billing Customer","billing_address_1":"Billing Street","billing_address_2":"Billing Unit","billing_city":"Mumbai","billing_state":"Maharashtra","billing_country":"India","billing_pin_code":400001,"billing_mobile":8888888888,"invoice_currency_code":"USD","payment_mode":"PrePaid","total_amount":50,"order_items":[{"sku":"A","suborder_quantity":1,"selling_price":50}]}`), &src)
	if err != nil {
		t.Fatal(err)
	}
	o, err := mapper.ToDomainOrder(src)
	if err != nil {
		t.Fatal(err)
	}
	if o.Currency != "USD" || o.ShippingAddress.Line1 != "Shipping Street" || o.BillingAddress.Line1 != "Billing Street" || o.BillingAddress.Line2 != "Billing Unit" || o.BillingAddress.City != "Mumbai" || o.BillingAddress.Name != "Billing Customer" || o.BillingAddress.Phone != "8888888888" || o.BillingAddress.Email != "customer@example.com" || o.BillingAddress.PostalCode != "400001" {
		t.Fatalf("billing or currency lost: %+v", o)
	}
}
