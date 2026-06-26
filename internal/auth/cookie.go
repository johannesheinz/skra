package auth

import (
	"net/http"
)

// Cookie names. The session cookie carries the opaque session id; the CSRF cookie carries the double-submit token.
const (
	SessionCookieName = "skra_session"
	CSRFCookieName    = "skra_csrf"
)

// NewSessionCookie builds the session cookie. Secure is driven by configuration (the external scheme), not the internal connection. SameSite=Lax blunts CSRF on top of the explicit token check.
func NewSessionCookie(value string, secure bool) *http.Cookie {
	return baseCookie(SessionCookieName, value, secure, int(SessionTTL.Seconds()))
}

// ClearSessionCookie expires the session cookie (logout).
func ClearSessionCookie(secure bool) *http.Cookie {
	return baseCookie(SessionCookieName, "", secure, -1)
}

// NewCSRFCookie builds the double-submit CSRF cookie. It is short-lived and scoped like the session cookie.
func NewCSRFCookie(value string, secure bool) *http.Cookie {
	return baseCookie(CSRFCookieName, value, secure, int(SessionTTL.Seconds()))
}

func baseCookie(name, value string, secure bool, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}
