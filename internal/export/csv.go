package export

import (
	"encoding/csv"
	"io"
	"strings"

	"github.com/johannesheinz/skra/internal/vcardio"
)

// CSVMIME is the content type for a CSV download.
const CSVMIME = "text/csv; charset=utf-8"

// CSVRow is one contact's exportable fields, flattened for a spreadsheet.
// Multi-value fields (emails, phones, addresses, links) are joined into a single
// cell so nothing is dropped, each entry carrying its type where it has one.
type CSVRow struct {
	FullName   string
	GivenName  string
	FamilyName string
	Org        string
	Title      string
	Birthday   string
	Emails     string
	Phones     string
	Addresses  string
	Links      string
	Note       string
}

var csvHeader = []string{
	"Full Name", "Given Name", "Family Name", "Organization", "Title",
	"Birthday", "Emails", "Phones", "Addresses", "Links", "Note",
}

// csvMultiSep joins the entries of a multi-value field within one cell.
const csvMultiSep = " | "

// CSVRowFromDetails flattens a contact's parsed record into a CSV row.
func CSVRowFromDetails(fullName string, d vcardio.Details) CSVRow {
	return CSVRow{
		FullName:   fullName,
		GivenName:  d.GivenName,
		FamilyName: d.FamilyName,
		Org:        d.Org,
		Title:      d.Title,
		Birthday:   d.Birthday,
		Emails:     joinTyped(d.Emails),
		Phones:     joinTyped(d.Phones),
		Addresses:  joinAddresses(d.Addresses),
		Links:      strings.Join(d.URLs, csvMultiSep),
		Note:       d.Note,
	}
}

// joinTyped renders typed values as "type: value" (or just "value" when untyped), joined into one cell.
func joinTyped(vals []vcardio.Typed) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.TrimSpace(v.Value) == "" {
			continue
		}
		if v.Type != "" {
			parts = append(parts, v.Type+": "+v.Value)
		} else {
			parts = append(parts, v.Value)
		}
	}
	return strings.Join(parts, csvMultiSep)
}

// joinAddresses renders each address as a single line (typed where set), joined into one cell.
func joinAddresses(addrs []vcardio.Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		line := a.SingleLine()
		if line == "" {
			continue
		}
		if a.Type != "" {
			parts = append(parts, a.Type+": "+line)
		} else {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, csvMultiSep)
}

// WriteCSV writes rows as CSV with a header.
// Every field is run through the CSV-injection sanitizer; encoding/csv handles quoting of commas, quotes, and newlines.
func WriteCSV(w io.Writer, rows []CSVRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, r := range rows {
		record := []string{
			sanitizeCSVField(r.FullName),
			sanitizeCSVField(r.GivenName),
			sanitizeCSVField(r.FamilyName),
			sanitizeCSVField(r.Org),
			sanitizeCSVField(r.Title),
			sanitizeCSVField(r.Birthday),
			sanitizeCSVField(r.Emails),
			sanitizeCSVField(r.Phones),
			sanitizeCSVField(r.Addresses),
			sanitizeCSVField(r.Links),
			sanitizeCSVField(r.Note),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// sanitizeCSVField defuses CSV/formula injection: a field starting with a character a spreadsheet treats as a formula (or a leading tab/CR that can shift the effective first character)
// is prefixed with a single quote so it is imported as literal text.
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
