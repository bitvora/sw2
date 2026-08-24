package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/lmdb"
	"fiatjaf.com/nostr/khatru"
	"github.com/joho/godotenv"
)

type WriteWhitelist struct {
	Pubkeys []string `json:"pubkeys"`
}

func loadWriteWhitelist() (*WriteWhitelist, error) {
	// Try opening "whitelist.json" first
	file, err := os.Open("whitelist.json")
	if err != nil {
		if os.IsNotExist(err) {
			// Fallback to "write_whitelist.json" if "whitelist.json" does not exist
			file, err = os.Open("write_whitelist.json")
			if err != nil {
				return nil, fmt.Errorf("could not open file: %w", err)
			}
		} else {
			return nil, fmt.Errorf("could not open file: %w", err)
		}
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %w", err)
	}

	var writeWhitelist WriteWhitelist
	if err := json.Unmarshal(bytes, &writeWhitelist); err != nil {
		return nil, fmt.Errorf("could not parse JSON: %w", err)
	}

	return &writeWhitelist, nil
}

type ReadWhitelist struct {
	Pubkeys []string `json:"pubkeys"`
}

func loadReadWhitelist(filename string) (*ReadWhitelist, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	bytes, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("could not read file: %w", err)
	}

	var readWhitelist ReadWhitelist
	if err := json.Unmarshal(bytes, &readWhitelist); err != nil {
		return nil, fmt.Errorf("could not parse JSON: %w", err)
	}

	return &readWhitelist, nil
}

// parsePubkeySet converts hex whitelist entries to typed pubkeys for the
// comparisons the new library requires. Unparseable entries are skipped with
// a warning — under the old string comparison they could never match either,
// so behaviour is unchanged.
func parsePubkeySet(pubkeys []string) map[nostr.PubKey]bool {
	set := make(map[nostr.PubKey]bool, len(pubkeys))
	for _, hex := range pubkeys {
		pk, err := nostr.PubKeyFromHex(hex)
		if err != nil {
			fmt.Println("Warning: skipping invalid pubkey in whitelist:", hex)
			continue
		}
		set[pk] = true
	}
	return set
}

// writePolicy preserves sw2's write rules exactly: an empty whitelist admits
// every author; otherwise the event author must be listed. The empty check is
// on the raw list (not the parsed set) so a list of only-invalid entries
// still rejects everyone, as it always has.
func writePolicy(rawCount int, allowed map[nostr.PubKey]bool) func(context.Context, nostr.Event) (bool, string) {
	var zero nostr.PubKey
	return func(ctx context.Context, event nostr.Event) (reject bool, msg string) {
		if event.PubKey == zero {
			return true, "no pubkey"
		}

		// Allow if writeWhitelist is empty
		if rawCount == 0 {
			return false, ""
		}

		if allowed[event.PubKey] {
			return false, ""
		}

		return true, "pubkey not whitelisted"
	}
}

// readPolicy: with an empty read whitelist the relay is publicly readable —
// no authentication asked — matching the README's long-documented behaviour
// ("if read_whitelist.json contains no pubkeys, all users are authorised to
// read"). With a populated list, every query requires NIP-42 authentication
// and the authenticated pubkey must be listed.
func readPolicy(rawCount int, allowed map[nostr.PubKey]bool) func(context.Context, nostr.Filter) (bool, string) {
	return func(ctx context.Context, filter nostr.Filter) (reject bool, msg string) {
		// Allow if readWhitelist is empty
		if rawCount == 0 {
			return false, ""
		}

		authenticatedUser, authed := khatru.GetAuthed(ctx)
		if !authed {
			return true, "auth-required: this query requires you to be authenticated"
		}

		if allowed[authenticatedUser] {
			return false, ""
		}
		return true, "restricted: you're not authorized to read"
	}
}

func main() {
	godotenv.Load(".env")

	relay := khatru.NewRelay()
	db := &lmdb.LMDBBackend{
		Path: "db/",
	}

	if err := db.Init(); err != nil {
		panic(err)
	}

	relay.Info.Name = os.Getenv("RELAY_NAME")
	relay.Info.Icon = os.Getenv("RELAY_ICON")
	relay.Info.Contact = os.Getenv("RELAY_CONTACT")
	relay.Info.Description = os.Getenv("RELAY_DESCRIPTION")
	relay.Info.Software = "https://github.com/bitvora/sw2"
	relay.Info.Version = "0.1.0"
	if hex := os.Getenv("RELAY_PUBKEY"); hex != "" {
		if pk, err := nostr.PubKeyFromHex(hex); err == nil {
			relay.Info.PubKey = &pk
		} else {
			// the old NIP-11 document served any string here; don't refuse to
			// start over a value that previously "worked"
			fmt.Println("Warning: RELAY_PUBKEY is not valid hex, omitting from NIP-11:", hex)
		}
	}

	writeWhitelist, err := loadWriteWhitelist()
	if err != nil {
		fmt.Println("Error loading write whitelist:", err)
		return
	}

	fmt.Println("Write whitelisted pubkeys:")
	for _, pubkey := range writeWhitelist.Pubkeys {
		fmt.Println(pubkey)
	}

	relay.OnEvent = writePolicy(len(writeWhitelist.Pubkeys), parsePubkeySet(writeWhitelist.Pubkeys))

	relay.OnConnect = func(ctx context.Context) {
		khatru.RequestAuth(ctx)
	}

	readWhitelist, err := loadReadWhitelist("read_whitelist.json")
	if err != nil {
		fmt.Println("Error loading read whitelist:", err)
		return
	}

	fmt.Println("Read whitelisted pubkeys:")
	for _, pubkey := range readWhitelist.Pubkeys {
		fmt.Println(pubkey)
	}

	// wires StoreEvent, QueryStored, ReplaceEvent, DeleteEvent and Count,
	// replacing the individual hook assignments of the old khatru API. 1500
	// matches the old lmdb backend's MaxLimit, so explicit client limits are
	// capped exactly as before (no-limit queries previously defaulted to 375;
	// they now get up to 1500).
	relay.UseEventstore(db, 1500)

	relay.OnRequest = readPolicy(len(readWhitelist.Pubkeys), parsePubkeySet(readWhitelist.Pubkeys))

	fmt.Println("running on :3334")
	http.ListenAndServe(":3334", relay)
}
