// Package order holds Vinculum order contracts. Source: official v4 parameter
// table and JSON example reviewed 19 October 2026; see the contract review.
// On 20 October 2026 the user reported successful BCPL creation using these
// fields. TODO(VERIFY): read back the recipient fields before closing B17.
package order

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gluzo/integration-gateway/app/integrations/vinculum/dto"
)

const (
	MaxOrderNoLength       = 50
	MaxOrderLocationLength = 3
	OrderDateLayout        = "02/01/2006 15:04:05"
)

// CreateOrderRequest serializes inside the required outer order object.
// orderAmount is the header total and items contains the lines.
// The table calls orderAmount an array, contradicting its total description.
// A decimal string matches both the example and the user's successful BCPL
// test reported on 20 October 2026; the actual source total remains unchanged.
type CreateOrderRequest struct {
	OrderNo       string  `json:"orderNo"`
	UniqueKey     string  `json:"uniqueKey,omitempty"`
	OrderLocation string  `json:"orderLocation"`
	ReqType       string  `json:"reqType"`
	OrderType     string  `json:"orderType"`
	PaymentType   string  `json:"paymentType"`
	Status        string  `json:"status"`
	OrderDate     string  `json:"orderDate"`
	OrderCurrency string  `json:"orderCurrency"`
	OrderAmount   float64 `json:"orderAmount,string"`
	AWBNo         string  `json:"awbNo,omitempty"`
	CustomerCode  string  `json:"customerCode,omitempty"`
	ShipTo        string  `json:"customerName,omitempty"`
	ShipAddress   string  `json:"shipAddress1,omitempty"`
	ShipAddress2  string  `json:"shipAddress2,omitempty"`
	ShipCity      string  `json:"shipCity,omitempty"`
	ShipState     string  `json:"shipState,omitempty"`
	ShipCountry   string  `json:"shipCountry,omitempty"`
	ShipPincode   string  `json:"shipPinCode,omitempty"`
	ShipMobile    string  `json:"shipPhone1,omitempty"`
	ShipEmail     string  `json:"shipEmail1,omitempty"`
	BillName      string  `json:"billName,omitempty"`
	BillAddress   string  `json:"billAddress1,omitempty"`
	BillAddress2  string  `json:"billAddress2,omitempty"`
	BillCity      string  `json:"billCity,omitempty"`
	BillState     string  `json:"billState,omitempty"`
	BillCountry   string  `json:"billCountry,omitempty"`
	BillPincode   string  `json:"billPinCode,omitempty"`
	BillMobile    string  `json:"billPhone1,omitempty"`
	BillEmail     string  `json:"billEmail1,omitempty"`
	Items         []Line  `json:"items"`
}

type requestFields CreateOrderRequest

func (r CreateOrderRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Order requestFields `json:"order"`
	}{requestFields(r)})
}

// UnmarshalJSON accepts the v4 order envelope only.
func (r *CreateOrderRequest) UnmarshalJSON(data []byte) error {
	var wrapper struct {
		Order *requestFields `json:"order"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return err
	}
	if wrapper.Order == nil {
		return errors.New("create-order document requires an outer order object")
	}
	*r = CreateOrderRequest(*wrapper.Order)
	return nil
}

type Line struct {
	LineNo        int     `json:"lineno,string"`
	SKU           string  `json:"sku"`
	OrderQty      int     `json:"orderQty,string"`
	UnitPrice     float64 `json:"unitPrice,string"`
	MRP           float64 `json:"mrp,string,omitempty"`
	DiscountAmt   float64 `json:"discountAmt,string,omitempty"`
	TaxPercentage float64 `json:"taxPercentage,string,omitempty"`
	TaxInclusive  string  `json:"taxInclusive,omitempty"`
	Mode          string  `json:"mode,omitempty"`
	Vendor        string  `json:"vendor,omitempty"`
	UDF1          string  `json:"udf1,omitempty"`
	UDF2          string  `json:"udf2,omitempty"`
	UDF3          string  `json:"udf3,omitempty"`
	UDF4          string  `json:"udf4,omitempty"`
}

func (r CreateOrderRequest) Validate() error {
	var errs []error
	check := func(name, value string, max int, required bool) {
		if required && strings.TrimSpace(value) == "" {
			errs = append(errs, fmt.Errorf("%s is required", name))
		}
		if utf8.RuneCountInString(value) > max {
			errs = append(errs, fmt.Errorf("%s exceeds %d characters", name, max))
		}
	}
	check("orderNo", r.OrderNo, MaxOrderNoLength, true)
	check("orderLocation", r.OrderLocation, MaxOrderLocationLength, true)
	check("orderType", r.OrderType, 200, true)
	check("paymentType", r.PaymentType, 50, true)
	check("status", r.Status, 50, true)
	check("orderCurrency", r.OrderCurrency, 10, true)
	check("customerCode", r.CustomerCode, 20, false)
	if r.ReqType != "create" {
		errs = append(errs, errors.New("reqType must be create"))
	}
	if _, err := time.Parse(OrderDateLayout, r.OrderDate); err != nil {
		errs = append(errs, errors.New("orderDate must use DD/MM/YYYY HH:mm:ss"))
	}
	if !validAmount(r.OrderAmount) {
		errs = append(errs, errors.New("orderAmount must be finite and nonnegative"))
	}
	required := strings.TrimSpace(r.CustomerCode) == ""
	for _, f := range []struct {
		name, value string
		max         int
		required    bool
	}{
		{"customerName", r.ShipTo, 100, required}, {"shipAddress1", r.ShipAddress, 1000, required}, {"shipAddress2", r.ShipAddress2, 200, false},
		{"shipCity", r.ShipCity, 40, required}, {"shipState", r.ShipState, 50, required}, {"shipCountry", r.ShipCountry, 50, required},
		{"shipPinCode", r.ShipPincode, 50, required}, {"shipPhone1", r.ShipMobile, 20, required}, {"shipEmail1", r.ShipEmail, 255, required},
		{"billName", r.BillName, 200, required}, {"billAddress1", r.BillAddress, 1000, required}, {"billAddress2", r.BillAddress2, 200, false},
		{"billCity", r.BillCity, 40, required}, {"billState", r.BillState, 50, required}, {"billCountry", r.BillCountry, 50, required},
		{"billPinCode", r.BillPincode, 50, required}, {"billPhone1", r.BillMobile, 20, required}, {"billEmail1", r.BillEmail, 255, required},
	} {
		check(f.name, f.value, f.max, f.required)
	}
	if len(r.Items) == 0 {
		errs = append(errs, errors.New("at least one order line is required"))
	}
	seen := map[int]bool{}
	for i, line := range r.Items {
		check(fmt.Sprintf("items[%d].sku", i), line.SKU, 50, true)
		if line.OrderQty <= 0 || line.LineNo <= 0 || seen[line.LineNo] {
			errs = append(errs, fmt.Errorf("line %d requires positive quantity and a unique positive line number", i))
		}
		seen[line.LineNo] = true
		if !validAmount(line.UnitPrice) || !validAmount(line.MRP) || !validAmount(line.DiscountAmt) || !validAmount(line.TaxPercentage) {
			errs = append(errs, fmt.Errorf("line %d has invalid amounts", i))
		}
		if line.Mode != "" && line.Mode != "wms" && line.Mode != "dropship" {
			errs = append(errs, fmt.Errorf("line %d mode must be wms or dropship", i))
		}
		if line.TaxInclusive != "" && line.TaxInclusive != "Yes" && line.TaxInclusive != "No" {
			errs = append(errs, fmt.Errorf("line %d taxInclusive must be Yes or No", i))
		}
		check("vendor", line.Vendor, 10, false)
		for _, v := range []string{line.UDF1, line.UDF2, line.UDF3, line.UDF4} {
			check("line udf", v, 50, false)
		}
	}
	return errors.Join(errs...)
}

func validAmount(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

// CreateOrderResponse follows the documented requestStatus acknowledgement.
type CreateOrderResponse struct {
	dto.Envelope
	RequestStatus *CreateOrderResult `json:"requestStatus"`
}

type CreateOrderResult struct {
	UniqueKey  dto.FlexString `json:"uniqueKey"`
	RequestKey dto.FlexString `json:"requestKey"`
	OutputKey  dto.FlexString `json:"outputKey"`
	DSOrderNo  dto.FlexString `json:"dsOrderNo"`
	Status     string         `json:"status"`
	ErrorCode  dto.FlexString `json:"errorCode"`
	ErrorDesc  string         `json:"errorDesc"`
}

func (r CreateOrderResponse) VendorOrderNo() string {
	if r.RequestStatus == nil {
		return ""
	}
	if id := strings.TrimSpace(r.RequestStatus.OutputKey.String()); id != "" {
		return id
	}
	return strings.TrimSpace(r.RequestStatus.DSOrderNo.String())
}
