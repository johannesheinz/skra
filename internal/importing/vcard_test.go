package importing

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const twoCards = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane Doe\r\nORG:Acme\r\nEMAIL:jane@acme.test\r\nTEL:+123\r\nUID:uid-jane\r\nEND:VCARD\r\n" +
	"BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Bob Roe\r\nEMAIL:bob@roe.test\r\nEND:VCARD\r\n"

func TestParseVCardsMultiple(t *testing.T) {
	recs, malformed := ParseVCards([]byte(twoCards))
	if malformed != 0 {
		t.Fatalf("malformed = %d, want 0", malformed)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	if recs[0].FullName != "Jane Doe" || recs[0].Org != "Acme" || recs[0].Email != "jane@acme.test" || recs[0].UID != "uid-jane" {
		t.Errorf("record 0 = %+v", recs[0])
	}
	if !strings.Contains(recs[0].CanonicalRaw, "VERSION:4.0") {
		t.Error("canonical raw should be normalized to v4")
	}
	// Bob has no UID → one is generated.
	if recs[1].UID == "" {
		t.Error("missing UID should be generated")
	}
}

func TestParseVCardsIsolatesMalformed(t *testing.T) {
	input := "garbage line\r\n" + twoCards +
		"BEGIN:VCARD\r\nVERSION:3.0\r\nEND:VCARD\r\n" // no FN/N → malformed
	recs, malformed := ParseVCards([]byte(input))
	if len(recs) != 2 {
		t.Errorf("records = %d, want 2 (good cards kept)", len(recs))
	}
	if malformed != 1 {
		t.Errorf("malformed = %d, want 1", malformed)
	}
}

func TestParseVCardsStripsBOMAndPhoto(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	b64 := base64.StdEncoding.EncodeToString(jpeg)
	card := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Pic Person\r\nPHOTO:data:image/jpeg;base64," + b64 + "\r\nUID:p1\r\nEND:VCARD\r\n"
	input := append([]byte{0xEF, 0xBB, 0xBF}, []byte(card)...)

	recs, malformed := ParseVCards(input)
	if malformed != 0 || len(recs) != 1 {
		t.Fatalf("got %d records, %d malformed", len(recs), malformed)
	}
	if !bytes.Equal(recs[0].PhotoData, jpeg) {
		t.Errorf("photo not extracted: %v", recs[0].PhotoData)
	}
	if strings.Contains(recs[0].CanonicalRaw, "PHOTO") {
		t.Error("PHOTO should be stripped from canonical raw")
	}
}

func TestAnalyzeDedup(t *testing.T) {
	recs := []Record{
		{FullName: "A", UID: "u1", Email: "a@x.test"},
		{FullName: "B", UID: "u2", Email: "b@x.test"}, // new
		{FullName: "A2", UID: "u1"},                   // dup by UID (existing)
		{FullName: "C", Email: "b@x.test"},            // dup by email (intra-batch)
	}
	existingUIDs := map[string]bool{"u1": true}
	existingEmails := map[string]bool{}

	classified, summary := Analyze(recs, existingUIDs, existingEmails)
	if summary.New != 1 || summary.Duplicate != 3 {
		t.Errorf("summary = %+v, want New 1 Duplicate 3", summary)
	}
	if classified[1].Duplicate {
		t.Error("record B should be new")
	}
	if !classified[0].Duplicate || !classified[2].Duplicate || !classified[3].Duplicate {
		t.Error("expected records 0,2,3 to be duplicates")
	}
}
