package intlog

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/gluzo/integration-gateway/app/correlation"
)

// MaxHTTPBodyBytes keeps two captured bodies, including JSON escaping, within
// the log reader's line limit. Oversized bodies are omitted before parsing:
// cutting JSON first could expose a partial credential to the sanitizer.
const MaxHTTPBodyBytes = 256 << 10

type callContextKey struct{}

// WithCallContext attaches an immutable snapshot of the action to outbound
// calls. It deliberately contains no workflow payload or mutable state.
func WithCallContext(ctx context.Context, entry Entry) context.Context {
	return context.WithValue(ctx, callContextKey{}, entry)
}

// CallEntry returns the action metadata inherited by an outbound call.
func CallEntry(ctx context.Context) Entry {
	e, _ := ctx.Value(callContextKey{}).(Entry)
	if e.CorrelationID == "" {
		e.CorrelationID = correlation.FromContext(ctx)
	}
	return e
}

// HTTPLogger captures sanitized HTTP exchanges only when explicitly wired in.
// A nil logger disables capture without parsing or retaining any extra data.
type HTTPLogger struct {
	recorder  Recorder
	limit     int
	sanitizer *Sanitizer
}

func NewHTTPLogger(recorder Recorder, maxBodyBytes int) *HTTPLogger {
	if recorder == nil {
		return nil
	}
	if maxBodyBytes < 1 || maxBodyBytes > MaxHTTPBodyBytes {
		maxBodyBytes = MaxHTTPBodyBytes
	}
	return &HTTPLogger{recorder: recorder, limit: maxBodyBytes, sanitizer: NewSanitizer()}
}

func (l *HTTPLogger) Request(method string, target *url.URL, headers http.Header, body []byte) map[string]any {
	if l == nil {
		return nil
	}
	u := *target
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	m := l.message(headers, body)
	m["method"], m["url"] = method, u.String()
	if target.RawQuery != "" {
		if len(target.RawQuery) > 16<<10 {
			m["query_omitted"] = "query exceeds capture limit"
		} else {
			m["query"] = l.sanitizer.Map(valuesMap(target.Query()))
		}
	}
	return m
}

func (l *HTTPLogger) Response(status int, headers http.Header, body []byte) map[string]any {
	if l == nil {
		return nil
	}
	m := l.message(headers, body)
	m["status_code"] = status
	return m
}

func (l *HTTPLogger) Record(ctx context.Context, e Entry) {
	if l != nil {
		l.recorder.Record(context.WithoutCancel(ctx), l.sanitizer.Entry(e))
	}
}

func (l *HTTPLogger) message(headers http.Header, body []byte) map[string]any {
	m := map[string]any{"body_bytes": len(body)}
	headerBytes := 0
	for k, vs := range headers {
		headerBytes += len(k)
		for _, v := range vs {
			headerBytes += len(v)
		}
	}
	if headerBytes > 16<<10 {
		m["headers_omitted"] = "headers exceed capture limit"
	} else {
		m["headers"] = l.sanitizer.Headers(headers)
	}
	switch {
	case len(body) == 0:
		m["body"] = nil
	case len(body) > l.limit:
		m["body_omitted"] = "body exceeds capture limit"
	default:
		var v any
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		if json.Valid(body) && dec.Decode(&v) == nil {
			m["body"] = l.sanitizer.value("", decodeNestedJSON(v, 0))
		} else if ct, _, _ := mime.ParseMediaType(headers.Get("Content-Type")); ct == "application/x-www-form-urlencoded" {
			if form, err := url.ParseQuery(string(body)); err == nil {
				m["body"] = l.sanitizer.value("", decodeNestedJSON(valuesMap(form), 0))
			} else {
				m["body_omitted"] = "invalid form body"
			}
		} else {
			m["body_omitted"] = "body is not valid JSON or form data"
		}
	}
	return m
}

func valuesMap(values url.Values) map[string]any {
	m := make(map[string]any, len(values))
	for k, vs := range values {
		if len(vs) == 1 {
			m[k] = vs[0]
		} else {
			m[k] = vs
		}
	}
	return m
}

// Vendors sometimes embed JSON in a form field or JSON string. Decode it
// before sanitizing so credentials inside it cannot bypass key redaction.
func decodeNestedJSON(v any, depth int) any {
	if depth > 32 {
		return "[nested data omitted]"
	}
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			t[k] = decodeNestedJSON(inner, depth+1)
		}
	case []any:
		for i, inner := range t {
			t[i] = decodeNestedJSON(inner, depth+1)
		}
	case []string:
		out := make([]any, len(t))
		for i, inner := range t {
			out[i] = decodeNestedJSON(inner, depth+1)
		}
		return out
	case string:
		trimmed := strings.TrimSpace(t)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var inner any
			dec := json.NewDecoder(strings.NewReader(t))
			dec.UseNumber()
			if json.Valid([]byte(t)) && dec.Decode(&inner) == nil {
				return decodeNestedJSON(inner, depth+1)
			}
		}
	}
	return v
}
