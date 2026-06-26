package export

import (
	"encoding/csv"
	"io"
)

// CSVMIME is the content type for a CSV download.
const CSVMIME = "text/csv; charset=utf-8"

// CSVRow is one contact's exportable structured fields.
type CSVRow struct {
	FullName string
	Org      string
	Email    string
	Phone    string
}

var csvHeader = []string{"Full Name", "Organization", "Email", "Phone"}

// WriteCSV writes rows as CSV with a header. Every field is run through the CSV-injection sanitizer; encoding/csv handles quoting of commas, quotes, and newlines.
func WriteCSV(w io.Writer, rows []CSVRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, r := range rows {
		record := []string{
			sanitizeCSVField(r.FullName),
			sanitizeCSVField(r.Org),
			sanitizeCSVField(r.Email),
			sanitizeCSVField(r.Phone),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// sanitizeCSVField defuses CSV/formula injection: a field starting with a character a spreadsheet treats as a formula (or a leading tab/CR that can shift the effective first character) is prefixed with a single quote so it is imported as literal text.
func sanitizeCSVField(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	default:
		return s
	}
}
