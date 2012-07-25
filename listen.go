package libpq

/*
#include <stdlib.h>
#include <sys/select.h>
#include <libpq-fe.h>

static PGnotify *waitForNotify(PGconn *conn) {
	int sock;
	fd_set input_mask;
	PGnotify *note;

	sock = PQsocket(conn);
	if (sock < 0) {
		return NULL;
	}

	while (1) {
		FD_ZERO(&input_mask);
		FD_SET(sock, &input_mask);

		// block waiting for input
		if (select(sock+1, &input_mask, NULL, NULL, NULL) < 0) {
			return NULL;
		}

		// check for notifications
		PQconsumeInput(conn);
		if ((note = PQnotifies(conn)) != NULL) {
			return note;
		}
	}
}
*/
import "C"
import (
	"database/sql/driver"
	"errors"
	"io"
	"unsafe"
)

var (
	ErrListenStmtNoExec       = errors.New("libpq: Exec() not supported for LISTEN statements")
	ErrWaitingForNotification = errors.New("libpq: Fatal error waiting for NOTIFY")
)

type libpqListenStmt struct {
	c     *libpqConn
	query string
}

func (c *libpqConn) prepareListen(query string) (driver.Stmt, error) {
	if err := c.exec(query, nil); err != nil {
		return nil, err
	}

	return &libpqListenStmt{c, query}, nil
}

func (s *libpqListenStmt) Query(args []driver.Value) (driver.Rows, error) {
	// first check to see if we have any pending notifications already
	note := C.PQnotifies(s.c.db)
	if note == nil {
		// none pending - block waiting for one
		note = C.waitForNotify(s.c.db)
		if note == nil {
			return nil, ErrWaitingForNotification
		}
	}
	defer C.PQfreemem(unsafe.Pointer(note))
	return &libpqNotificationRows{C.GoString(note.extra), false}, nil
}

func (s *libpqListenStmt) Close() error {
	// issue unlisten - assumes s.query starts with "listen", which is true
	// given the check in libpqConn.Prepare()
	return s.c.exec("un"+s.query, nil)
}

func (s *libpqListenStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, ErrListenStmtNoExec
}

func (s *libpqListenStmt) NumInput() int {
	return 0
}

type libpqNotificationRows struct {
	payload  string
	reported bool
}

func (r *libpqNotificationRows) Close() error {
	return nil
}

func (r *libpqNotificationRows) Columns() []string {
	return []string{"NOTIFY payload"}
}

func (r *libpqNotificationRows) Next(dest []driver.Value) error {
	if r.reported {
		return io.EOF
	}
	r.reported = true
	if len(dest) > 0 {
		dest[0] = r.payload
	}
	return nil
}
