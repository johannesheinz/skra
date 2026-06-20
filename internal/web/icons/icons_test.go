package icons

import (
	"strings"
	"testing"
)

func TestInlineReturnsSVG(t *testing.T) {
	got := string(Inline("pencil"))
	if !strings.Contains(got, "<svg") || !strings.Contains(got, "lucide-pencil") {
		t.Errorf("Inline(pencil) = %q, want lucide svg markup", got)
	}
	if strings.Contains(got, "@license") {
		t.Error("license comment should be stripped from inline output")
	}
	if !strings.Contains(got, "currentColor") {
		t.Error("icon should use currentColor")
	}
}

func TestInlinePanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Inline on unknown icon did not panic")
		}
	}()
	Inline("definitely-not-an-icon")
}

func TestAllVendoredIconsLoad(t *testing.T) {
	// Every icon referenced by templates must exist; spot-check the set loads.
	for _, name := range []string{"pencil", "trash-2", "plus", "search", "download", "upload", "users", "book-open", "log-out", "arrow-left"} {
		if got := string(Inline(name)); !strings.Contains(got, "<svg") {
			t.Errorf("icon %q did not load", name)
		}
	}
}
