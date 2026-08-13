package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/slicestore"
	"fiatjaf.com/nostr/khatru"
)

// In-process integration tests: a real khatru relay over httptest with the
// in-memory slicestore, wired exactly like main(). These pin the read/write
// permission matrix that used to be verified by hand:
//
//	key      write list  read list   can write   can read
//	RW       yes         yes         yes         yes
//	W        yes         no          yes         no
//	R        no          yes         no          yes
//	N        no          no          no          no

func newTestRelay(t *testing.T, writeList, readList []string) string {
	t.Helper()
	store := &slicestore.SliceStore{}
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}
	relay := khatru.NewRelay()
	relay.OnEvent = writePolicy(len(writeList), parsePubkeySet(writeList))
	relay.OnConnect = func(ctx context.Context) { khatru.RequestAuth(ctx) }
	relay.UseEventstore(store, 500)
	relay.OnRequest = readPolicy(len(readList), parsePubkeySet(readList))

	srv := httptest.NewServer(relay)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

// tryWrite publishes a kind-1 event signed by sk; returns nil if accepted.
func tryWrite(t *testing.T, ctx context.Context, url string, sk nostr.SecretKey) error {
	t.Helper()
	client, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()
	evt := nostr.Event{
		PubKey:    nostr.GetPublicKey(sk),
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   "hello",
	}
	if err := evt.Sign(sk); err != nil {
		t.Fatal(err)
	}
	return client.Publish(ctx, evt)
}

// tryRead subscribes to kind 1. When authWith is non-nil it authenticates
// first (NIP-42). Returns (allowed, closedReason).
func tryRead(t *testing.T, ctx context.Context, url string, authWith *nostr.SecretKey) (bool, string) {
	t.Helper()
	client, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	if authWith != nil {
		authClient(t, ctx, client, *authWith)
	}

	sub, err := client.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{1}}, nostr.SubscriptionOptions{})
	if err != nil {
		return false, err.Error()
	}
	defer sub.Unsub()

	for {
		select {
		case <-sub.Events: // drain stored events until EOSE decides the outcome
		case <-sub.EndOfStoredEvents:
			return true, ""
		case reason := <-sub.ClosedReason:
			return false, reason
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for EOSE or CLOSED")
			return false, ""
		}
	}
}

// authClient performs NIP-42 auth, retrying briefly: the relay sends its
// AUTH challenge asynchronously on connect, so an immediate Auth can race it.
// Note: `go test -race` currently flags a data race inside the nostrlib
// *client* (challenge write in handleMessage vs read in Auth) — an upstream
// library issue, not a relay bug.
func authClient(t *testing.T, ctx context.Context, client *nostr.Relay, sk nostr.SecretKey) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := client.Auth(ctx, func(ctx context.Context, evt *nostr.Event) error { return evt.Sign(sk) })
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auth: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestPermissionMatrix(t *testing.T) {
	skRW, skW, skR, skN := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	url := newTestRelay(t,
		[]string{nostr.GetPublicKey(skW).Hex(), nostr.GetPublicKey(skRW).Hex()}, // write list
		[]string{nostr.GetPublicKey(skR).Hex(), nostr.GetPublicKey(skRW).Hex()}, // read list
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		sk       nostr.SecretKey
		canWrite bool
		canRead  bool
	}{
		{"RW: read and write", skRW, true, true},
		{"W: write only", skW, true, false},
		{"R: read only", skR, false, true},
		{"N: neither", skN, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tryWrite(t, ctx, url, tc.sk)
			if tc.canWrite && err != nil {
				t.Errorf("write should be allowed, got %v", err)
			}
			if !tc.canWrite {
				if err == nil || !strings.Contains(err.Error(), "not whitelisted") {
					t.Errorf("write should be rejected with 'pubkey not whitelisted', got %v", err)
				}
			}

			allowed, reason := tryRead(t, ctx, url, &tc.sk)
			if tc.canRead && !allowed {
				t.Errorf("read should be allowed, got CLOSED %q", reason)
			}
			if !tc.canRead {
				if allowed || !strings.Contains(reason, "not authorized to read") {
					t.Errorf("read should be rejected with 'restricted', got allowed=%v reason=%q", allowed, reason)
				}
			}
		})
	}
}

func TestUnauthenticatedRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	t.Run("populated read list: rejected with auth-required", func(t *testing.T) {
		url := newTestRelay(t, nil, []string{nostr.GetPublicKey(nostr.Generate()).Hex()})
		allowed, reason := tryRead(t, ctx, url, nil)
		if allowed || !strings.Contains(reason, "auth-required") {
			t.Errorf("got allowed=%v reason=%q", allowed, reason)
		}
	})

	t.Run("empty read list: publicly readable without auth", func(t *testing.T) {
		url := newTestRelay(t, nil, nil)
		allowed, reason := tryRead(t, ctx, url, nil)
		if !allowed {
			t.Errorf("empty read list must be publicly readable; got CLOSED %q", reason)
		}
	})
}

func TestEmptyLists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	url := newTestRelay(t, nil, nil)
	skAnyone := nostr.Generate()

	if err := tryWrite(t, ctx, url, skAnyone); err != nil {
		t.Errorf("empty write list must admit any author, got %v", err)
	}
	allowed, reason := tryRead(t, ctx, url, &skAnyone)
	if !allowed {
		t.Errorf("empty read list must admit any authenticated user, got CLOSED %q", reason)
	}
}

func TestWrittenEventsAreServed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sk := nostr.Generate()
	url := newTestRelay(t, nil, nil)

	if err := tryWrite(t, ctx, url, sk); err != nil {
		t.Fatal(err)
	}

	client, err := nostr.RelayConnect(ctx, url, nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	authClient(t, ctx, client, sk)
	count := 0
	for range client.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{1}}) {
		count++
	}
	if count != 1 {
		t.Errorf("expected the written event to be served, got %d", count)
	}
}
