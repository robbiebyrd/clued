package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
)

func TestEnvironmentKeysOnlyExchange(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := auth.OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	f.Now = func() time.Time { return now }
	key, err := f.CreateEnvironmentKey("cloud-env", "acct", []string{"a", "b"}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Lookup(key); ok {
		t.Fatal("an environment key authenticated a request")
	}
	ex, err := f.Exchange(context.Background(), key, "container 1/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ex.Name, "cloud-env@container1x-") || ex.TokenID != ex.Name || ex.AccountID != "acct" || !ex.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Errorf("exchanged = %+v", ex)
	}
	p, ok := f.Lookup(ex.Token)
	if !ok || p.AccountID != "acct" || strings.Join(p.Projects, ",") != "a,b" || p.Name != ex.Name {
		t.Fatalf("the exchanged token: %+v, %v", p, ok)
	}
	if _, err := f.Exchange(context.Background(), ex.Token, "", nil); !errors.Is(err, auth.ErrInvalidKey) {
		t.Errorf("a token exchanged as a key: %v", err)
	}
	if _, err := f.Exchange(context.Background(), "nope", "", nil); !errors.Is(err, auth.ErrInvalidKey) {
		t.Errorf("an unknown key: %v", err)
	}

	// The token ends with its lifetime.
	now = now.Add(2 * time.Hour)
	if _, ok := f.Lookup(ex.Token); ok {
		t.Error("an expired token still authenticates")
	}

	// A day after it expired, the next exchange drops it from the file.
	now = now.Add(25 * time.Hour)
	second, err := f.Exchange(context.Background(), key, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := f.List()
	var names []string
	for _, tok := range list {
		names = append(names, tok.Name)
	}
	if len(list) != 2 || list[0].Kind != auth.KindEnvironment || list[1].Parent != "cloud-env" || list[1].Name != second.Name {
		t.Errorf("tokens after the prune: %v", names)
	}

	// Revoking the key revokes what it was exchanged for.
	if err := f.Revoke("cloud-env"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Lookup(second.Token); ok {
		t.Error("a token of a revoked key still authenticates")
	}
	if _, err := f.Exchange(context.Background(), key, "", nil); !errors.Is(err, auth.ErrInvalidKey) {
		t.Errorf("a revoked key exchanged: %v", err)
	}

	// The file survives a reload with kinds, lifetimes and parents.
	again := auth.OpenTokenFile(f.Path())
	reloaded, err := again.List()
	if err != nil || len(reloaded) != 2 || time.Duration(reloaded[0].TokenTTL) != 2*time.Hour || reloaded[1].Expires == nil {
		t.Errorf("reloaded = %+v, %v", reloaded, err)
	}
}

func TestEnvironmentKeyLimits(t *testing.T) {
	f := auth.OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	for _, ttl := range []time.Duration{time.Minute, 8 * 24 * time.Hour} {
		if _, err := f.CreateEnvironmentKey("k", "acct", nil, ttl); err == nil {
			t.Errorf("a lifetime of %s was accepted", ttl)
		}
	}
	key, err := f.CreateEnvironmentKey("k", "acct", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auth.ExchangesPerHour; i++ {
		if _, err := f.Exchange(context.Background(), key, "", nil); err != nil {
			t.Fatalf("exchange %d: %v", i, err)
		}
	}
	if _, err := f.Exchange(context.Background(), key, "", nil); !errors.Is(err, auth.ErrRateLimited) {
		t.Errorf("exchange past the hourly limit: %v", err)
	}
	list, _ := f.List()
	if got := time.Until(*list[1].Expires); got < 23*time.Hour || got > 25*time.Hour {
		t.Errorf("the default lifetime is %s", got)
	}
}
