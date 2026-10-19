package webhook_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gluzo/integration-gateway/app/idempotency"
	"github.com/gluzo/integration-gateway/app/queue"
)

func detailedLogging(e *env) (idempotency.Store, queue.Publisher) {
	e.detailedLogging = true
	return e.store, e.queue
}

func TestWebhookDetailedCapture(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var opts []envOption
		if enabled {
			opts = append(opts, detailedLogging)
		}
		e := newEnv(t, opts...)
		// A second delivery must retain the original run's correlation ID.
		for attempt := 0; attempt < 2; attempt++ {
			w := e.post("/webhooks/easyecom", validPayload, authHeaders())
			if w.Code != http.StatusAccepted && w.Code != http.StatusOK {
				t.Fatalf("webhook: %d %s", w.Code, w.Body.String())
			}
			entry := e.recorder.Entries()[attempt]
			if !enabled {
				if entry.Request != nil || entry.Response != nil {
					t.Fatal("details retained while disabled")
				}
				continue
			}
			if entry.Request == nil || entry.Response == nil || entry.Response["status_code"] != w.Code {
				t.Fatalf("missing webhook exchange: %+v", entry)
			}
			data, _ := json.Marshal(entry)
			for _, secret := range []string{platformKey, accessToken} {
				if strings.Contains(string(data), secret) {
					t.Fatalf("credential retained: %s", secret)
				}
			}
			payload := entry.Request["body"].([]any)
			if payload[0].(map[string]any)["order_id"].(json.Number).String() != "9876543" {
				t.Fatal("webhook payload missing")
			}
			var ack any
			_ = json.Unmarshal(w.Body.Bytes(), &ack)
			expected, _ := json.Marshal(ack)
			actual, _ := json.Marshal(entry.Response["body"])
			if string(expected) != string(actual) {
				t.Fatalf("ack mismatch: %s / %s", expected, actual)
			}
		}
		entries := e.recorder.Entries()
		if entries[0].CorrelationID != entries[1].CorrelationID {
			t.Fatal("duplicate detached from original workflow")
		}
	}
}

func TestWebhookDetailedBatchAndRejection(t *testing.T) {
	e := newEnv(t, detailedLogging)
	batch := strings.TrimSuffix(validPayload, "]") + "," + strings.TrimPrefix(strings.ReplaceAll(validPayload, "9876543", "9876544"), "[")
	w := e.post("/webhooks/easyecom", batch, authHeaders())
	entries := e.recorder.Entries()
	if w.Code != 202 || len(entries) != 2 {
		t.Fatalf("batch failed: %d, %s, %+v", w.Code, w.Body.String(), entries)
	}
	if entries[0].CorrelationID == entries[1].CorrelationID || len(entries[1].Request["body"].([]any)) != 2 {
		t.Fatal("batch correlation or payload missing")
	}
	e = newEnv(t, detailedLogging)
	w = e.post("/webhooks/easyecom", `{"password":"incomplete`, authHeaders())
	entries = e.recorder.Entries()
	if w.Code != 400 || len(entries) != 1 || entries[0].Request["body_omitted"] == nil {
		t.Fatalf("rejection capture: %d %+v", w.Code, entries)
	}
}
