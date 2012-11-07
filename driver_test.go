package libpq_test

import (
	"database/sql"
	"fmt"
	_ "github.com/jgallagher/go-libpq"
	"os"
	"testing"
	"time"
)

func getConn(t *testing.T) *sql.DB {
	user := os.Getenv("GOSQLTEST_PQ_USER")
	if user == "" {
		user = os.Getenv("USER")
	}
	dbName := "gosqltest"
	db, err := sql.Open("libpq", fmt.Sprintf("user=%s password=gosqltest dbname=%s sslmode=disable", user, dbName))
	if err != nil {
		t.Fatalf("Failed to open database: ", err)
	}
	return db
}

func TestBool(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := false
	err := db.QueryRow("select true").Scan(&val)
	if err != nil || val != true {
		t.Fatalf("Failed to Scan() a bool: ", err)
	}
	err = db.QueryRow("select false").Scan(&val)
	if err != nil || val != false {
		t.Fatalf("Failed to Scan() a bool: ", err)
	}
}

func TestInt(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := int64(0)
	expect := int64(1099511627776)
	err := db.QueryRow("select 1099511627776").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() an int64: ", err)
	}
}

func TestFloat(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := float64(0)
	expect := float64(1.25)
	err := db.QueryRow("select 1.25").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a float64: ", err)
	}
}

func TestByteArray(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	var val []byte
	err := db.QueryRow("select E'\\\\x01020304'::bytea").Scan(&val)
	if err != nil {
		t.Fatalf("Failed to Scan() a []byte: ", err)
	}
	if len(val) != 4 {
		t.Fatalf("Incorrect length of scanned []byte (expected 4, got %d)", len(val))
	}
	for i, v := range val {
		if int(v) != i+1 {
			t.Errorf("Incorrect value for []byte[%d] (expected %d, got %d)", i, i+1, v)
		}
	}
}

func TestString(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := ""
	expect := "a string"
	err := db.QueryRow("select 'a string'").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a string: ", err)
	}
}

func TestTime(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := time.Now()
	expect := time.Date(1999, time.January, 8, 0, 0, 0, 0, time.UTC)
	err := db.QueryRow("select date '1999-Jan-08'").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a DATE: ", err)
	}

	expect = time.Date(1999, time.January, 8, 16, 5, 1, 0, time.UTC)
	err = db.QueryRow("select timestamp '1999-Jan-08 04:05:01 PM'").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a TIMESTAMP: ", err)
	}

	expect = time.Date(1999, time.January, 8, 16, 5, 1, 0, time.Local)
	err = db.QueryRow("select timestamp with time zone '1999-Jan-08 04:05:01 PM'").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a TIMESTAMP WITH TIME ZONE: err=%s, expect=%s, got=%s", err, expect, val)
	}

	expect = time.Date(0, time.January, 1, 16, 5, 1, 0, time.UTC)
	err = db.QueryRow("select time '16:05:01'").Scan(&val)
	if err != nil || val != expect {
		t.Fatalf("Failed to Scan() a TIME: err=%s, expect=%s, got=%s", err, expect, val)
	}
}

func TestNull(t *testing.T) {
	db := getConn(t)
	defer db.Close()
	val := new(string)
	*val = "a string"
	err := db.QueryRow("select NULL").Scan(&val)
	if err != nil || val != nil {
		t.Fatalf("Failed to Scan() NULL: ", err)
	}
}

func TestTimestampWithTimeZone(t *testing.T) {
	db := getConn(t)
	defer db.Close()

	_, err := db.Exec("create temp table test (t timestamp with time zone)")
	if err != nil {
		t.Fatal(err)
	}

	// try several different locations, all included in Go's zoneinfo.zip
	for _, locName := range []string{
		"UTC",
		"America/Chicago",
		"America/New_York",
		"Australia/Darwin",
		"Australia/Perth",
	} {
		loc, err := time.LoadLocation(locName)
		if err != nil {
			t.Logf("Could not load time zone %s - skipping", locName)
			continue
		}

		// Postgres timestamps have a resolution of 1 microsecond, so don't
		// use the full range of the Nanosecond argument
		refTime := time.Date(2012, 11, 6, 10, 23, 42, 123456000, loc)
		_, err = db.Exec("insert into test(t) values($1)", refTime)
		if err != nil {
			t.Fatal(err)
		}

		var gotTime time.Time
		row := db.QueryRow("select t from test")
		err = row.Scan(&gotTime)
		if err != nil {
			t.Fatal(err)
		}

		// Postgres timestamps have a resolution of 1 microsecond
		if !refTime.Equal(gotTime) {
			t.Errorf("timestamps not equal: %s != %s", refTime, gotTime)
		}

		_, err = db.Exec("delete from test")
		if err != nil {
			t.Fatal(err)
		}
	}
}
