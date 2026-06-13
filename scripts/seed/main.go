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
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

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
	count := flag.Int("extra", 150, "number of generated contacts to create across the books")
	force := flag.Bool("force", false, "seed even if the database already has users")
	flag.Parse()

	if max := len(firstNames) * len(lastNames); *count > max {
		fmt.Fprintf(os.Stderr, "seed: note: %d contacts exceeds %d unique name pairs; names wrap with a numeric suffix\n", *count, max)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	ctx := context.Background()

	existing, err := models.CountUsers(ctx, database)
	if err != nil {
		return err
	}
	if existing > 0 && !*force {
		return fmt.Errorf("database already has %d user(s); pass --force to seed anyway", existing)
	}

	users := map[string]models.User{}
	for _, u := range []struct{ name, role string }{
		{"admin", models.RoleAdmin}, {"alice", models.RoleUser}, {"bob", models.RoleUser},
		{"carol", models.RoleUser}, {"dave", models.RoleUser},
	} {
		created, err := createUser(ctx, database, u.name, u.name+"@demo.test", u.role)
		if err != nil {
			return err
		}
		users[u.name] = created
	}

	type bookSpec struct {
		name, owner, desc string
		grants            map[string]string
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

	for i := 0; i < *count; i++ {
		book := books[i%len(books)]
		in, avatar := generateContact(i)
		contact, err := models.CreateContact(ctx, database, book.ID, in)
		if err != nil {
			return fmt.Errorf("create contact %d: %w", i, err)
		}
		jpeg, err := images.Process(avatar)
		if err != nil {
			return fmt.Errorf("process avatar %d: %w", i, err)
		}
		if err := models.SetContactPhoto(ctx, database, contact.ID, jpeg); err != nil {
			return fmt.Errorf("set photo %d: %w", i, err)
		}
	}

	fmt.Printf(`seeded %s

  users:    admin, alice, bob, carol, dave   (password: %s)
  books:    %d (Work, Friends, Family, Clients) with cross-user grants
  contacts: %d, each with a unique name and an initials avatar

run it:
  SKRA_LISTEN=127.0.0.1:3000 SKRA_DB_PATH=%s \
    SKRA_COOKIE_SECURE=false SKRA_EXTERNAL_URL=http://127.0.0.1:3000 \
    SKRA_SESSION_KEY=dev-only-session-key-not-secret-000 ./skra serve
`, *dbPath, demoPassword, len(books), *count, *dbPath)
	return nil
}

func createUser(ctx context.Context, d *db.DB, username, email, role string) (models.User, error) {
	hash, err := auth.HashPassword(demoPassword)
	if err != nil {
		return models.User{}, err
	}
	return models.CreateUser(ctx, d, username, email, hash, role)
}

// generateContact deterministically builds a unique, varied rich contact from an
// index, returning the input and a rendered avatar PNG. Name pairs are unique up
// to len(firstNames)*len(lastNames); beyond that a numeric suffix keeps them so.
func generateContact(i int) (models.ContactInput, []byte) {
	// Latin-square pairing (pools are equal length): both the first and last
	// name advance each step, and every pair is unique within one full cycle.
	first := firstNames[i%len(firstNames)]
	last := lastNames[(i/len(firstNames)+i)%len(lastNames)]
	if wrap := i / (len(firstNames) * len(lastNames)); wrap > 0 {
		last = fmt.Sprintf("%s-%d", last, wrap+1)
	}
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
		in.Emails = append(in.Emails, vcardio.Typed{Type: "work", Value: slug + "@" + strings.ToLower(strings.ReplaceAll(in.Org, " ", "")) + ".demo"})
	}
	if i%3 == 0 {
		in.Phones = append(in.Phones, vcardio.Typed{Type: "work", Value: fmt.Sprintf("+1 555 %04d", 9000+i)})
	}
	if i%4 == 0 {
		in.Addresses = []vcardio.Address{{Type: "home",
			Street: fmt.Sprintf("%d %s St", 10+i, last), City: cities[i%len(cities)],
			Region: regions[i%len(regions)], PostalCode: fmt.Sprintf("%05d", 10000+i*37%89999), Country: "USA"}}
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

	initials := strings.ToUpper(first[:1] + last[:1])
	return in, avatarPNG(initials, avatarColor(slug))
}

// avatarPNG draws the initials in white, centered on a solid background.
func avatarPNG(initials string, bg color.RGBA) []byte {
	const size = 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)

	// Render the initials small with the bitmap face, then scale up to fill.
	face := basicfont.Face7x13
	tw := font.MeasureString(face, initials).Ceil()
	if tw < 1 {
		tw = 7
	}
	th := face.Metrics().Ascent.Ceil() + face.Metrics().Descent.Ceil()
	text := image.NewRGBA(image.Rect(0, 0, tw, th))
	(&font.Drawer{
		Dst:  text,
		Src:  image.NewUniform(color.White),
		Face: face,
		Dot:  fixed.Point26_6{X: 0, Y: face.Metrics().Ascent},
	}).DrawString(initials)

	scale := float64(size) * 0.55 / float64(max(tw, th))
	dw, dh := int(float64(tw)*scale), int(float64(th)*scale)
	dst := image.Rect((size-dw)/2, (size-dh)/2, (size-dw)/2+dw, (size-dh)/2+dh)
	draw.NearestNeighbor.Scale(img, dst, text, text.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// avatarColor derives a stable, mid-tone background color from a seed string so
// each contact gets a distinct, readable avatar.
func avatarColor(seed string) color.RGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	hue := float64(h.Sum32() % 360)
	return hsv(hue, 0.55, 0.60)
}

func hsv(h, s, v float64) color.RGBA {
	c := v * s
	x := c * (1 - abs(mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return color.RGBA{uint8((r + m) * 255), uint8((g + m) * 255), uint8((b + m) * 255), 255}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func mod(a, b float64) float64 {
	r := a - b*float64(int(a/b))
	if r < 0 {
		r += b
	}
	return r
}

var (
	firstNames = []string{"Grace", "Alan", "Ada", "Katherine", "Dennis", "Jamie", "Priya", "Tom", "Mei", "Omar", "Sofia", "Liam", "Noor", "Hiro", "Elena", "Kwame", "Ingrid", "Diego", "Yuki", "Fatima"}
	lastNames  = []string{"Hopper", "Turing", "Lovelace", "Johnson", "Ritchie", "Rivera", "Nair", "Okafor", "Lin", "Haddad", "Costa", "Murphy", "Khan", "Sato", "Petrova", "Mensah", "Larsen", "Reyes", "Tanaka", "Aziz"}
	orgs       = []string{"Acme", "Globex", "Initech", "Umbrella", "Hooli", "Soylent", "Stark Industries", "Wayne Enterprises"}
	titles     = []string{"Engineer", "Designer", "Manager", "Analyst", "Director", "Consultant", "Researcher", "Coordinator"}
	cities     = []string{"Portland", "Austin", "Denver", "Seattle", "Boston", "Atlanta", "Chicago", "Madison"}
	regions    = []string{"OR", "TX", "CO", "WA", "MA", "GA", "IL", "WI"}
)
