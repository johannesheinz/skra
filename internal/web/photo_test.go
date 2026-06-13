package web

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestContactPhotoUploadServeAndDelete(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})
	session := sessionCookieFor(t, d, owner.ID)
	contactURL := "/contacts/" + contact.PublicID

	// Grab a CSRF token from the contact page.
	_, token, csrf := authedGet(t, router, session, contactURL)

	// Build a multipart upload with a 1000x500 PNG.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(auth.CSRFFormField, token)
	fw, err := mw.CreateFormFile("photo", "avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if err := png.Encode(fw, image.NewRGBA(image.Rect(0, 0, 1000, 500))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	mw.Close()

	upReq := httptest.NewRequest(http.MethodPost, contactURL+"/photo", &body)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.AddCookie(session)
	upReq.AddCookie(csrf)
	upRec := httptest.NewRecorder()
	router.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusSeeOther {
		t.Fatalf("upload = %d, want 303", upRec.Code)
	}

	// Serve the normalized photo.
	serveReq := httptest.NewRequest(http.MethodGet, contactURL+"/photo", nil)
	serveReq.AddCookie(session)
	serveRec := httptest.NewRecorder()
	router.ServeHTTP(serveRec, serveReq)
	if serveRec.Code != http.StatusOK {
		t.Fatalf("serve photo = %d, want 200", serveRec.Code)
	}
	if ct := serveRec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("photo Content-Type = %q, want image/jpeg", ct)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(serveRec.Body.Bytes())); err != nil || format != "jpeg" {
		t.Errorf("served photo not JPEG: format %q err %v", format, err)
	}
	etag := serveRec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("served photo missing ETag")
	}

	// Conditional GET → 304.
	condReq := httptest.NewRequest(http.MethodGet, contactURL+"/photo", nil)
	condReq.AddCookie(session)
	condReq.Header.Set("If-None-Match", etag)
	condRec := httptest.NewRecorder()
	router.ServeHTTP(condRec, condReq)
	if condRec.Code != http.StatusNotModified {
		t.Errorf("conditional photo GET = %d, want 304", condRec.Code)
	}

	// Delete the photo.
	_, delToken, delCSRF := authedGet(t, router, session, contactURL)
	delRec := authedPostForm(router, session, delCSRF, contactURL+"/photo/delete",
		url.Values{auth.CSRFFormField: {delToken}})
	if delRec.Code != http.StatusSeeOther {
		t.Fatalf("delete photo = %d, want 303", delRec.Code)
	}
	goneReq := httptest.NewRequest(http.MethodGet, contactURL+"/photo", nil)
	goneReq.AddCookie(session)
	goneRec := httptest.NewRecorder()
	router.ServeHTTP(goneRec, goneReq)
	if goneRec.Code != http.StatusNotFound {
		t.Errorf("photo after delete = %d, want 404", goneRec.Code)
	}
}

func TestContactPhotoUploadRejectsNonImage(t *testing.T) {
	d := testutil.NewDB(t)
	router := testRouter(t, d)
	ctx := context.Background()

	owner := seedUser(t, d, "owner", "pw", models.RoleUser)
	book, _ := models.CreateAddressBook(ctx, d, owner.ID, "Book", "")
	contact, _ := models.CreateContact(ctx, d, book.ID, models.ContactInput{FullName: "Jane"})
	session := sessionCookieFor(t, d, owner.ID)
	contactURL := "/contacts/" + contact.PublicID

	_, token, csrf := authedGet(t, router, session, contactURL)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(auth.CSRFFormField, token)
	fw, _ := mw.CreateFormFile("photo", "notes.txt")
	_, _ = fw.Write([]byte("definitely not an image"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, contactURL+"/photo", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-image upload = %d, want 422", rec.Code)
	}
}
