# libpq - cgo-based Postgres driver for Go's database/sql package

## Install

If your Postgres headers and libraries are installed in what appears to be
the typical places:

	/usr/include/libpq-fe.h
	/usr/include/postgresql/server/postgres_fe.h
	/usr/include/postgresql/server/catalog/pg_type.h

or you're on Mac OS X and installed [Postgres.app](http://postgresapp.com/) in
/Applications, then

	go get github.com/jgallagher/go-libpq

should work. If you have build problems, you will need to modify pgdriver.go to
point to the correct locations. (Please let me know if there's a way I could
make this smoother; [this
discussion](https://groups.google.com/forum/#!msg/golang-nuts/ABK6gcHbBjc/eGlxjrmXzfoJ)
seems to imply that there isn't much support for this sort of thing at the
moment.

## Use

TODO: Example

**Connection String Parameters **

## LISTEN/NOTIFY Support

There is no explicit support for NOTIFY; simply calling `Exec("NOTIFY channel,
message")` is sufficient. LISTEN is a different beast. This driver allows for
support for LISTEN completely within the database/sql API, but some care must
be taken to avoid undetectable (by the go runtime) deadlock. Specifically,
to start listening on a channel, issue a Query against an open database:

	// assuming "db" was returned from sql.Open(...)
	notifications, err := db.Query("LISTEN mychan")
	if err != nil {
		// handle "couldn't start listening"
	}

	// wait for a notification to arrive on channel "mychan"
	// WARNING: This call will BLOCK until a notification arrives!
	if !notifications.Next() {
		// this will never happen unless there is a failure with the underlying
		// database connection
	}

	// get the message sent on the channel (possibly "")
	var message string
	notifications.Scan(&message)

It's almost certain that the actual use for this will be inside a goroutine
that relays notifications back on a channel. A full example:

	package main

	import (
		"database/sql"
		"fmt"
		_ "github.com/jgallagher/go-libpq"
		"sync"
	)

	func pglistener(db *sql.DB, messages chan string, wg *sync.WaitGroup) {
		notifications, err := db.Query("LISTEN mychan")
		if err != nil {
			fmt.Printf("Could not listen to mychan: %s\n", err)
			close(messages)
			return
		}
		defer notifications.Close()

		// tell main() it's okay to spawn the pgnotifier goroutine
		wg.Done()

		var msg string
		for notifications.Next() {
			if err = notifications.Scan(&msg); err != nil {
				fmt.Printf("Error while scanning: %s\n", err)
				continue
			}
			messages <- msg
		}

		fmt.Printf("Lost database connection ?!")
		close(messages)
	}

	func notifier(db *sql.DB) {
		for i := 0; i < 10; i++ {
			// WARNING: Postgres does not appear to support parameterized notifications
			//          like "NOTIFY mychan, $1". Be careful not to expose SQL injection!
			query := fmt.Sprintf("NOTIFY mychan, 'message-%d'", i)
			if _, err := db.Exec(query); err != nil {
				fmt.Printf("error sending NOTIFY: %s\n", err)
			}
		}
	}

	func main() {
		db, err := sql.Open("libpq", "") // assuming localhost, user ok, etc
		if err != nil {
			fmt.Printf("could not connect to postgres: %s\n", err)
			return
		}
		defer db.Close()

		messages := make(chan string)
		var wg sync.WaitGroup
		wg.Add(1)
		go pglistener(db, messages, &wg)

		// wait until LISTEN was issued, then spawn notifier goroutine
		wg.Wait()
		go notifier(db)

		for i := 0; i < 10; i++ {
			fmt.Printf("received notification %s\n", <-messages)
		}

		db.Close()
	}

## Testing

Describe how to test.
Add to go-sql-test?
