package admin_test

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gluzo/integration-gateway/app/admin"
	"github.com/gluzo/integration-gateway/app/intlog"
)

func TestDetailedTimelineEscapesAndCopiesExchanges(t *testing.T) {
	root := t.TempDir()
	rec, err := intlog.NewFileRecorder(root)
	if err != nil {
		t.Fatal(err)
	}
	payload := `</pre><script>alert("payload")</script>`
	rec.Record(context.Background(), intlog.Entry{CorrelationID: "INT-details", Action: "HTTP_CALL", Status: "SUCCESS",
		Request:  map[string]any{"headers": map[string]any{"Authorization": "Bearer private-token"}, "body": map[string]any{"sku": payload}},
		Response: map[string]any{"status_code": 200, "body": map[string]any{"order_id": "42"}},
	})
	_ = rec.Close()
	h, err := admin.NewHandler(intlog.NewReader(root), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	h.Register(r.Group("/admin"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/logs/INT-details", nil))
	if w.Code != 200 {
		t.Fatalf("timeline: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"Request and response data", "Copy request", "Copy response", "navigator.clipboard.writeText(text)", "***REDACTED***", `role="status"`} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline missing %q", want)
		}
	}
	if strings.Contains(body, payload) || strings.Contains(body, "private-token") {
		t.Fatal("timeline exposed active HTML or credentials")
	}
	if !strings.Contains(html.UnescapeString(body), `"order_id": "42"`) {
		t.Fatal("copyable response data missing")
	}
}
