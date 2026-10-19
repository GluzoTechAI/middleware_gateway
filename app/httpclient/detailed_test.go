package httpclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gluzo/integration-gateway/app/correlation"
	"github.com/gluzo/integration-gateway/app/httpclient"
	"github.com/gluzo/integration-gateway/app/intlog"
)

func TestDetailedLogRetriesAndPreservesTraffic(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		data, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer request-secret" || !strings.Contains(string(data), "payload-secret") {
			t.Error("logging altered the outgoing credentials or body")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=response-secret")
		if calls == 1 {
			w.WriteHeader(503)
		}
		_, _ = io.WriteString(w, `{"access_token":"body-secret","id":"42","email":"alice@example.com"}`)
	}))
	defer srv.Close()
	mem := &intlog.Memory{}
	c := newClient(t, srv.URL, &recordingSleep{}, httpclient.WithDetailedLog(intlog.NewHTTPLogger(mem, 4096)))
	ctx := intlog.WithCallContext(correlation.WithID(context.Background(), "INT-detail"), intlog.Entry{
		Workflow: "ORDER_SYNC", Action: "FETCH_ORDER", Attempt: 2, Platform: "easyecom", Integration: "vinculum", IntegrationID: "pipeline-1", ExternalOrderID: "order-1",
	})
	resp, err := c.Do(ctx, httpclient.Request{Method: "POST", Path: "/orders", Operation: "GetOrder",
		Header: http.Header{"Authorization": {"Bearer request-secret"}, "X-Trace": {"trace-1"}},
		Query:  url.Values{"token": {"query-secret"}, "order_id": {"42"}},
		Body:   map[string]any{"password": "payload-secret", "sku": "ABC"},
	})
	if err != nil || resp.Attempts != 2 || !strings.Contains(string(resp.Body), "body-secret") {
		t.Fatalf("response changed: %+v, %v", resp, err)
	}
	entries := mem.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	for i, e := range entries {
		if e.CorrelationID != "INT-detail" || e.Workflow != "ORDER_SYNC" || e.IntegrationID != "pipeline-1" || e.ExternalOrderID != "order-1" || e.Action != "HTTP_CALL" || e.Attempt != i+1 || e.Details["workflow_action"] != "FETCH_ORDER" || e.Details["workflow_attempt"] != 2 {
			t.Fatalf("missing action correlation: %+v", e)
		}
		data, _ := json.Marshal(e)
		for _, secret := range []string{"request-secret", "payload-secret", "query-secret", "response-secret", "body-secret", "alice@example.com"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("captured secret %q", secret)
			}
		}
		if e.Request["headers"].(map[string]any)["X-Trace"] != "trace-1" || e.Response["body"].(map[string]any)["id"] != "42" {
			t.Fatal("useful HTTP data missing")
		}
	}
	if entries[0].Status != intlog.StatusFailed || entries[0].Error == nil || entries[1].Status != intlog.StatusSuccess {
		t.Fatal("retry outcomes missing")
	}
}

type failedTransport struct{}

func (failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection failed")
}

func TestDetailedTransportFailureAndDisabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		mem := &intlog.Memory{}
		var detail *intlog.HTTPLogger
		if enabled {
			detail = intlog.NewHTTPLogger(mem, 1024)
		}
		c := newClient(t, "http://unused.example", &recordingSleep{}, httpclient.WithHTTPClient(&http.Client{Transport: failedTransport{}}), httpclient.WithRetryPolicy(httpclient.NoRetry()), httpclient.WithDetailedLog(detail))
		_, err := c.Do(correlation.WithID(context.Background(), "INT-failure"), httpclient.Request{Method: "GET", Path: "/orders"})
		if err == nil {
			t.Fatal("expected transport error")
		}
		entries := mem.Entries()
		if !enabled && len(entries) != 0 {
			t.Fatal("capture was enabled by default")
		}
		if enabled && (len(entries) != 1 || entries[0].Response != nil || entries[0].Error == nil || entries[0].Request == nil) {
			t.Fatalf("failure exchange = %+v", entries)
		}
	}
}
