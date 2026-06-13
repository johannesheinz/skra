// Command seed populates a Skrá database with demo data for development and
// demos. It uses the real model write path, so contacts get proper vcard_raw,
// passwords are argon2id-hashed, and photos run through the ingest pipeline.
//
// Usage:
//
//	go run ./scripts/seed --db skra-demo.db --extra 150
//	SKRA_LISTEN=127.0.0.1:3000 SKRA_DB_PATH=skra-demo.db \
//	  SKRA_COOKIE_SECURE=false SKRA_EXTERNAL_URL=http://127.0.0.1:3000 \
//	  SKRA_SESSION_KEY=dev-only-session-key-not-secret-000 ./skra serve
//
// NEVER run this against production data.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"

	"github.com/johannesheinz/skra/internal/auth"
	"github.com/johannesheinz/skra/internal/db"
	"github.com/johannesheinz/skra/internal/images"
	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/vcardio"
)

const demoPassword = "demo-password-123"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := flag.String("db", "skra-demo.db", "SQLite database path to seed")
	extra := flag.Int("extra", 150, "number of generated contacts to add across the books")
	force := flag.Bool("force", false, "seed even if the database already has users")
	flag.Parse()

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	ctx := context.Background()

	count, err := models.CountUsers(ctx, database)
	if err != nil {
		return err
	}
	if count > 0 && !*force {
		return fmt.Errorf("database already has %d user(s); pass --force to seed anyway", count)
	}

	// Users.
	users := map[string]models.User{}
	for _, u := range []struct{ name, role string }{
		{"admin", models.RoleAdmin},
		{"alice", models.RoleUser},
		{"bob", models.RoleUser},
		{"carol", models.RoleUser},
		{"dave", models.RoleUser},
	} {
		created, err := createUser(ctx, database, u.name, u.name+"@demo.test", u.role)
		if err != nil {
			return err
		}
		users[u.name] = created
	}

	// Books with cross-user grants (owner already gets a manager grant).
	type bookSpec struct {
		name, owner, desc string
		grants            map[string]string // username -> level
	}
	specs := []bookSpec{
		{"Work", "admin", "Colleagues and clients", map[string]string{"alice": models.AccessManager, "bob": models.AccessViewer}},
		{"Friends", "alice", "People I actually like", nil},
		{"Family", "bob", "Relatives", map[string]string{"carol": models.AccessViewer}},
		{"Clients", "admin", "External contacts", map[string]string{"dave": models.AccessManager}},
	}
	var books []models.AddressBook
	for _, s := range specs {
		book, err := models.CreateAddressBook(ctx, database, users[s.owner].ID, s.name, s.desc)
		if err != nil {
			return err
		}
		for username, level := range s.grants {
			if err := models.AddOrUpdateMember(ctx, database, book.ID, users[username].ID, level, users[s.owner].ID); err != nil {
				return err
			}
		}
		books = append(books, book)
	}

	// Curated, recognizable contacts go in the first book for flavor.
	if err := seedContacts(ctx, database, books[0].ID, curatedContacts); err != nil {
		return err
	}

	// Generated contacts spread round-robin across all books.
	for i := 0; i < *extra; i++ {
		book := books[i%len(books)]
		if err := seedContacts(ctx, database, book.ID, []demoContact{generateContact(i)}); err != nil {
			return err
		}
	}

	total := len(curatedContacts) + *extra
	fmt.Printf(`seeded %s

  users:    admin, alice, bob, carol, dave   (password: %s)
  books:    %d (Work, Friends, Family, Clients) with cross-user grants
  contacts: %d total (%d curated + %d generated)

run it:
  SKRA_LISTEN=127.0.0.1:3000 SKRA_DB_PATH=%s \
    SKRA_COOKIE_SECURE=false SKRA_EXTERNAL_URL=http://127.0.0.1:3000 \
    SKRA_SESSION_KEY=dev-only-session-key-not-secret-000 ./skra serve
`, *dbPath, demoPassword, len(books), total, len(curatedContacts), *extra, *dbPath)
	return nil
}

func createUser(ctx context.Context, d *db.DB, username, email, role string) (models.User, error) {
	hash, err := auth.HashPassword(demoPassword)
	if err != nil {
		return models.User{}, err
	}
	return models.CreateUser(ctx, d, username, email, hash, role)
}

type demoContact struct {
	in    models.ContactInput
	color *color.RGBA // optional avatar color
}

func seedContacts(ctx context.Context, d *db.DB, bookID int64, contacts []demoContact) error {
	for _, c := range contacts {
		contact, err := models.CreateContact(ctx, d, bookID, c.in)
		if err != nil {
			return fmt.Errorf("create contact %q: %w", c.in.GivenName, err)
		}
		if c.color != nil {
			jpeg, err := images.Process(avatarPNG(*c.color))
			if err != nil {
				return fmt.Errorf("process avatar: %w", err)
			}
			if err := models.SetContactPhoto(ctx, d, contact.ID, jpeg); err != nil {
				return fmt.Errorf("set photo: %w", err)
			}
		}
	}
	return nil
}

// generateContact deterministically builds a varied rich contact from an index,
// so emails are unique and runs are reproducible.
func generateContact(i int) demoContact {
	first := firstNames[i%len(firstNames)]
	last := lastNames[(i*7)%len(lastNames)]
	slug := strings.ToLower(first) + "." + strings.ToLower(last) + fmt.Sprint(i+1)

	in := models.ContactInput{
		GivenName:  first,
		FamilyName: last,
		Emails:     []vcardio.Typed{{Type: "home", Value: slug + "@demo.test"}},
		Phones:     []vcardio.Typed{{Type: "mobile", Value: fmt.Sprintf("+1 555 %04d", i+1)}},
	}
	if i%2 == 0 {
		in.Org = orgs[i%len(orgs)]
		in.Title = titles[i%len(titles)]
		in.Emails = append(in.Emails, vcardio.Typed{Type: "work", Value: strings.ToLower(first) + "@" + strings.ToLower(strings.ReplaceAll(in.Org, " ", "")) + ".demo"})
	}
	if i%3 == 0 {
		in.Phones = append(in.Phones, vcardio.Typed{Type: "work", Value: fmt.Sprintf("+1 555 %04d", 9000+i)})
	}
	if i%4 == 0 {
		city := cities[i%len(cities)]
		in.Addresses = []vcardio.Address{{Type: "home", Street: fmt.Sprintf("%d %s St", 10+i, last), City: city, Region: regions[i%len(regions)], PostalCode: fmt.Sprintf("%05d", 10000+i*7%89999), Country: "USA"}}
	}
	if i%5 == 0 {
		in.Birthday = fmt.Sprintf("19%02d-%02d-%02d", 60+i%39, 1+i%12, 1+i%28)
	}
	if i%6 == 0 {
		in.Note = "Met via " + orgs[(i+3)%len(orgs)] + "."
	}
	if i%9 == 0 {
		in.URLs = []string{"https://demo.test/" + slug}
	}

	var col *color.RGBA
	if i%3 == 0 {
		c := palette[i%len(palette)]
		col = &c
	}
	return demoContact{in: in, color: col}
}

// avatarPNG returns a solid-color square PNG to stand in for a contact photo.
func avatarPNG(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func email(t, v string) vcardio.Typed { return vcardio.Typed{Type: t, Value: v} }
func phone(t, v string) vcardio.Typed { return vcardio.Typed{Type: t, Value: v} }

var (
	firstNames = []string{"Grace", "Alan", "Ada", "Katherine", "Dennis", "Jamie", "Priya", "Tom", "Mei", "Omar", "Sofia", "Liam", "Noor", "Hiro", "Elena", "Kwame", "Ingrid", "Diego", "Yuki", "Fatima"}
	lastNames  = []string{"Hopper", "Turing", "Lovelace", "Johnson", "Ritchie", "Rivera", "Nair", "Okafor", "Lin", "Haddad", "Costa", "Murphy", "Khan", "Sato", "Petrova", "Mensah", "Larsen", "Reyes", "Tanaka", "Aziz"}
	orgs       = []string{"Acme", "Globex", "Initech", "Umbrella", "Hooli", "Soylent", "Stark Industries", "Wayne Enterprises"}
	titles     = []string{"Engineer", "Designer", "Manager", "Analyst", "Director", "Consultant", "Researcher", "Coordinator"}
	cities     = []string{"Portland", "Austin", "Denver", "Seattle", "Boston", "Atlanta", "Chicago", "Madison"}
	regions    = []string{"OR", "TX", "CO", "WA", "MA", "GA", "IL", "WI"}
	palette    = []color.RGBA{{47, 93, 80, 255}, {120, 80, 160, 255}, {180, 90, 60, 255}, {60, 120, 180, 255}, {150, 120, 50, 255}, {90, 100, 110, 255}}
)

var curatedContacts = []demoContact{
	{
		in: models.ContactInput{
			GivenName: "Grace", FamilyName: "Hopper", Org: "Navy", Title: "Rear Admiral",
			Emails:    []vcardio.Typed{email("work", "grace@navy.demo"), email("home", "grace@home.demo")},
			Phones:    []vcardio.Typed{phone("work", "+1 202 555 0100")},
			Addresses: []vcardio.Address{{Type: "work", Street: "1 Navy Yard", City: "Washington", Region: "DC", PostalCode: "20374", Country: "USA"}},
			Birthday:  "1906-12-09", Note: "Coined the term 'debugging'.",
			URLs: []string{"https://en.wikipedia.org/wiki/Grace_Hopper"},
		},
		color: &color.RGBA{47, 93, 80, 255},
	},
	{
		in: models.ContactInput{
			GivenName: "Alan", FamilyName: "Turing", Org: "GC&CS", Title: "Cryptanalyst",
			Emails: []vcardio.Typed{email("work", "alan@bletchley.demo")},
			Phones: []vcardio.Typed{phone("mobile", "+44 7700 900111")},
		},
		color: &color.RGBA{120, 80, 160, 255},
	},
	{
		in: models.ContactInput{
			GivenName: "Ada", FamilyName: "Lovelace", Org: "Analytical Engine Co.",
			Emails: []vcardio.Typed{email("home", "ada@analytical.demo")},
			URLs:   []string{"https://example.demo/ada"},
		},
	},
}
