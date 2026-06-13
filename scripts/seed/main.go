// Command seed populates a Skrá database with demo data for development and
// demos. It uses the real model write path, so contacts get proper vcard_raw,
// passwords are argon2id-hashed, and photos run through the ingest pipeline.
//
// Usage:
//
//	go run ./scripts/seed --db skra-demo.db
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

	admin, err := createUser(ctx, database, "admin", "admin@demo.test", models.RoleAdmin)
	if err != nil {
		return err
	}
	alice, err := createUser(ctx, database, "alice", "alice@demo.test", models.RoleUser)
	if err != nil {
		return err
	}
	bob, err := createUser(ctx, database, "bob", "bob@demo.test", models.RoleUser)
	if err != nil {
		return err
	}

	// Work book owned by the admin; alice manages it, bob may view it.
	work, err := models.CreateAddressBook(ctx, database, admin.ID, "Work", "Colleagues and clients")
	if err != nil {
		return err
	}
	if err := models.AddOrUpdateMember(ctx, database, work.ID, alice.ID, models.AccessManager, admin.ID); err != nil {
		return err
	}
	if err := models.AddOrUpdateMember(ctx, database, work.ID, bob.ID, models.AccessViewer, admin.ID); err != nil {
		return err
	}

	// Friends book owned by alice.
	friends, err := models.CreateAddressBook(ctx, database, alice.ID, "Friends", "People I actually like")
	if err != nil {
		return err
	}

	if err := seedContacts(ctx, database, work.ID, workContacts); err != nil {
		return err
	}
	if err := seedContacts(ctx, database, friends.ID, friendContacts); err != nil {
		return err
	}

	fmt.Printf(`seeded %s

  users:    admin / alice / bob   (password: %s)
  books:    "Work" (admin; alice=manager, bob=viewer), "Friends" (alice)
  contacts: %d in Work, %d in Friends

run it:
  SKRA_LISTEN=127.0.0.1:3000 SKRA_DB_PATH=%s \
    SKRA_COOKIE_SECURE=false SKRA_EXTERNAL_URL=http://127.0.0.1:3000 \
    SKRA_SESSION_KEY=dev-only-session-key-not-secret-000 ./skra serve
`, *dbPath, demoPassword, len(workContacts), len(friendContacts), *dbPath)
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

var workContacts = []demoContact{
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
	{
		in: models.ContactInput{
			GivenName: "Katherine", FamilyName: "Johnson", Org: "NASA", Title: "Mathematician",
			Emails:    []vcardio.Typed{email("work", "katherine@nasa.demo")},
			Phones:    []vcardio.Typed{phone("work", "+1 757 555 0188")},
			Birthday:  "1918-08-26",
			Addresses: []vcardio.Address{{Type: "work", City: "Hampton", Region: "VA", Country: "USA"}},
		},
		color: &color.RGBA{180, 90, 60, 255},
	},
	{
		in: models.ContactInput{
			GivenName: "Dennis", FamilyName: "Ritchie", Org: "Bell Labs", Title: "Researcher",
			Emails: []vcardio.Typed{email("work", "dmr@bell.demo")},
		},
	},
}

var friendContacts = []demoContact{
	{
		in: models.ContactInput{
			GivenName: "Jamie", FamilyName: "Rivera",
			Emails: []vcardio.Typed{email("home", "jamie@friends.demo")},
			Phones: []vcardio.Typed{phone("mobile", "+1 415 555 0143"), phone("home", "+1 415 555 0199")},
			Note:   "Makes excellent tacos.",
		},
		color: &color.RGBA{60, 120, 180, 255},
	},
	{
		in: models.ContactInput{
			GivenName: "Priya", FamilyName: "Nair",
			Emails:    []vcardio.Typed{email("home", "priya@friends.demo")},
			Birthday:  "1992-03-14",
			Addresses: []vcardio.Address{{Type: "home", Street: "22 Garden Way", City: "Portland", Region: "OR", PostalCode: "97201", Country: "USA"}},
		},
	},
	{
		in: models.ContactInput{
			GivenName: "Tom", FamilyName: "Okafor",
			Phones: []vcardio.Typed{phone("mobile", "+44 7700 900222")},
		},
		color: &color.RGBA{150, 120, 50, 255},
	},
	{
		in: models.ContactInput{
			GivenName: "Mei", FamilyName: "Lin",
			Emails: []vcardio.Typed{email("home", "mei@friends.demo"), email("work", "mei.lin@work.demo")},
		},
	},
}
