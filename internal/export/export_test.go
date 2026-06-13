package export

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteCSVSanitizesInjection(t *testing.T) {
	var buf bytes.Buffer
	err := WriteCSV(&buf, []CSVRow{
		{FullName: "=cmd|' /C calc'!A0", Org: "+SUM(1)", Email: "-2+3", Phone: "@evil"},
		{FullName: "Jane Doe", Org: "Acme", Email: "jane@acme.test", Phone: "+1 555"},
	})
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	out := buf.String()

	// Dangerous leading characters must be quote-prefixed.
	for _, want := range []string{`'=cmd`, `'+SUM(1)`, `'-2+3`, `'@evil`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing sanitized %q:\n%s", want, out)
		}
	}
	// A normal phone that merely contains "+1 555" — its field starts with '+'
	// so it is also prefixed; ensure the benign name is untouched.
	if !strings.Contains(out, "Jane Doe") {
		t.Error("benign value altered")
	}
}

func TestWriteCSVHasHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCSV(&buf, nil); err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "Full Name,Organization,Email,Phone") {
		t.Errorf("missing/incorrect header: %q", buf.String())
	}
}

func TestSanitizeCSVField(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"plain":          "plain",
		"=danger":        "'=danger",
		"+danger":        "'+danger",
		"-danger":        "'-danger",
		"@danger":        "'@danger",
		"\tdanger":       "'\tdanger",
		"mid=safe":       "mid=safe",
		"jane@acme.test": "jane@acme.test",
	}
	for in, want := range cases {
		if got := sanitizeCSVField(in); got != want {
			t.Errorf("sanitizeCSVField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteVCardsVerbatimWithoutPhoto(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Jane Doe\r\nUID:abc\r\nEND:VCARD\r\n"
	var buf bytes.Buffer
	if err := WriteVCards(&buf, []VCardEntry{{Raw: raw}, {Raw: raw}}); err != nil {
		t.Fatalf("WriteVCards: %v", err)
	}
	out := buf.String()
	if n := strings.Count(out, "BEGIN:VCARD"); n != 2 {
		t.Errorf("expected 2 cards, got %d", n)
	}
	if !strings.Contains(out, "FN:Jane Doe") {
		t.Error("card content not preserved")
	}
}

func TestWriteVCardsInjectsPhoto(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Jane Doe\r\nUID:abc\r\nEND:VCARD\r\n"
	var buf bytes.Buffer
	err := WriteVCards(&buf, []VCardEntry{{Raw: raw, PhotoJPEG: []byte{0xFF, 0xD8, 0xFF, 0xD9}}})
	if err != nil {
		t.Fatalf("WriteVCards: %v", err)
	}
	out := buf.String()
	// The comma in the data URI is vCard-escaped as "\," on encode (and
	// unescaped on decode), so match the format-agnostic substring.
	if !strings.Contains(out, "PHOTO:data:image/jpeg;base64") {
		t.Errorf("expected embedded base64 PHOTO:\n%s", out)
	}
	if !strings.Contains(out, "FN:Jane Doe") {
		t.Error("original fields lost after photo injection")
	}
}
