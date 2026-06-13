package rbac

import (
	"testing"

	"github.com/johannesheinz/skra/internal/models"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name        string
		isAdmin     bool
		level       string
		hasGrant    bool
		action      Action
		wantAllow   bool
		wantVisible bool
	}{
		{name: "admin read", isAdmin: true, action: Read, wantAllow: true, wantVisible: true},
		{name: "admin write", isAdmin: true, action: Write, wantAllow: true, wantVisible: true},
		{name: "no grant read", action: Read, wantAllow: false, wantVisible: false},
		{name: "no grant write", action: Write, wantAllow: false, wantVisible: false},
		{name: "viewer read", level: models.AccessViewer, hasGrant: true, action: Read, wantAllow: true, wantVisible: true},
		{name: "viewer write", level: models.AccessViewer, hasGrant: true, action: Write, wantAllow: false, wantVisible: true},
		{name: "manager read", level: models.AccessManager, hasGrant: true, action: Read, wantAllow: true, wantVisible: true},
		{name: "manager write", level: models.AccessManager, hasGrant: true, action: Write, wantAllow: true, wantVisible: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.isAdmin, tc.level, tc.hasGrant, tc.action)
			if got.Allow != tc.wantAllow || got.Visible != tc.wantVisible {
				t.Errorf("Evaluate = %+v, want {Allow:%v Visible:%v}", got, tc.wantAllow, tc.wantVisible)
			}
		})
	}
}
