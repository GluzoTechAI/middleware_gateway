package vinculum

import (
	"context"
	"strings"

	"github.com/gluzo/integration-gateway/app/apperror"
	dtoorder "github.com/gluzo/integration-gateway/app/integrations/vinculum/dto/order"
)

// createOrderPath is the order creation endpoint.
const createOrderPath = "/RestWS/api/eretail/v4/order/create"

// duplicateKeywords recognise Vinculum's rejection of an order number it
// already holds.
//
// TODO(VERIFY): the specification states that a duplicate is rejected but
// does not give the code or wording. Matching on keywords is a guess, and
// VINCULUM_DUPLICATE_ORDER_CODES lets a deployment name the exact codes
// instead once BCPL supply them.
//
// Failing to recognise a duplicate is safe, which is why a guess is
// tolerable here: the submission is reported as an error, the workflow
// retries, Vinculum rejects it again, and the run fails permanently with the
// vendor's own message in the log. The order is stuck and visible — it is
// never duplicated, because Vinculum did the rejecting.
var duplicateKeywords = []string{
	"duplicate",
	"already exist",
	"already present",
	"already processed",
	"order no already",
}

// CreateOrder submits one order for fulfilment.
//
// The call waits for the rate limiter first. Vinculum documents 80 calls per
// five minutes on this endpoint, and the queue paces work but does not
// enforce a ceiling: a backlog drains as fast as the workers can go.
func (c *Client) CreateOrder(ctx context.Context, req dtoorder.CreateOrderRequest) (*dtoorder.CreateOrderResponse, error) {
	const op = "CreateOrder"
	if err := req.Validate(); err != nil {
		return nil, invalidRequest(op, err)
	}
	if c.orderLimiter != nil {
		if err := c.orderLimiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	var resp dtoorder.CreateOrderResponse
	if err := c.post(ctx, createOrderPath, op, req, &resp); err != nil {
		return nil, err
	}
	// A nonzero envelope code can say only "Generic Error" while requestStatus
	// identifies the rejected SKU or tenant configuration. Keep the outer code
	// and generic message, but include the actionable nested rejection as well.
	envelope := resp.Envelope
	if resp.RequestStatus != nil {
		if detail := strings.TrimSpace(resp.RequestStatus.ErrorDesc); detail != "" {
			message := strings.TrimSpace(envelope.ResponseMessage)
			if message == "" {
				envelope.ResponseMessage = detail
			} else if message != detail {
				envelope.ResponseMessage = message + ": " + detail
			}
		}
	}
	if err := checkEnvelope(op, envelope); err != nil {
		return nil, err
	}
	if resp.RequestStatus == nil {
		return nil, &apperror.Error{Category: apperror.ExternalAPI, Message: "Vinculum create response has no requestStatus", Integration: PlatformName, Operation: op}
	}
	ack := resp.RequestStatus
	if !strings.EqualFold(strings.TrimSpace(ack.Status), "Success") || strings.TrimSpace(ack.ErrorDesc) != "" || (ack.ErrorCode.String() != "" && ack.ErrorCode.String() != "0") {
		return nil, &apperror.Error{Category: apperror.ExternalAPI, Message: "vinculum rejected the order", ExternalCode: ack.ErrorCode.String(), ExternalMessage: strings.TrimSpace(ack.ErrorDesc), Integration: PlatformName, Operation: op}
	}
	if resp.VendorOrderNo() == "" {
		return nil, &apperror.Error{Category: apperror.ExternalAPI, Message: "Vinculum acknowledgement has no outputKey or dsOrderNo", Integration: PlatformName, Operation: op}
	}
	return &resp, nil
}

// IsDuplicateRejection reports whether a response describes an order Vinculum
// already holds.
//
// It is exported because the vendor adapter reads it from the error the
// envelope check produced, and a test needs to exercise the recognition
// directly.
func (c *Client) IsDuplicateRejection(code, message string) bool {
	for _, configured := range c.duplicateCodes {
		if configured != "" && strings.EqualFold(configured, strings.TrimSpace(code)) {
			return true
		}
	}
	lower := strings.ToLower(message)
	for _, keyword := range duplicateKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}
