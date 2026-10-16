package admin_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/gluzo/integration-gateway/app/admin"
	"github.com/gluzo/integration-gateway/app/event"
	"github.com/gluzo/integration-gateway/app/health"
	"github.com/gluzo/integration-gateway/app/httpserver"
	"github.com/gluzo/integration-gateway/app/intlog"
	"github.com/gluzo/integration-gateway/app/workflow"
	"github.com/gluzo/integration-gateway/app/workflowstate"
)

type browserEnv struct {
	redis   *miniredis.Miniredis
	client  *redis.Client
	handler *admin.Handler
	router  http.Handler
	resumer *fakeResumer
}

func newBrowserEnv(t *testing.T) *browserEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	repo := workflowstate.NewMemoryRepository()
	st := workflow.NewState("ORDER_SYNC", event.Event{CorrelationID: "INT-browser", ExternalOrderID: "123", Payload: []byte(`{}`)}, "job", time.Now())
	st.Status = workflow.StatusFailed
	st.NextAction = "FETCH_ORDER"
	if err := repo.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	resumer := &fakeResumer{}
	h, err := admin.NewHandler(intlog.NewReader(t.TempDir()), repo, resumer, logger)
	if err != nil {
		t.Fatal(err)
	}
	e := &browserEnv{redis: mr, client: client, handler: h, resumer: resumer}
	e.router = e.newRouter(adminToken)
	return e
}

func (e *browserEnv) newRouter(token string) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpserver.NewRouter(httpserver.Dependencies{
		Logger: logger, Health: health.NewHandler("gateway", "test", time.Second, logger),
		MaxBodyBytes: 1 << 20, Admin: e.handler, AdminToken: token, AdminSessions: e.client,
	})
}

func browserRequest(router http.Handler, method, path string, cookie *http.Cookie, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, "https://gateway.gluzone.dev"+path, body)
	req.Header.Set("Accept", "text/html")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func csrfFrom(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	matches := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(matches) != 2 {
		t.Fatalf("missing CSRF form field: %s", w.Body.String())
	}
	return matches[1]
}

func cookieFrom(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "__Host-gluzo_admin_session" {
			return cookie
		}
	}
	t.Fatal("missing session cookie")
	return nil
}

func loginBrowser(t *testing.T, e *browserEnv) *http.Cookie {
	t.Helper()
	page := browserRequest(e.router, http.MethodGet, "/admin/login", nil, nil, nil)
	w := browserRequest(e.router, http.MethodPost, "/admin/login", cookieFrom(t, page), url.Values{"token": {adminToken}, "_csrf": {csrfFrom(t, page)}}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("login: status %d, body %s", w.Code, w.Body.String())
	}
	return cookieFrom(t, w)
}

func TestBrowserLoginLifecycle(t *testing.T) {
	e := newBrowserEnv(t)
	page := browserRequest(e.router, http.MethodGet, "/admin/login", nil, nil, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Admin sign in") {
		t.Fatalf("login page: %d %s", page.Code, page.Body.String())
	}
	if page.Header().Get("Cache-Control") != "no-store" || strings.Contains(page.Body.String(), adminToken) {
		t.Fatal("login page must be uncached and must not contain the configured credential")
	}
	anonymous := cookieFrom(t, page)
	if !anonymous.Secure || !anonymous.HttpOnly || anonymous.SameSite != http.SameSiteStrictMode || anonymous.Path != "/" || anonymous.Domain != "" || anonymous.MaxAge != 600 {
		t.Fatalf("unsafe login cookie: %+v", anonymous)
	}
	csrf := csrfFrom(t, page)
	w := browserRequest(e.router, http.MethodGet, "/admin", anonymous, nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/login" {
		t.Fatal("anonymous sessions must not authenticate")
	}
	w = browserRequest(e.router, http.MethodPost, "/admin/login", anonymous, url.Values{"token": {"wrong-token"}, "_csrf": {csrf}}, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "Invalid admin token") || strings.Contains(w.Body.String(), "wrong-token") {
		t.Fatalf("invalid login: %d %s", w.Code, w.Body.String())
	}
	w = browserRequest(e.router, http.MethodPost, "/admin/login", anonymous, url.Values{"token": {adminToken}, "_csrf": {csrf}}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin" {
		t.Fatalf("valid login: %d %s", w.Code, w.Body.String())
	}
	session := cookieFrom(t, w)
	if session.Value == anonymous.Value || session.MaxAge != 8*60*60 || !session.Secure || !session.HttpOnly {
		t.Fatal("login must rotate the ID and issue a secure eight-hour session")
	}
	if w = browserRequest(e.router, http.MethodGet, "/admin", anonymous, nil, nil); w.Code != http.StatusSeeOther {
		t.Fatal("old anonymous cookie must not authenticate after login")
	}
	// The opaque ID and plaintext admin token must not be persisted in Redis.
	for _, key := range e.redis.Keys() {
		if strings.Contains(key, session.Value) || strings.Contains(key, adminToken) {
			t.Fatal("Redis key leaked a session ID or admin token")
		}
		if strings.Contains(key, ":session:") {
			value, err := e.redis.Get(key)
			if err != nil || strings.Contains(value, adminToken) || strings.Contains(value, session.Value) {
				t.Fatal("Redis session leaked plaintext credentials")
			}
		}
	}
	// A second router shares sessions, as another replica or a restarted binary would.
	w = browserRequest(e.newRouter(adminToken), http.MethodGet, "/admin", session, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Admin home") || !strings.Contains(w.Body.String(), "/admin/logout") {
		t.Fatalf("home: %d %s", w.Code, w.Body.String())
	}
	csrf = csrfFrom(t, w)
	if w = browserRequest(e.router, http.MethodGet, "/admin/login", session, nil, nil); w.Code != http.StatusSeeOther {
		t.Fatal("authenticated visitors should land on home")
	}
	w = browserRequest(e.router, http.MethodPost, "/admin/logout", session, url.Values{"_csrf": {csrf}}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/login" || cookieFrom(t, w).MaxAge != -1 {
		t.Fatalf("logout failed: %d", w.Code)
	}
	if w = browserRequest(e.router, http.MethodGet, "/admin", session, nil, nil); w.Code != http.StatusSeeOther {
		t.Fatal("logged-out cookie must not authenticate")
	}
}

func TestBrowserSessionExpiryRotationAndTampering(t *testing.T) {
	e := newBrowserEnv(t)
	cookie := loginBrowser(t, e)
	tampered := *cookie
	tampered.Value = strings.Repeat("x", 43)
	for _, tc := range []struct {
		name   string
		router http.Handler
		cookie *http.Cookie
	}{
		{"tampered cookie", e.router, &tampered},
		{"rotated credential", e.newRouter("replacement-admin-token"), cookie},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := browserRequest(tc.router, http.MethodGet, "/admin", tc.cookie, nil, nil); w.Code != http.StatusSeeOther {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	e.redis.FastForward(8*time.Hour + time.Second)
	if w := browserRequest(e.router, http.MethodGet, "/admin", cookie, nil, nil); w.Code != http.StatusSeeOther {
		t.Fatalf("expired cookie accepted: %d", w.Code)
	}
}

func TestBrowserCSRFProtectsLoginAndExistingActions(t *testing.T) {
	e := newBrowserEnv(t)
	page := browserRequest(e.router, http.MethodGet, "/admin/login", nil, nil, nil)
	anonymous := cookieFrom(t, page)
	for _, csrf := range []string{"", "wrong"} {
		w := browserRequest(e.router, http.MethodPost, "/admin/login", anonymous, url.Values{"token": {adminToken}, "_csrf": {csrf}}, nil)
		if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
			t.Fatalf("login without CSRF accepted: %d", w.Code)
		}
	}
	cookie := loginBrowser(t, e)
	w := browserRequest(e.router, http.MethodGet, "/admin/logs/INT-browser", cookie, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `action="/admin/workflows/INT-browser/resume"`) {
		t.Fatalf("timeline: %d %s", w.Code, w.Body.String())
	}
	csrf := csrfFrom(t, w)
	for _, path := range []string{"/admin/logout", "/admin/workflows/INT-browser/resume"} {
		for _, bad := range []string{"", "wrong"} {
			w := browserRequest(e.router, http.MethodPost, path, cookie, url.Values{"_csrf": {bad}}, nil)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s without CSRF: %d", path, w.Code)
			}
		}
	}
	w = browserRequest(e.router, http.MethodPost, "/admin/workflows/INT-browser/resume", cookie, url.Values{"_csrf": {csrf}}, map[string]string{"Accept": "application/json"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("valid CSRF resume: %d %s", w.Code, w.Body.String())
	}
}

func TestBrowserPreservesAPIAuthentication(t *testing.T) {
	e := newBrowserEnv(t)
	cookie := loginBrowser(t, e)
	for _, tc := range []struct {
		name    string
		headers map[string]string
		cookie  *http.Cookie
		want    int
	}{
		{"missing API token", map[string]string{"Accept": "application/json"}, nil, http.StatusUnauthorized},
		{"bearer", map[string]string{"Authorization": "Bearer " + adminToken}, nil, http.StatusOK},
		{"admin header", map[string]string{"X-Admin-Token": adminToken}, nil, http.StatusOK},
		{"wrong bearer overrides cookie", map[string]string{"Authorization": "Bearer wrong"}, cookie, http.StatusUnauthorized},
		{"wrong header overrides cookie", map[string]string{"X-Admin-Token": "wrong"}, cookie, http.StatusUnauthorized},
		{"browser cookie", nil, cookie, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := browserRequest(e.router, http.MethodGet, "/admin/logs/search", tc.cookie, nil, tc.headers)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
	// API actions continue to work without browser CSRF fields.
	w := browserRequest(e.router, http.MethodPost, "/admin/workflows/INT-browser/resume", nil, nil, map[string]string{"Authorization": "Bearer " + adminToken, "Accept": "application/json"})
	if w.Code != http.StatusAccepted {
		t.Fatalf("API resume: %d", w.Code)
	}
}

func TestBrowserLoginRateLimitExpires(t *testing.T) {
	e := newBrowserEnv(t)
	page := browserRequest(e.router, http.MethodGet, "/admin/login", nil, nil, nil)
	cookie := cookieFrom(t, page)
	form := url.Values{"token": {"wrong"}, "_csrf": {csrfFrom(t, page)}}
	for i := 0; i < 10; i++ {
		if w := browserRequest(e.router, http.MethodPost, "/admin/login", cookie, form, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	w := browserRequest(e.newRouter(adminToken), http.MethodPost, "/admin/login", cookie, form, nil)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit not shared: %d", w.Code)
	}
	e.redis.FastForward(5*time.Minute + time.Second)
	if w := browserRequest(e.router, http.MethodPost, "/admin/login", cookie, form, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("rate limit did not expire: %d", w.Code)
	}
}

func TestBrowserFailsClosedWithoutRedisOrAdminToken(t *testing.T) {
	e := newBrowserEnv(t)
	cookie := loginBrowser(t, e)
	if w := browserRequest(e.newRouter(""), http.MethodGet, "/admin/login", nil, nil, nil); w.Code != http.StatusNotFound {
		t.Fatalf("empty admin token must disable login: %d", w.Code)
	}
	_ = e.client.Close()
	for _, path := range []string{"/admin/login", "/admin"} {
		w := browserRequest(e.router, http.MethodGet, path, cookie, nil, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s must fail closed without Redis: %d", path, w.Code)
		}
	}
	// Header-authenticated investigation remains available during a session-store outage.
	w := browserRequest(e.router, http.MethodGet, "/admin/logs/search", nil, nil, map[string]string{"Authorization": "Bearer " + adminToken})
	if w.Code != http.StatusOK {
		t.Fatalf("API authentication unexpectedly depends on Redis: %d", w.Code)
	}
}
