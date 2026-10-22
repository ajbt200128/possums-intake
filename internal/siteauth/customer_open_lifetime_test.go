package siteauth_test

import (
	"context"
	"testing"

	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
)

func TestMergedAccountOpenPreservesStoredTokenDuration(t *testing.T) {
	for _, seconds := range []int64{3600, 7200, 43200} {
		dir := t.TempDir()
		ctx := context.Background()
		if ledger.Init(ctx, dir) != nil {
			t.Fatal("synthetic merged initialization unavailable")
		}
		s, err := ledger.Open(ctx, dir)
		if err != nil {
			t.Fatal("synthetic merged open unavailable")
		}
		app, err := s.CustomerAuthApp()
		if err != nil {
			t.Fatal("synthetic merged auth app unavailable")
		}
		setNativeDuration(t, app, seconds)
		if s.Close() != nil {
			t.Fatal("synthetic merged close unavailable")
		}
		s, err = ledger.Open(ctx, dir)
		if err != nil {
			t.Fatal("stored duration rejected on ordinary open")
		}
		app, err = s.CustomerAuthApp()
		if err != nil {
			s.Close()
			t.Fatal("synthetic reopened auth app unavailable")
		}
		c, err := app.FindCollectionByNameOrId(siteauth.CustomerCollection)
		closeErr := s.Close()
		if err != nil || closeErr != nil || c.AuthToken.Duration != seconds {
			t.Fatal("ordinary open rewrote the stored auth lifetime")
		}
	}
}
