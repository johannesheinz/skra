package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
)

func TestIssueAndVerifyCSRF(t *testing.T) {
	rec := httptest.NewRecorder()
	token, err := auth.IssueCSRF(rec, true)
	if err != nil {
		t.Fatalf("IssueCSRF: %v", err)
	}

	cookies := rec.Result().Cookies()
	var csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.CSRFCookieName {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatal("no CSRF cookie set")
	}
	if csrfCookie.Value != token {
		t.Errorf("cookie value %q != returned token %q", csrfCookie.Value, token)
	}
	if !csrfCookie.Secure || !csrfCookie.HttpOnly {
		t.Error("CSRF cookie should be Secure and HttpOnly")
	}

	// A POST carrying both the cookie and the matching form field passes.
	form := url.Values{auth.CSRFFormField: {token}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	if err := auth.VerifyCSRF(req); err != nil {
		t.Errorf("VerifyCSRF on matching token: %v", err)
	}
}

func TestVerifyCSRFRejectsMismatchAndMissing(t *testing.T) {
	token := "the-real-token"

	cases := []struct {
		name       string
		withCookie bool
		formValue  string
	}{
		{name: "mismatched", withCookie: true, formValue: "wrong"},
		{name: "missing form field", withCookie: true, formValue: ""},
		{name: "missing cookie", withCookie: false, formValue: token},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			if tc.formValue != "" {
				form.Set(auth.CSRFFormField, tc.formValue)
			}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.withCookie {
				req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
			}
			if err := auth.VerifyCSRF(req); !errors.Is(err, auth.ErrCSRF) {
				t.Errorf("VerifyCSRF = %v, want ErrCSRF", err)
			}
		})
	}
}
