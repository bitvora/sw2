// export-legacy-db dumps every event from a pre-nostrlib sw2 database as
// JSON lines on stdout. The on-disk event encoding changed when sw2 moved to
// fiatjaf.com/nostr, so old databases cannot be read by the new binary; this
// tool (pinned to the old library) extracts the events for replay.
//
// Usage, from the sw2 directory, with the relay STOPPED:
//
//	cd tools/export-legacy-db && go build -o export-legacy-db .
//	./export-legacy-db ../../db > events.jsonl
//
// then move the old db aside, start the new sw2, and replay:
//
//	cat events.jsonl | nak event ws://localhost:3334
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/fiatjaf/eventstore/lmdb"
	"github.com/nbd-wtf/go-nostr"
)

func main() {
	path := "db/"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	// the old backend materializes results in RAM before returning, so the
	// export needs memory proportional to the database size
	db := lmdb.LMDBBackend{Path: path, MaxLimit: 1 << 24}
	if err := db.Init(); err != nil {
		fmt.Fprintln(os.Stderr, "could not open database:", err)
		os.Exit(1)
	}

	ch, err := db.QueryEvents(context.Background(), nostr.Filter{Limit: 1 << 24})
	if err != nil {
		fmt.Fprintln(os.Stderr, "query failed:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	count := 0
	for evt := range ch {
		if err := enc.Encode(evt); err != nil {
			fmt.Fprintln(os.Stderr, "encode failed:", err)
			os.Exit(1)
		}
		count++
	}
	fmt.Fprintf(os.Stderr, "exported %d events\n", count)
}
