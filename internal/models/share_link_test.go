package models_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johannesheinz/skra/internal/models"
	"github.com/johannesheinz/skra/internal/sharing"
	"github.com/johannesheinz/skra/internal/testutil"
)

func TestCreateShareLinkValidation(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	u, _ := models.CreateUser(ctx, d, "u", "u@e.com", "h", models.RoleUser)

	// gated requires a secret.
	if _, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModeGated, Scope: sharing.ScopeBook, TargetID: 1, CreatedBy: u.ID,
	}); err == nil {
		t.Error("gated share without secret should be rejected")
	}
	// non-gated must not carry a secret.
	if _, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: 1, SecretHash: "x", CreatedBy: u.ID,
	}); err == nil {
		t.Error("non-gated share with secret should be rejected")
	}

	// valid public_long.
	link, err := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: 1, CreatedBy: u.ID,
	})
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if link.Token == "" {
		t.Error("missing token")
	}
}

func TestGetAndListAndRevokeShareLink(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	u, _ := models.CreateUser(ctx, d, "u", "u@e.com", "h", models.RoleUser)

	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: 42, CreatedBy: u.ID,
	})

	got, err := models.GetShareLinkByToken(ctx, d, link.Token)
	if err != nil || got.ID != link.ID {
		t.Fatalf("GetShareLinkByToken = %+v, %v", got, err)
	}
	if _, err := models.GetShareLinkByToken(ctx, d, "missing"); !errors.Is(err, models.ErrShareLinkNotFound) {
		t.Errorf("missing token err = %v, want ErrShareLinkNotFound", err)
	}

	list, err := models.ListShareLinksForTarget(ctx, d, sharing.ScopeBook, 42)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v (len %d), err %v", list, len(list), err)
	}

	if err := models.RevokeShareLink(ctx, d, link.ID); err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	got, _ = models.GetShareLinkByToken(ctx, d, link.Token)
	if got.Usable(time.Now()) {
		t.Error("revoked link should not be usable")
	}
}

func TestShareLinkUsable(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour).Format("2006-01-02 15:04:05")
	future := now.Add(time.Hour).Format("2006-01-02 15:04:05")

	cases := []struct {
		name string
		link models.ShareLink
		want bool
	}{
		{"fresh", models.ShareLink{}, true},
		{"revoked", models.ShareLink{Revoked: true}, false},
		{"exhausted", models.ShareLink{MaxUses: 2, UseCount: 2}, false},
		{"under max uses", models.ShareLink{MaxUses: 2, UseCount: 1}, true},
		{"locked by failures", models.ShareLink{FailedCount: sharing.GateMaxFailures}, false},
		{"expired", models.ShareLink{ExpiresAt: past}, false},
		{"not yet expired", models.ShareLink{ExpiresAt: future}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.link.Usable(now); got != tc.want {
				t.Errorf("Usable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIncrementCounters(t *testing.T) {
	d := testutil.NewDB(t)
	ctx := context.Background()
	u, _ := models.CreateUser(ctx, d, "u", "u@e.com", "h", models.RoleUser)
	link, _ := models.CreateShareLink(ctx, d, models.NewShareLinkParams{
		Mode: sharing.ModePublicLong, Scope: sharing.ScopeBook, TargetID: 1, CreatedBy: u.ID,
	})

	if err := models.IncrementShareUse(ctx, d, link.ID); err != nil {
		t.Fatalf("IncrementShareUse: %v", err)
	}
	if err := models.IncrementShareFailure(ctx, d, link.ID); err != nil {
		t.Fatalf("IncrementShareFailure: %v", err)
	}
	got, _ := models.GetShareLinkByToken(ctx, d, link.Token)
	if got.UseCount != 1 || got.FailedCount != 1 {
		t.Errorf("counters = use %d fail %d, want 1/1", got.UseCount, got.FailedCount)
	}
}
