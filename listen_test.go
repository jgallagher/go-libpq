package libpq

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
)

func TestListen(t *testing.T) {
	user := os.Getenv("GOSQLTEST_PQ_USER")
	if user == "" {
		user = os.Getenv("USER")
	}
	dbName := "gosqltest"
	db, err := sql.Open("libpq", fmt.Sprintf("user=%s password=foo dbname=%s sslmode=disable", user, dbName))
	if err != nil {
		t.Fatalf("Failed to open database: ", err)
	}

	// start listening
	stmt, err := db.Prepare("LISTEN channel")
	if err != nil {
		t.Fatalf("Failed to prepare LISTEN: ", err)
	}

	// make sure a plain NOTIFY with no payload works
	// this has to execute in another goroutine because stmt's Scan() blocks
	db.Exec("NOTIFY channel")

	payload := "bogus payload"
	err = stmt.QueryRow().Scan(&payload)
	if err != nil {
		t.Fatalf("Failed to receive NOTIFY: ", err)
	}
	if payload != "" {
		t.Fatalf("Received unexpected payload '%s' (expected '')", payload)
	}

	// we can also pass in a payload
	db.Exec("NOTIFY channel, 'the payload'")
	err = stmt.QueryRow().Scan(&payload)
	if err != nil {
		t.Fatalf("Failed to receive NOTIFY: ", err)
	}
	if payload != "the payload" {
		t.Fatalf("Received unexpected payload '%s' (expected 'the payload')", payload)
	}

	// if we close the statement, we should UNLISTEN
	// test this by sending a notification after closing, then re-listen, send
	// a different notification, and make sure we only get the second one
	stmt.Close()
	db.Exec("NOTIFY channel, 'the first'")
	stmt, err = db.Prepare("LISTEN channel")
	if err != nil {
		t.Fatalf("Failed to prepare LISTEN: ", err)
	}
	db.Exec("NOTIFY channel, 'the second'")
	err = stmt.QueryRow().Scan(&payload)
	if err != nil {
		t.Fatalf("Failed to receive NOTIFY: ", err)
	}
	if payload == "the first" {
		t.Fatalf("Incorrectly received notification sent while we weren't listening")
	}
	if payload != "the second" {
		t.Fatalf("Received unexpected payload '%s' (expected 'the second')", payload)
	}
}
