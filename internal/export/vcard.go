// Package export renders address books and contacts to downloadable vCard and CSV.
// It is pure transformation (no database access) so it is easy to test; callers gather the data and pass it in.
package export

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-vcard"
)

// VCardEntry is one contact for vCard export: its canonical vcard_raw plus, optionally, the normalized JPEG photo to embed.
type VCardEntry struct {
	Raw       string
	PhotoJPEG []byte
}

// VCardMIME is the content type for a vCard download.
const VCardMIME = "text/vcard; charset=utf-8"

// WriteVCards writes the entries as a single vCard stream.
// Entries without a photo are emitted verbatim (preserving stored fidelity); entries with a photo are re-encoded to inject a base64 PHOTO so the photo is not double-stored.
func WriteVCards(w io.Writer, entries []VCardEntry) error {
	for _, e := range entries {
		if len(e.PhotoJPEG) == 0 {
			if err := writeRaw(w, e.Raw); err != nil {
				return err
			}
			continue
		}
		withPhoto, err := injectPhoto(e.Raw, e.PhotoJPEG)
		if err != nil {
			return err
		}
		if err := writeRaw(w, withPhoto); err != nil {
			return err
		}
	}
	return nil
}

func writeRaw(w io.Writer, card string) error {
	if _, err := io.WriteString(w, card); err != nil {
		return err
	}
	// Guarantee separation between concatenated cards.
	if !strings.HasSuffix(card, "\n") {
		if _, err := io.WriteString(w, "\r\n"); err != nil {
			return err
		}
	}
	return nil
}

// injectPhoto parses a single vCard, sets its PHOTO to a base64 JPEG data URI, and re-encodes it as vCard 4.0.
func injectPhoto(raw string, jpeg []byte) (string, error) {
	card, err := vcard.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		return "", fmt.Errorf("export: decode vcard: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(jpeg)
	card.SetValue(vcard.FieldPhoto, "data:image/jpeg;base64,"+b64)
	vcard.ToV4(card)

	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return "", fmt.Errorf("export: encode vcard: %w", err)
	}
	return buf.String(), nil
}
