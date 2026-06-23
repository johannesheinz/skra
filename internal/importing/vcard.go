// Package importing parses contact interchange files (currently vCard) into
// normalized records and classifies them for de-duplication. It does no
// database or image work; callers persist the results.
package importing

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/emersion/go-vcard"

	"github.com/johannesheinz/skra/internal/ids"
)

// Record is one normalized contact parsed from an import. CanonicalRaw is the
// re-encoded vCard 4.0 (with PHOTO stripped, since photos are stored
// separately); PhotoData holds the decoded photo bytes before the image
// pipeline, or nil.
type Record struct {
	FullName     string
	Org          string
	Email        string
	Phone        string
	Birthday     string
	UID          string
	CanonicalRaw string
	PhotoData    []byte
}

// ParseVCards parses (possibly many concatenated) vCards, isolating per-card
// failures: a malformed card increments the malformed count rather than
// aborting the batch. Handles a leading UTF-8 BOM.
func ParseVCards(data []byte) (records []Record, malformed int) {
	for _, block := range splitCards(stripBOM(data)) {
		rec, err := parseCard(block)
		if err != nil {
			malformed++
			continue
		}
		records = append(records, rec)
	}
	return records, malformed
}

func parseCard(block string) (Record, error) {
	card, err := vcard.NewDecoder(strings.NewReader(block)).Decode()
	if err != nil {
		return Record{}, err
	}

	fullName := strings.TrimSpace(card.PreferredValue(vcard.FieldFormattedName))
	if fullName == "" {
		if n := card.Name(); n != nil {
			fullName = strings.TrimSpace(n.GivenName + " " + n.FamilyName)
		}
	}
	if fullName == "" {
		return Record{}, fmt.Errorf("importing: card has no name")
	}

	photo := extractPhoto(card)

	uid := strings.TrimSpace(card.Value(vcard.FieldUID))
	rec := Record{
		FullName:  fullName,
		Org:       strings.TrimSpace(card.PreferredValue(vcard.FieldOrganization)),
		Email:     strings.TrimSpace(card.PreferredValue(vcard.FieldEmail)),
		Phone:     strings.TrimSpace(card.PreferredValue(vcard.FieldTelephone)),
		Birthday:  strings.TrimSpace(card.Value(vcard.FieldBirthday)),
		UID:       uid,
		PhotoData: photo,
	}

	// Photos are stored separately, so strip PHOTO from the canonical record.
	delete(card, vcard.FieldPhoto)
	if rec.UID == "" {
		generated, err := ids.Random(16)
		if err != nil {
			return Record{}, err
		}
		rec.UID = generated
	}
	card.SetValue(vcard.FieldUID, rec.UID)
	vcard.ToV4(card)

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return Record{}, err
	}
	rec.CanonicalRaw = buf.String()
	return rec, nil
}

// BuildCanonicalRaw produces a vCard 4.0 string from structured fields, used
// when a uid must be re-minted (create-new on collision).
func BuildCanonicalRaw(fullName, org, email, phone, uid string) string {
	card := vcard.Card{}
	card.SetValue(vcard.FieldFormattedName, fullName)
	if org != "" {
		card.SetValue(vcard.FieldOrganization, org)
	}
	if email != "" {
		card.SetValue(vcard.FieldEmail, email)
	}
	if phone != "" {
		card.SetValue(vcard.FieldTelephone, phone)
	}
	card.SetValue(vcard.FieldUID, uid)
	vcard.ToV4(card)
	var buf bytes.Buffer
	_ = vcard.NewEncoder(&buf).Encode(card)
	return buf.String()
}

// extractPhoto returns decoded photo bytes from a card's PHOTO, handling both
// the vCard 4.0 data: URI form and the 3.0 base64-encoded form. Remote URI
// photos are skipped (we never fetch external resources).
func extractPhoto(card vcard.Card) []byte {
	f := card.Get(vcard.FieldPhoto)
	if f == nil || f.Value == "" {
		return nil
	}
	if strings.HasPrefix(f.Value, "data:") {
		comma := strings.IndexByte(f.Value, ',')
		if comma < 0 || !strings.Contains(f.Value[:comma], "base64") {
			return nil
		}
		if b, err := base64.StdEncoding.DecodeString(f.Value[comma+1:]); err == nil {
			return b
		}
		return nil
	}
	if enc := strings.ToLower(f.Params.Get("ENCODING")); enc == "b" || enc == "base64" {
		clean := strings.Join(strings.Fields(f.Value), "")
		if b, err := base64.StdEncoding.DecodeString(clean); err == nil {
			return b
		}
	}
	return nil
}

func stripBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
}

// splitCards extracts each BEGIN:VCARD…END:VCARD block as its own string so the
// blocks can be decoded independently for per-card error isolation.
func splitCards(data []byte) []string {
	var blocks []string
	var current []string
	inCard := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "BEGIN:VCARD":
			inCard = true
			current = []string{line}
		case "END:VCARD":
			if inCard {
				current = append(current, line)
				blocks = append(blocks, strings.Join(current, "\r\n"))
				inCard = false
				current = nil
			}
		default:
			if inCard {
				current = append(current, line)
			}
		}
	}
	return blocks
}
