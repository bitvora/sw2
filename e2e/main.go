// End-to-end test harness for sw2. Unlike the in-process integration tests,
// this spawns the real binary with real whitelist files in a temp directory
// and exercises it over websocket — the automated version of the manual
// checks that used to gate releases.
//
//	go build -o sw2 . && go run ./e2e -binary ./sw2 -matrix
//	go run ./e2e -binary ./sw2 -open     # empty lists: anyone writes, reads are public
//	go run ./e2e -binary ./sw2 -legacy   # whitelist.json takes primacy over write_whitelist.json
//
// The relay listens on the fixed port 3334 (sw2 behaviour), so run one mode
// at a time.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

const relayURL = "ws://localhost:3334"

var failures int

func check(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("PASS  %s\n", name)
		return
	}
	failures++
	fmt.Printf("FAIL  %s — %s\n", name, detail)
}

// startRelay runs the sw2 binary in a temp dir seeded with the given
// whitelist files, waits for the port to accept, and returns a stop func.
func startRelay(binary string, files map[string]string) func() {
	dir, err := os.MkdirTemp("", "sw2-e2e-")
	if err != nil {
		panic(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			panic(err)
		}
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		panic(err)
	}
	cmd := exec.Command(abs)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", "localhost:3334", 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			panic("relay did not start listening on :3334")
		}
		time.Sleep(100 * time.Millisecond)
	}

	return func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(dir)
		// wait for the port to free up before the next mode starts a relay
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", "localhost:3334", 100*time.Millisecond)
			if err != nil {
				return
			}
			conn.Close()
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func pubkeysJSON(sks ...nostr.SecretKey) string {
	hexes := make([]string, len(sks))
	for i, sk := range sks {
		hexes[i] = `"` + nostr.GetPublicKey(sk).Hex() + `"`
	}
	return `{"pubkeys":[` + strings.Join(hexes, ",") + `]}`
}

func tryWrite(ctx context.Context, sk nostr.SecretKey) error {
	client, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()
	evt := nostr.Event{
		PubKey:    nostr.GetPublicKey(sk),
		CreatedAt: nostr.Now(),
		Kind:      1,
		Content:   "e2e",
	}
	if err := evt.Sign(sk); err != nil {
		return err
	}
	return client.Publish(ctx, evt)
}

func auth(ctx context.Context, client *nostr.Relay, sk nostr.SecretKey) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := client.Auth(ctx, func(ctx context.Context, evt *nostr.Event) error { return evt.Sign(sk) })
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// tryRead subscribes to kind 1, authenticating first when sk is non-nil.
func tryRead(ctx context.Context, sk *nostr.SecretKey) (bool, string) {
	client, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
	if err != nil {
		return false, "connect: " + err.Error()
	}
	defer client.Close()
	if sk != nil {
		if err := auth(ctx, client, *sk); err != nil {
			return false, "auth: " + err.Error()
		}
	}
	sub, err := client.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{1}}, nostr.SubscriptionOptions{})
	if err != nil {
		return false, err.Error()
	}
	defer sub.Unsub()
	for {
		select {
		case <-sub.Events:
		case <-sub.EndOfStoredEvents:
			return true, ""
		case reason := <-sub.ClosedReason:
			return false, reason
		case <-time.After(5 * time.Second):
			return false, "timeout"
		}
	}
}

func runMatrix(binary string) {
	skRW, skW, skR, skN := nostr.Generate(), nostr.Generate(), nostr.Generate(), nostr.Generate()
	stop := startRelay(binary, map[string]string{
		"write_whitelist.json": pubkeysJSON(skW, skRW),
		"read_whitelist.json":  pubkeysJSON(skR, skRW),
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		sk       nostr.SecretKey
		canWrite bool
		canRead  bool
	}{
		{"RW", skRW, true, true},
		{"W (write only)", skW, true, false},
		{"R (read only)", skR, false, true},
		{"N (neither)", skN, false, false},
	}
	for _, tc := range cases {
		err := tryWrite(ctx, tc.sk)
		if tc.canWrite {
			check(tc.name+": can write", err == nil, fmt.Sprintf("%v", err))
		} else {
			check(tc.name+": cannot write", err != nil && strings.Contains(err.Error(), "not whitelisted"),
				fmt.Sprintf("expected whitelist rejection, got %v", err))
		}
		allowed, reason := tryRead(ctx, &tc.sk)
		if tc.canRead {
			check(tc.name+": can read", allowed, "CLOSED "+reason)
		} else {
			check(tc.name+": cannot read", !allowed && strings.Contains(reason, "not authorized"),
				fmt.Sprintf("expected restricted, got allowed=%v %q", allowed, reason))
		}
	}
}

func runOpen(binary string) {
	stop := startRelay(binary, map[string]string{
		"write_whitelist.json": `{"pubkeys":[]}`,
		"read_whitelist.json":  `{"pubkeys":[]}`,
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sk := nostr.Generate()
	check("empty write list: anyone can write", tryWrite(ctx, sk) == nil, "publish rejected")
	allowed, reason := tryRead(ctx, &sk)
	check("empty read list: any authenticated user can read", allowed, "CLOSED "+reason)
	allowed, reason = tryRead(ctx, nil)
	check("empty read list: publicly readable without auth", allowed, "CLOSED "+reason)
}

func runLegacy(binary string) {
	skLegacy, skOther := nostr.Generate(), nostr.Generate()
	stop := startRelay(binary, map[string]string{
		"whitelist.json":       pubkeysJSON(skLegacy),
		"write_whitelist.json": pubkeysJSON(skOther), // must be ignored: whitelist.json wins
		"read_whitelist.json":  `{"pubkeys":[]}`,
	})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	check("legacy whitelist.json author can write", tryWrite(ctx, skLegacy) == nil, "publish rejected")
	err := tryWrite(ctx, skOther)
	check("write_whitelist.json ignored when whitelist.json present",
		err != nil && strings.Contains(err.Error(), "not whitelisted"),
		fmt.Sprintf("expected rejection, got %v", err))
}

func main() {
	binary := flag.String("binary", "./sw2", "path to the sw2 binary")
	matrix := flag.Bool("matrix", false, "run the 4-key read/write permission matrix")
	open := flag.Bool("open", false, "run the empty-whitelists checks")
	legacy := flag.Bool("legacy", false, "run the legacy whitelist.json primacy check")
	flag.Parse()

	if !*matrix && !*open && !*legacy {
		fmt.Println("pick a mode: -matrix, -open, or -legacy")
		os.Exit(2)
	}
	if *matrix {
		runMatrix(*binary)
	}
	if *open {
		runOpen(*binary)
	}
	if *legacy {
		runLegacy(*binary)
	}
	if failures > 0 {
		fmt.Printf("\n%d failure(s)\n", failures)
		os.Exit(1)
	}
	fmt.Println("\nall checks passed")
}
