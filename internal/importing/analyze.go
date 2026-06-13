package importing

import "strings"

// Classified is a record plus whether it duplicates an existing or
// earlier-in-batch contact.
type Classified struct {
	Record    Record
	Duplicate bool
}

// Summary counts the outcome of an import analysis.
type Summary struct {
	New       int
	Duplicate int
}

// Analyze classifies records against the existing book keys, also treating
// earlier records in the same batch as "existing" so intra-batch duplicates are
// flagged. Match is by UID first, then by lowercased email.
func Analyze(records []Record, existingUIDs, existingEmails map[string]bool) ([]Classified, Summary) {
	seenUID := make(map[string]bool)
	seenEmail := make(map[string]bool)

	out := make([]Classified, 0, len(records))
	var summary Summary
	for _, r := range records {
		email := strings.ToLower(strings.TrimSpace(r.Email))
		duplicate := false
		if r.UID != "" && (existingUIDs[r.UID] || seenUID[r.UID]) {
			duplicate = true
		}
		if !duplicate && email != "" && (existingEmails[email] || seenEmail[email]) {
			duplicate = true
		}

		if duplicate {
			summary.Duplicate++
		} else {
			summary.New++
		}
		if r.UID != "" {
			seenUID[r.UID] = true
		}
		if email != "" {
			seenEmail[email] = true
		}
		out = append(out, Classified{Record: r, Duplicate: duplicate})
	}
	return out, summary
}
