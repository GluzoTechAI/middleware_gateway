package webhook

import (
	"github.com/gin-gonic/gin"
	"github.com/gluzo/integration-gateway/app/auth"
	"github.com/gluzo/integration-gateway/app/correlation"
	"github.com/gluzo/integration-gateway/app/intlog"
)

// capture defers detailed entries until the acknowledgement is written. The
// original WEBHOOK_RECEIVED entries and timing remain unchanged when disabled.
func (h *Handler) capture(c *gin.Context) (func(intlog.Entry), func([]byte)) {
	ctx := c.Request.Context()
	if h.detailedLog == nil {
		return func(e intlog.Entry) { h.recorder.Record(ctx, e) }, func([]byte) {}
	}
	started := h.now()
	w := &captureWriter{ResponseWriter: c.Writer}
	c.Writer = w
	var entries []intlog.Entry
	return func(e intlog.Entry) { entries = append(entries, e) }, func(body []byte) {
		request := h.detailedLog.Request(c.Request.Method, c.Request.URL, c.Request.Header, body)
		response := h.detailedLog.Response(w.Status(), w.Header(), w.body)
		if len(entries) == 0 {
			e := intlog.Entry{Timestamp: started, CorrelationID: correlation.FromContext(ctx), Platform: h.parser.Platform(), Action: intlog.ActionWebhookReceived, Status: intlog.StatusInfo}
			if principal, ok := auth.PrincipalFromContext(ctx); ok {
				e.IntegrationID = principal.Integration.ID.String()
			}
			if w.Status() >= 400 {
				e.Status = intlog.StatusFailed
			}
			entries = append(entries, e)
		}
		for _, e := range entries {
			e.Request, e.Response = request, response
			e.DurationMS = h.now().Sub(started).Milliseconds()
			h.detailedLog.Record(ctx, e)
		}
	}
}

type captureWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *captureWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.capture(data[:n])
	return n, err
}

func (w *captureWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *captureWriter) capture(data []byte) {
	remaining := intlog.MaxHTTPBodyBytes + 1 - len(w.body)
	if remaining > 0 {
		w.body = append(w.body, data[:min(remaining, len(data))]...)
	}
}
