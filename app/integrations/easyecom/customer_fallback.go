package easyecom

import (
	"encoding/json"
	"strings"

	"github.com/gluzo/integration-gateway/app/domain/order"
	"github.com/gluzo/integration-gateway/app/event"
	dtoorder "github.com/gluzo/integration-gateway/app/integrations/easyecom/dto/order"
	"github.com/gluzo/integration-gateway/app/vendor"
)

var _ vendor.OrderContactEnricher = (*Source)(nil)

// EnrichOrderContact fills blank contact/address fields using the original
// per-order webhook, including when MAP_ORDER resumes an already fetched run.
// A missing, malformed or differently identified payload leaves o unchanged.
// In particular, a single unrelated API result must never inherit the event's
// recipient. Current API values and every non-contact field remain untouched.
func (s *Source) EnrichOrderContact(o order.Order, ev event.Event) order.Order {
	id := strings.TrimSpace(o.ExternalID)
	if id == "" || id != strings.TrimSpace(ev.ExternalOrderID) || len(ev.Payload) == 0 {
		return o
	}
	var webhook dtoorder.Order
	if err := json.Unmarshal(ev.Payload, &webhook); err != nil {
		return o
	}
	webhookID := strings.TrimSpace(webhook.OrderID.String())
	if webhookID == "" {
		webhookID = strings.TrimSpace(webhook.InvoiceID.String())
	}
	if webhookID != id {
		return o
	}

	fillContactField(&o.Customer.Name, webhook.CustomerName)
	fillContactField(&o.Customer.Phone, webhook.ContactNum.String())
	fillContactField(&o.Customer.Email, webhook.Email)
	fillAddress := func(address *order.Address) {
		fillContactField(&address.Name, webhook.CustomerName)
		fillContactField(&address.Line1, webhook.AddressLine1)
		fillContactField(&address.Line2, webhook.AddressLine2)
		fillContactField(&address.City, webhook.City)
		fillContactField(&address.State, webhook.State)
		fillContactField(&address.Country, webhook.Country)
		fillContactField(&address.PostalCode, webhook.PinCode.String())
		fillContactField(&address.Phone, webhook.ContactNum.String())
		fillContactField(&address.Email, webhook.Email)
	}
	// The current EasyEcom mapper uses the same source address for both.
	fillAddress(&o.ShippingAddress)
	fillAddress(&o.BillingAddress)
	return o
}

func fillContactField(current *string, fallback string) {
	if strings.TrimSpace(*current) == "" && strings.TrimSpace(fallback) != "" {
		*current = strings.TrimSpace(fallback)
	}
}
