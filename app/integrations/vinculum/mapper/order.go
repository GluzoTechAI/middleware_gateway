package mapper

import (
	"strings"
	"time"
	_ "time/tzdata" // Distroless images have no system timezone database.

	"github.com/gluzo/integration-gateway/app/domain/order"
	dtoorder "github.com/gluzo/integration-gateway/app/integrations/vinculum/dto/order"
	"github.com/gluzo/integration-gateway/app/vendor"
)

// DefaultCurrency preserves the gateway's INR fallback when the origin omits currency.
const DefaultCurrency = "INR"

// OrderOptions tunes an order mapping.
// TODO(VERIFY): obtain the tenant's ODRTYPE/ODRSTS values, payment methods and
// timezone from BCPL. Public examples establish no production defaults.
type OrderOptions struct {
	// VendorName is stamped on each line's `vendor` field where the
	// deployment uses it. Empty omits it.
	VendorName         string
	PrepaidOrderType   string
	CODOrderType       string
	PrepaidPaymentType string
	CODPaymentType     string
	InitialStatus      string
	Timezone           string
	Mode               string
	TaxInclusive       string
}

// ToCreateOrder converts a domain order into Vinculum's create-order
// document.
//
// Pure: no network, no database, no clock. The route supplies the location
// and the order supplies everything else, so the same order and route always
// produce the same document — which is what lets a resumed run submit
// exactly what was mapped rather than remapping against a moved world.
//
// The item SKUs must already be the vendor's. Translating them needs the
// integration's SKU map, which is configuration a pure mapper cannot load;
// the workflow does it before calling.
func ToCreateOrder(o order.Order, route vendor.Route, opts OrderOptions) (dtoorder.CreateOrderRequest, error) {
	var orderType, paymentMethod string
	switch o.PaymentMode {
	case order.PaymentCashOnDelivery:
		orderType, paymentMethod = opts.CODOrderType, opts.CODPaymentType
	case order.PaymentPrepaid:
		orderType, paymentMethod = opts.PrepaidOrderType, opts.PrepaidPaymentType
	default:
		return dtoorder.CreateOrderRequest{}, mappingErrorf("order %s: payment mode must be known before selecting Vinculum master values", o.ExternalID)
	}
	if strings.TrimSpace(orderType) == "" || strings.TrimSpace(paymentMethod) == "" || strings.TrimSpace(opts.InitialStatus) == "" {
		return dtoorder.CreateOrderRequest{}, mappingErrorf("order %s: configure VINCULUM_ORDER_TYPE_PREPAID/COD, VINCULUM_PAYMENT_TYPE_PREPAID/COD for its payment mode and VINCULUM_ORDER_STATUS with confirmed tenant values", o.ExternalID)
	}
	date, err := FormatOrderDate(o.OrderedAt, opts.Timezone)
	if err != nil {
		return dtoorder.CreateOrderRequest{}, err
	}
	req := dtoorder.CreateOrderRequest{
		// Gluzo's own order id becomes Vinculum's order number. Vinculum
		// rejecting a duplicate is what makes submission idempotent, so this
		// must be stable across retries and resumes — it is the order's
		// identity, not a generated reference.
		OrderNo:       strings.TrimSpace(o.ExternalID),
		OrderLocation: strings.TrimSpace(route.VendorReference),
		ReqType:       "create",
		OrderType:     strings.TrimSpace(orderType),
		PaymentType:   strings.TrimSpace(paymentMethod),
		Status:        strings.TrimSpace(opts.InitialStatus),
		OrderDate:     date,
		OrderCurrency: currency(o),
		OrderAmount:   o.TotalAmount,
	}

	ship := shippingAddress(o)
	req.ShipTo = firstNonEmpty(ship.Name, o.Customer.Name)
	req.ShipAddress = strings.TrimSpace(ship.Line1)
	req.ShipAddress2 = strings.TrimSpace(ship.Line2)
	req.ShipCity = strings.TrimSpace(ship.City)
	req.ShipState = strings.TrimSpace(ship.State)
	req.ShipCountry = strings.TrimSpace(ship.Country)
	req.ShipPincode = strings.TrimSpace(ship.PostalCode)
	req.ShipMobile = firstNonEmpty(ship.Phone, o.Customer.Phone)
	req.ShipEmail = firstNonEmpty(ship.Email, o.Customer.Email)
	bill := o.BillingAddress
	if bill.IsEmpty() {
		bill = ship
	}
	req.BillName = firstNonEmpty(bill.Name, o.Customer.Name)
	req.BillAddress, req.BillAddress2 = strings.TrimSpace(bill.Line1), strings.TrimSpace(bill.Line2)
	req.BillCity, req.BillState = strings.TrimSpace(bill.City), strings.TrimSpace(bill.State)
	req.BillCountry = strings.TrimSpace(bill.Country)
	req.BillPincode = strings.TrimSpace(bill.PostalCode)
	req.BillMobile, req.BillEmail = firstNonEmpty(bill.Phone, o.Customer.Phone), firstNonEmpty(bill.Email, o.Customer.Email)

	req.Items = make([]dtoorder.Line, 0, len(o.Items))
	for i, item := range o.Items {
		line := dtoorder.Line{
			// Line numbers are one-based and assigned by position. They are
			// persisted with the prepared document, so a shipment response
			// that names a line can be attributed to the item that shipped.
			LineNo:        i + 1,
			SKU:           strings.TrimSpace(item.SKU),
			OrderQty:      item.Quantity,
			UnitPrice:     item.UnitPrice,
			DiscountAmt:   item.Discount,
			TaxPercentage: item.TaxRate,
			Vendor:        strings.TrimSpace(opts.VendorName),
			Mode:          strings.TrimSpace(opts.Mode),
			TaxInclusive:  strings.TrimSpace(opts.TaxInclusive),
			// The origin's line identifier travels in a user-defined field
			// so a dispatch can be traced back to the EasyEcom suborder
			// without another lookup.
			UDF1: strings.TrimSpace(item.ExternalID),
		}
		// Omit optional MRP: selling price does not establish an actual MRP.
		req.Items = append(req.Items, line)
	}

	if err := checkShippable(o, req); err != nil {
		return dtoorder.CreateOrderRequest{}, err
	}
	if err := req.Validate(); err != nil {
		return dtoorder.CreateOrderRequest{}, mappingErrorf("order %s: %v", o.ExternalID, err)
	}
	return req, nil
}

// FormatOrderDate renders an order timestamp for Vinculum.
//
// TODO(VERIFY): confirm the configured timezone with the tenant. The official
// example establishes the representation, not a timezone. Read endpoint dates
// retain their independent formatter.
func FormatOrderDate(t time.Time, zone string) (string, error) {
	if t.IsZero() {
		return "", mappingErrorf("orderDate is required")
	}
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return "", mappingErrorf("configure VINCULUM_ORDER_TIMEZONE with the confirmed order timezone")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", mappingErrorf("invalid VINCULUM_ORDER_TIMEZONE %q", zone)
	}
	return t.In(loc).Format(dtoorder.OrderDateLayout), nil
}

// checkShippable preserves readable shipping errors; DTO validation also checks
// the documented conditional billing, country and email requirements.
func checkShippable(o order.Order, req dtoorder.CreateOrderRequest) error {
	missing := make([]string, 0, 6)
	for _, f := range []struct {
		name  string
		value string
	}{
		{"recipient name", req.ShipTo},
		{"address line 1", req.ShipAddress},
		{"city", req.ShipCity},
		{"state", req.ShipState},
		{"postal code", req.ShipPincode},
		{"phone", req.ShipMobile},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return mappingErrorf("order %s cannot be shipped: missing %s",
		o.ExternalID, strings.Join(missing, ", "))
}

func currency(o order.Order) string {
	if v := strings.TrimSpace(o.Currency); v != "" {
		return v
	}
	return DefaultCurrency
}

// shippingAddress prefers the shipping address and falls back to billing.
// An order that carries only one address is ordinary: EasyEcom's payload
// can omit a separate billing address.
func shippingAddress(o order.Order) order.Address {
	if !o.ShippingAddress.IsEmpty() {
		return o.ShippingAddress
	}
	return o.BillingAddress
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
