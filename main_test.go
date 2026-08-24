package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWriteWhitelist(t *testing.T) {
	t.Run("legacy whitelist.json takes primacy", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "whitelist.json", `{"pubkeys":["aa"]}`)
		writeFile(t, dir, "write_whitelist.json", `{"pubkeys":["bb","cc"]}`)
		t.Chdir(dir)
		wl, err := loadWriteWhitelist()
		if err != nil {
			t.Fatal(err)
		}
		if len(wl.Pubkeys) != 1 || wl.Pubkeys[0] != "aa" {
			t.Errorf("whitelist.json must win over write_whitelist.json, got %v", wl.Pubkeys)
		}
	})

	t.Run("falls back to write_whitelist.json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "write_whitelist.json", `{"pubkeys":["bb"]}`)
		t.Chdir(dir)
		wl, err := loadWriteWhitelist()
		if err != nil {
			t.Fatal(err)
		}
		if len(wl.Pubkeys) != 1 || wl.Pubkeys[0] != "bb" {
			t.Errorf("got %v", wl.Pubkeys)
		}
	})

	t.Run("error when neither file exists", func(t *testing.T) {
		t.Chdir(t.TempDir())
		if _, err := loadWriteWhitelist(); err == nil {
			t.Error("expected error with no whitelist files")
		}
	})
}

func TestLoadReadWhitelist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "read_whitelist.json", `{"pubkeys":["dd"]}`)
	t.Chdir(dir)
	rl, err := loadReadWhitelist("read_whitelist.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(rl.Pubkeys) != 1 || rl.Pubkeys[0] != "dd" {
		t.Errorf("got %v", rl.Pubkeys)
	}
}

func TestParsePubkeySet(t *testing.T) {
	pk := nostr.GetPublicKey(nostr.Generate())
	set := parsePubkeySet([]string{pk.Hex(), "not-hex", ""})
	if len(set) != 1 || !set[pk] {
		t.Errorf("valid entry must parse, invalid must be skipped: %v", set)
	}
}

func TestWritePolicy(t *testing.T) {
	skListed := nostr.Generate()
	pkListed := nostr.GetPublicKey(skListed)
	pkOther := nostr.GetPublicKey(nostr.Generate())
	ctx := context.Background()

	t.Run("empty list admits every author", func(t *testing.T) {
		policy := writePolicy(0, parsePubkeySet(nil))
		if reject, _ := policy(ctx, nostr.Event{PubKey: pkOther}); reject {
			t.Error("empty whitelist must admit all")
		}
	})

	t.Run("listed author admitted, others rejected", func(t *testing.T) {
		policy := writePolicy(1, parsePubkeySet([]string{pkListed.Hex()}))
		if reject, _ := policy(ctx, nostr.Event{PubKey: pkListed}); reject {
			t.Error("listed author must be admitted")
		}
		reject, msg := policy(ctx, nostr.Event{PubKey: pkOther})
		if !reject || msg != "pubkey not whitelisted" {
			t.Errorf("unlisted author must be rejected with the historic message, got %v %q", reject, msg)
		}
	})

	t.Run("list of only-invalid entries rejects everyone (historic behaviour)", func(t *testing.T) {
		policy := writePolicy(2, parsePubkeySet([]string{"junk", "more-junk"}))
		if reject, _ := policy(ctx, nostr.Event{PubKey: pkOther}); !reject {
			t.Error("a non-empty list that parses to nothing must reject, not fall open")
		}
	})

	t.Run("zero pubkey rejected", func(t *testing.T) {
		policy := writePolicy(0, nil)
		reject, msg := policy(ctx, nostr.Event{})
		if !reject || msg != "no pubkey" {
			t.Errorf("got %v %q", reject, msg)
		}
	})
}

func TestReadPolicyUnauthenticated(t *testing.T) {
	// the authenticated paths are covered by the integration matrix
	t.Run("empty list: publicly readable, no auth asked", func(t *testing.T) {
		policy := readPolicy(0, nil)
		if reject, msg := policy(context.Background(), nostr.Filter{}); reject {
			t.Errorf("empty read whitelist must allow unauthenticated reads, got rejected %q", msg)
		}
	})
	t.Run("populated list: auth required", func(t *testing.T) {
		policy := readPolicy(1, nil)
		reject, msg := policy(context.Background(), nostr.Filter{})
		if !reject || msg != "auth-required: this query requires you to be authenticated" {
			t.Errorf("got %v %q", reject, msg)
		}
	})
}
