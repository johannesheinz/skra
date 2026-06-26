package auth

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/johannesheinz/skra/internal/ids"
)

// CSRFFormField is the form field carrying the double-submit token.
const CSRFFormField = "csrf_token"

// ErrCSRF is returned when the submitted token does not match the cookie.
var ErrCSRF = errors.New("auth: CSRF token mismatch")

// IssueCSRF generates a token, sets it as the CSRF cookie, and returns it so a handler can embed it in a form. Uses the double-submit pattern: no server-side storage and no signing key required.
func IssueCSRF(w http.ResponseWriter, secure bool) (string, error) {
	token, err := ids.NewCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, NewCSRFCookie(token, secure))
	return token, nil
}

// VerifyCSRF checks that the token in the form matches the one in the cookie, in constant time. The request's form must already be parsed, or this parses it.
func VerifyCSRF(r *http.Request) error {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return ErrCSRF
	}
	submitted := r.PostFormValue(CSRFFormField)
	if submitted == "" {
		return ErrCSRF
	}
	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) != 1 {
		return ErrCSRF
	}
	return nil
}
