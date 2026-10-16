package order

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gluzo/integration-gateway/app/integrations/easyecom/dto"
)

// UnmarshalJSON accepts both order API/webhook warehouse spellings. Observed
// V1 location-assignment webhooks use warehouseId, whereas other payloads use
// warehouse_id. Normalize here so intake and subsequent payload mapping agree.
// import_warehouse_id identifies a different location and is never a fallback.
func (o *Order) UnmarshalJSON(data []byte) error {
	type plainOrder Order // Avoid recursively calling UnmarshalJSON.
	var wire struct {
		plainOrder
		WarehouseIDCamel dto.FlexString `json:"warehouseId"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	snake := strings.TrimSpace(wire.WarehouseID.String())
	camel := strings.TrimSpace(wire.WarehouseIDCamel.String())
	if snake != "" && camel != "" && snake != camel {
		return fmt.Errorf("conflicting warehouse_id and warehouseId")
	}
	if snake == "" {
		wire.WarehouseID = wire.WarehouseIDCamel
	}
	*o = Order(wire.plainOrder)
	return nil
}
