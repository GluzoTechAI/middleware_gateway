package intlog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gluzo/integration-gateway/app/intlog"
)

func TestHTTPLogBoundsAndEncodedSecrets(t *testing.T) {
	l := intlog.NewHTTPLogger(&intlog.Memory{}, 1024)
	u, _ := url.Parse("https://user:pass@example.com/orders?api_key=query-secret&order_id=42")
	form := url.Values{"data": {`{"nested":{"password":"form-secret","sku":"A"}}`}, "location_key": {"location-secret"}}.Encode()
	m := l.Request("POST", u, http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "ApiOwner": {"owner-secret"}}, []byte(form))
	data, _ := json.Marshal(m)
	for _, secret := range []string{"user:pass", "query-secret", "form-secret", "location-secret", "owner-secret"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("leaked %s", secret)
		}
	}
	if !strings.Contains(string(data), `"sku":"A"`) {
		t.Fatalf("lost form JSON: %s", data)
	}
	for _, body := range []string{strings.Repeat("x", 1025), `{"password":"unfinished`, `<password>xml-secret</password>`} {
		m := l.Response(200, http.Header{}, []byte(body))
		if m["body_omitted"] == nil || m["body"] != nil {
			t.Fatalf("unsafe body retained: %+v", m)
		}
	}
	m = l.Response(200, http.Header{}, []byte(`{"data":"{\"password\":\"nested-secret\",\"id\":42}"}`))
	data, _ = json.Marshal(m)
	if strings.Contains(string(data), "nested-secret") {
		t.Fatalf("encoded JSON bypassed sanitizer: %s", data)
	}
	m = l.Response(200, nil, []byte(`{"phone":9876543210}`))
	if m["body"].(map[string]any)["phone"] != "******3210" {
		t.Fatalf("numeric phone unmasked: %+v", m)
	}
}

func TestDetailedEntryRoundTripNearBodyLimit(t *testing.T) {
	root := t.TempDir()
	recorder, err := intlog.NewFileRecorder(root)
	if err != nil {
		t.Fatal(err)
	}
	l := intlog.NewHTTPLogger(recorder, intlog.MaxHTTPBodyBytes)
	// JSON escaping expands this significantly on disk.
	body := []byte(`{"data":"` + strings.Repeat("<", intlog.MaxHTTPBodyBytes-20) + `"}`)
	u, _ := url.Parse("https://example.com/orders")
	l.Record(context.Background(), intlog.Entry{CorrelationID: "INT-large", Action: "HTTP_CALL", Status: "SUCCESS", Request: l.Request("POST", u, nil, body), Response: l.Response(200, nil, body)})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := intlog.NewReader(root).Timeline(context.Background(), "INT-large")
	if err != nil || len(entries) != 1 {
		t.Fatalf("unreadable detailed log: %d, %v", len(entries), err)
	}
}
