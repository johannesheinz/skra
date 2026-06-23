// Package i18n provides Skrá's localization: a small registry of supported
// locales (language × region), message catalogs keyed by language, and locale
// -aware formatting of numbers, dates, and addresses.
//
// A locale is a BCP-47 tag such as en-US, de-DE, or en-DK. The language subtag
// selects the message catalog (en-US and en-DK share the English catalog); the
// full tag drives number/date/address formatting (so en-DK renders English text
// with European formats). Contact data is user content and is never translated.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ctxKey is the private context key for the request's resolved locale.
type ctxKey struct{}

// WithLocale returns a context carrying the resolved locale for the request.
func WithLocale(ctx context.Context, loc Locale) context.Context {
	return context.WithValue(ctx, ctxKey{}, loc)
}

// FromContext returns the request's locale, or the default when none is set.
func FromContext(ctx context.Context) Locale {
	if loc, ok := ctx.Value(ctxKey{}).(Locale); ok {
		return loc
	}
	return Default()
}

//go:embed locales/*.json
var catalogFS embed.FS

// Locale is one selectable language×region combination.
type Locale struct {
	Code             string // BCP-47 tag stored in preferences, e.g. "en-DK"
	Name             string // display name for the selector
	Tag              language.Tag
	lang             string // catalog key: "en" | "de"
	dateLayout       string // Go reference layout for a full date
	monthDayLayout   string // Go reference layout for a month+day (no year)
	postalBeforeCity bool   // address line order
	dir              string // "ltr" | "rtl"
}

// Dir returns the text direction ("ltr"/"rtl") for the <html dir> attribute.
func (l Locale) Dir() string { return l.dir }

// Lang returns the BCP-47 code for the <html lang> attribute.
func (l Locale) Lang() string { return l.Code }

// locales is the ordered registry; the first entry is the default.
var locales = []Locale{
	{Code: "en-US", Name: "English (US)", Tag: language.MustParse("en-US"), lang: "en",
		dateLayout: "Jan 2, 2006", monthDayLayout: "Jan 2", postalBeforeCity: false, dir: "ltr"},
	{Code: "de-DE", Name: "Deutsch", Tag: language.MustParse("de-DE"), lang: "de",
		dateLayout: "02.01.2006", monthDayLayout: "02.01.", postalBeforeCity: true, dir: "ltr"},
	{Code: "en-DK", Name: "English (EU)", Tag: language.MustParse("en-DK"), lang: "en",
		dateLayout: "2 Jan 2006", monthDayLayout: "2 Jan", postalBeforeCity: true, dir: "ltr"},
}

// Locales returns the registry (default first) for building a selector.
func Locales() []Locale { return locales }

// Default is the fallback locale (en-US).
func Default() Locale { return locales[0] }

// matcher matches an Accept-Language header against the registry.
var matcher = language.NewMatcher(func() []language.Tag {
	tags := make([]language.Tag, len(locales))
	for i, l := range locales {
		tags[i] = l.Tag
	}
	return tags
}())

// ByCode resolves a stored locale code; ok is false for an unknown code.
func ByCode(code string) (Locale, bool) {
	for _, l := range locales {
		if l.Code == code {
			return l, true
		}
	}
	return Locale{}, false
}

// Match picks the best locale for an Accept-Language header, falling back to the
// default when nothing matches.
func Match(acceptLanguage string) Locale {
	tag, _ := language.MatchStrings(matcher, acceptLanguage)
	base, _ := tag.Base()
	// MatchStrings returns one of the registry tags; find it by exact code, else
	// by language base, else default.
	for _, l := range locales {
		if l.Tag == tag {
			return l
		}
	}
	for _, l := range locales {
		if lb, _ := l.Tag.Base(); lb == base {
			return l
		}
	}
	return Default()
}

// entry is one catalog message: either a single string or plural forms keyed by
// CLDR category ("one", "other", ...).
type entry struct {
	single string
	forms  map[string]string
}

// catalogs holds each language's messages, loaded once at init.
var catalogs = mustLoadCatalogs()

func mustLoadCatalogs() map[string]map[string]entry {
	out := map[string]map[string]entry{}
	dir, err := catalogFS.ReadDir("locales")
	if err != nil {
		panic("i18n: read locales: " + err.Error())
	}
	for _, f := range dir {
		lang := strings.TrimSuffix(f.Name(), ".json")
		raw, err := catalogFS.ReadFile("locales/" + f.Name())
		if err != nil {
			panic("i18n: read " + f.Name() + ": " + err.Error())
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			panic("i18n: parse " + f.Name() + ": " + err.Error())
		}
		cat := make(map[string]entry, len(doc))
		for key, rm := range doc {
			trimmed := strings.TrimSpace(string(rm))
			if strings.HasPrefix(trimmed, "{") {
				var forms map[string]string
				if err := json.Unmarshal(rm, &forms); err != nil {
					panic("i18n: parse plural " + key + " in " + f.Name() + ": " + err.Error())
				}
				cat[key] = entry{forms: forms}
			} else {
				var s string
				if err := json.Unmarshal(rm, &s); err != nil {
					panic("i18n: parse " + key + " in " + f.Name() + ": " + err.Error())
				}
				cat[key] = entry{single: s}
			}
		}
		out[lang] = cat
	}
	if _, ok := out["en"]; !ok {
		panic("i18n: missing base English catalog (locales/en.json)")
	}
	return out
}

// Translator renders messages and formats values for one locale.
type Translator struct {
	Locale  Locale
	printer *message.Printer
}

// For returns a translator for the given locale.
func For(loc Locale) *Translator {
	return &Translator{Locale: loc, printer: message.NewPrinter(loc.Tag)}
}

// lookup finds a key's entry in the locale's language catalog, falling back to
// English. ok is false when the key is absent everywhere.
func (t *Translator) lookup(key string) (entry, bool) {
	if e, ok := catalogs[t.Locale.lang][key]; ok {
		return e, true
	}
	if e, ok := catalogs["en"][key]; ok {
		return e, true
	}
	return entry{}, false
}

// T returns the message for key. A missing key returns the key itself so gaps
// are visible rather than silently blank.
func (t *Translator) T(key string) string {
	e, ok := t.lookup(key)
	if !ok {
		return key
	}
	return e.single
}

// Tf formats a message that contains printf verbs, using locale-aware number
// formatting for its arguments.
func (t *Translator) Tf(key string, args ...any) string {
	return t.printer.Sprintf(t.T(key), args...)
}

// Plural returns the correct plural form for n, substituting {n} with the
// locale-formatted count. The catalog entry must be a plural object.
func (t *Translator) Plural(key string, n int) string {
	e, ok := t.lookup(key)
	if !ok || e.forms == nil {
		return key
	}
	cat := pluralCategory(t.Locale.Tag, n)
	msg, ok := e.forms[cat]
	if !ok {
		msg = e.forms["other"]
	}
	return strings.ReplaceAll(msg, "{n}", t.Num(n))
}

// Num formats an integer with locale grouping (1,234 vs 1.234).
func (t *Translator) Num(n int) string {
	return t.printer.Sprintf("%d", n)
}

// Date formats a full date for the locale.
func (t *Translator) Date(tm time.Time) string {
	return tm.Format(t.Locale.dateLayout)
}

// MonthDay formats a month+day (no year), e.g. for an upcoming birthday.
func (t *Translator) MonthDay(month time.Month, day int) string {
	return time.Date(2000, month, day, 0, 0, 0, 0, time.UTC).Format(t.Locale.monthDayLayout)
}

// PostalBeforeCity reports whether the postal code precedes the city on an
// address line (European order) versus following it (US order).
func (t *Translator) PostalBeforeCity() bool { return t.Locale.postalBeforeCity }

// ISODate formats a stored YYYY-MM-DD date for the locale; a value that does not
// parse (e.g. a year-less birthday) is returned unchanged.
func (t *Translator) ISODate(s string) string {
	tm, err := time.Parse("2006-01-02", s)
	if err != nil {
		return s
	}
	return t.Date(tm)
}

// TypeLabel translates a known type tag (home/work/mobile/other), falling back
// to the raw value for anything else (e.g. a type from an imported vCard).
func (t *Translator) TypeLabel(s string) string {
	if e, ok := t.lookup("type." + s); ok && e.single != "" {
		return e.single
	}
	return s
}

// pluralCategory maps a count to its CLDR plural category for the language.
func pluralCategory(tag language.Tag, n int) string {
	switch plural.Cardinal.MatchPlural(tag, n, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

// MissingKeys returns keys present in the English base catalog but absent from
// the given language catalog — used by tests to guarantee full coverage.
func MissingKeys(lang string) []string {
	var missing []string
	for key := range catalogs["en"] {
		if _, ok := catalogs[lang][key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// assert Locale exposes a stable string for logs/debug.
func (l Locale) String() string { return fmt.Sprintf("%s(%s)", l.Code, l.lang) }
