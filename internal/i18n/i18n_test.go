package i18n

import "testing"

// TestGermanCatalogComplete guards translation coverage: every key in the English source catalog must exist in German.
func TestGermanCatalogComplete(t *testing.T) {
	if missing := MissingKeys("de"); len(missing) > 0 {
		t.Errorf("de.json is missing %d keys: %v", len(missing), missing)
	}
}

func TestMatch(t *testing.T) {
	cases := map[string]string{
		"de-DE":          "de-DE",
		"de":             "de-DE",
		"de-AT,de;q=0.9": "de-DE", // Austrian German → German catalog
		"en-GB":          "en-US", // English base → first English locale
		"fr-FR":          "en-US", // unsupported → default
		"":               "en-US",
		"en-DK":          "en-DK", // exact English-Denmark match
		"en-US":          "en-US",
	}
	for header, want := range cases {
		if got := Match(header).Code; got != want {
			t.Errorf("Match(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestByCode(t *testing.T) {
	if loc, ok := ByCode("en-DK"); !ok || loc.lang != "en" {
		t.Errorf("ByCode(en-DK) = %+v, %v", loc, ok)
	}
	if _, ok := ByCode("xx-YY"); ok {
		t.Error("ByCode(unknown) should be !ok")
	}
}

func TestPlural(t *testing.T) {
	en := For(mustLoc("en-US"))
	de := For(mustLoc("de-DE"))
	if got := en.Plural("contacts.count", 1); got != "1 contact" {
		t.Errorf("en plural(1) = %q", got)
	}
	if got := en.Plural("contacts.count", 5); got != "5 contacts" {
		t.Errorf("en plural(5) = %q", got)
	}
	if got := de.Plural("contacts.count", 1); got != "1 Kontakt" {
		t.Errorf("de plural(1) = %q", got)
	}
	if got := de.Plural("contacts.count", 5); got != "5 Kontakte" {
		t.Errorf("de plural(5) = %q", got)
	}
}

func TestNumberFormatting(t *testing.T) {
	cases := map[string]string{"en-US": "1,234", "de-DE": "1.234", "en-DK": "1.234"}
	for code, want := range cases {
		if got := For(mustLoc(code)).Num(1234); got != want {
			t.Errorf("Num(1234) for %s = %q, want %q", code, got, want)
		}
	}
}

func TestDateFormatting(t *testing.T) {
	cases := map[string]string{"en-US": "Jul 1, 2026", "de-DE": "01.07.2026", "en-DK": "1 Jul 2026"}
	for code, want := range cases {
		if got := For(mustLoc(code)).ISODate("2026-07-01"); got != want {
			t.Errorf("ISODate for %s = %q, want %q", code, got, want)
		}
	}
}

func TestFallbackToEnglish(t *testing.T) {
	de := For(mustLoc("de-DE"))
	// A key present only in English still resolves (falls back), never blank.
	if got := de.T("app_missing_key_xyz"); got != "app_missing_key_xyz" {
		t.Errorf("missing key should echo itself, got %q", got)
	}
}

func mustLoc(code string) Locale {
	l, ok := ByCode(code)
	if !ok {
		panic("unknown test locale " + code)
	}
	return l
}
