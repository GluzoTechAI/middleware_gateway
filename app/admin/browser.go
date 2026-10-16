package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/gluzo/integration-gateway/app/auth"
)

const (
	adminSessionCookie = "__Host-gluzo_admin_session"
	adminSessionTTL    = 8 * time.Hour
	adminLoginTTL      = 10 * time.Minute
	adminLoginWindow   = 5 * time.Minute
	adminLoginLimit    = 10
	adminCSRFKey       = "admin.csrf"
	adminSessionIDKey  = "admin.session_id"
)

// BrowserAuth adds browser sessions to the existing header-authenticated admin
// routes. Only opaque random IDs leave the server; the configured admin token
// is never stored in a cookie, HTML, or Redis.
type BrowserAuth struct {
	handler *Handler
	redis   redis.UniversalClient
	token   string
}

type browserSession struct {
	CredentialHash string `json:"credential_hash,omitempty"`
	CSRF           string `json:"csrf"`
}

func NewBrowserAuth(h *Handler, client redis.UniversalClient, token string) *BrowserAuth {
	return &BrowserAuth{handler: h, redis: client, token: strings.TrimSpace(token)}
}

// Register exposes login without authentication, then protects every other
// admin route with either an existing API header or a browser session.
func (b *BrowserAuth) Register(g *gin.RouterGroup) {
	g.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	})
	g.GET("/login", b.loginPage)
	g.POST("/login", b.login)
	protected := g.Group("", b.requireSession())
	b.handler.Register(protected)
	protected.POST("/logout", b.logout)
}

func (b *BrowserAuth) requireSession() gin.HandlerFunc {
	headerAuth := auth.RequireAdminToken(b.token, b.handler.logger)
	return func(c *gin.Context) {
		// Explicit API credentials take precedence, including invalid ones.
		// Cookie authentication must not rescue a bad Authorization header.
		if c.GetHeader("Authorization") != "" || c.GetHeader(auth.AdminTokenHeader) != "" {
			headerAuth(c)
			return
		}
		id, session, err := b.session(c)
		if err != nil {
			b.unavailable(c)
			return
		}
		if !b.authenticated(session) {
			if strings.Contains(c.GetHeader("Accept"), "text/html") || c.Request.URL.Path == "/admin" || c.Request.URL.Path == "/admin/" {
				c.Redirect(http.StatusSeeOther, "/admin/login")
				c.Abort()
				return
			}
			headerAuth(c)
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead && c.Request.Method != http.MethodOptions {
			if !validCSRF(c, session) {
				b.handler.fail(c, http.StatusForbidden, "invalid or missing CSRF token")
				return
			}
		}
		c.Set(adminCSRFKey, session.CSRF)
		c.Set(adminSessionIDKey, id)
		c.Next()
	}
}

func (b *BrowserAuth) loginPage(c *gin.Context) {
	_, session, err := b.session(c)
	if err != nil {
		b.unavailable(c)
		return
	}
	if b.authenticated(session) {
		c.Redirect(http.StatusSeeOther, "/admin")
		return
	}
	if session == nil {
		id, fresh, err := b.createSession(c, false)
		if err != nil {
			b.unavailable(c)
			return
		}
		b.setCookie(c, id, adminLoginTTL)
		session = fresh
	}
	b.renderLogin(c, http.StatusOK, session.CSRF, "")
}

func (b *BrowserAuth) login(c *gin.Context) {
	allowed, err := b.allowLogin(c)
	if err != nil {
		b.unavailable(c)
		return
	}
	if !allowed {
		c.Header("Retry-After", "300")
		b.handler.fail(c, http.StatusTooManyRequests, "too many login attempts; try again in five minutes")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.Request.ParseForm(); err != nil {
		b.handler.fail(c, http.StatusBadRequest, "invalid login form")
		return
	}
	id, session, err := b.session(c)
	if err != nil {
		b.unavailable(c)
		return
	}
	if session == nil || !validCSRF(c, session) {
		b.renderLogin(c, http.StatusForbidden, "", "Your login form expired. Reload the sign-in page and try again.")
		return
	}
	if b.token == "" || !equalSecret(strings.TrimSpace(c.PostForm("token")), b.token) {
		b.handler.logger.WarnContext(c.Request.Context(), "admin login failed", "client_ip", c.ClientIP())
		b.renderLogin(c, http.StatusUnauthorized, session.CSRF, "Invalid admin token.")
		return
	}
	newID, _, err := b.createSession(c, true)
	if err != nil {
		b.unavailable(c)
		return
	}
	// Rotate the anonymous session on authentication, preventing fixation.
	if err := b.deleteSession(c, id); err != nil {
		b.unavailable(c)
		return
	}
	b.setCookie(c, newID, adminSessionTTL)
	b.handler.logger.InfoContext(c.Request.Context(), "admin login succeeded", "client_ip", c.ClientIP())
	c.Redirect(http.StatusSeeOther, "/admin")
}

func (b *BrowserAuth) logout(c *gin.Context) {
	if id, err := c.Cookie(adminSessionCookie); err == nil && id != "" {
		if err := b.deleteSession(c, id); err != nil {
			b.unavailable(c)
			return
		}
	}
	b.setCookie(c, "", -time.Second)
	b.handler.logger.InfoContext(c.Request.Context(), "admin logout", "client_ip", c.ClientIP())
	c.Redirect(http.StatusSeeOther, "/admin/login")
}

func (b *BrowserAuth) authenticated(session *browserSession) bool {
	return session != nil && b.token != "" && equalSecret(session.CredentialHash, auth.HashSecret(b.token))
}

func (b *BrowserAuth) session(c *gin.Context) (string, *browserSession, error) {
	id, err := c.Cookie(adminSessionCookie)
	if err != nil || len(id) != 43 {
		return "", nil, nil
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	value, err := b.redis.Get(ctx, sessionKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var session browserSession
	if err := json.Unmarshal(value, &session); err != nil {
		return "", nil, err
	}
	if session.CSRF == "" {
		return "", nil, nil
	}
	// Credential rotation invalidates existing authenticated sessions.
	if session.CredentialHash != "" && !b.authenticated(&session) {
		return "", nil, nil
	}
	return id, &session, nil
}

func (b *BrowserAuth) createSession(c *gin.Context, authenticated bool) (string, *browserSession, error) {
	id, err := auth.GenerateSecret("")
	if err != nil {
		return "", nil, err
	}
	csrf, err := auth.GenerateSecret("")
	if err != nil {
		return "", nil, err
	}
	session := &browserSession{CSRF: csrf}
	ttl := adminLoginTTL
	if authenticated {
		session.CredentialHash = auth.HashSecret(b.token)
		ttl = adminSessionTTL
	}
	value, err := json.Marshal(session)
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if err := b.redis.Set(ctx, sessionKey(id), value, ttl).Err(); err != nil {
		return "", nil, err
	}
	return id, session, nil
}

func (b *BrowserAuth) deleteSession(c *gin.Context, id string) error {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	return b.redis.Del(ctx, sessionKey(id)).Err()
}

func (b *BrowserAuth) allowLogin(c *gin.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	// Atomic expiry avoids persistent lockouts if the process stops between
	// incrementing the counter and applying its TTL. ClientIP ignores forwarded
	// headers unless the router explicitly trusts the deployment's proxy.
	count, err := b.redis.Eval(ctx, `
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], ARGV[1]) end
return n`, []string{"gluzo:admin:login:" + auth.HashSecret(c.ClientIP())}, int(adminLoginWindow.Seconds())).Int64()
	return count <= adminLoginLimit, err
}

func (b *BrowserAuth) setCookie(c *gin.Context, id string, ttl time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: adminSessionCookie, Value: id, Path: "/", Secure: true,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(ttl.Seconds()), Expires: time.Now().Add(ttl).UTC(),
	})
}

func (b *BrowserAuth) renderLogin(c *gin.Context, status int, csrf, message string) {
	b.handler.renderStatus(c, status, "login.html", gin.H{"LoginCSRF": csrf, "Problem": message})
}

func (b *BrowserAuth) unavailable(c *gin.Context) {
	b.handler.logger.ErrorContext(c.Request.Context(), "admin session store unavailable")
	b.handler.fail(c, http.StatusServiceUnavailable, "admin login is temporarily unavailable")
}

func sessionKey(id string) string { return "gluzo:admin:session:" + auth.HashSecret(id) }

func equalSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(auth.HashSecret(a)), []byte(auth.HashSecret(b))) == 1
}

func validCSRF(c *gin.Context, session *browserSession) bool {
	presented := c.GetHeader("X-CSRF-Token")
	if presented == "" {
		presented = c.PostForm("_csrf")
	}
	return presented != "" && equalSecret(presented, session.CSRF)
}
