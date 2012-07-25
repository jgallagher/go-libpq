package libpq

/*
#include <c.h>
#include <catalog/pg_type.h>
#include <libpq-fe.h>
*/
import "C"
import (
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unsafe"
)

const timeFormat = "2006-01-02 15:04:05.000000-07"

var (
	ErrLastInsertId = errors.New("libpq: LastInsertId() not supported")
	ErrThreadSafety = errors.New("libpq: Not compiled for thread-safe operation")
)

type libpqDriver struct {
}

func init() {
	go handleArgpool()
	sql.Register("libpq", &libpqDriver{})
}

func (d *libpqDriver) Open(dsn string) (driver.Conn, error) {
	if C.PQisthreadsafe() != 1 {
		return nil, ErrThreadSafety
	}

	params := C.CString(dsn)
	defer C.free(unsafe.Pointer(params))

	db := C.PQconnectdb(params)
	if C.PQstatus(db) != C.CONNECTION_OK {
		defer C.PQfinish(db)
		return nil, errors.New("libpq: connection error " + C.GoString(C.PQerrorMessage(db)))
	}

	return &libpqConn{
		db,
		make(map[string]driver.Stmt),
		0,
	}, nil
}

type libpqConn struct {
	db        *C.PGconn
	stmtCache map[string]driver.Stmt
	stmtNum   int
}

func (c *libpqConn) Begin() (driver.Tx, error) {
	if err := c.exec("BEGIN", nil); err != nil {
		return nil, err
	}
	return &libpqTx{c}, nil
}

type libpqTx struct {
	c *libpqConn
}

func (tx *libpqTx) Commit() error {
	return tx.c.exec("COMMIT", nil)
}

func (tx *libpqTx) Rollback() error {
	return tx.c.exec("ROLLBACK", nil)
}

func (c *libpqConn) Close() error {
	C.PQfinish(c.db)
	// free cached prepared statement names
	for _, v := range c.stmtCache {
		if stmt, ok := v.(*libpqStmt); ok {
			C.free(unsafe.Pointer(stmt.name))
		}
	}
	return nil
}

func (c *libpqConn) exec(cmd string, res *libpqResult) error {
	ccmd := C.CString(cmd)
	defer C.free(unsafe.Pointer(ccmd))
	cres := C.PQexec(c.db, ccmd)
	defer C.PQclear(cres)
	if err := resultError(cres); err != nil {
		return err
	}

	// check to see if caller cares about number of rows modified
	if res == nil {
		return nil
	}

	nrows, err := getNumRows(cres)
	if err != nil {
		return err
	}

	*res = libpqResult(nrows)
	return nil
}

func (c *libpqConn) execParams(cmd string, args []driver.Value) (driver.Result, error) {
	// convert args into C array-of-strings
	cargs, err := buildCArgs(args)
	if err != nil {
		return nil, err
	}
	defer returnCharArrayToPool(len(args), cargs)

	ccmd := C.CString(cmd)
	defer C.free(unsafe.Pointer(ccmd))

	// execute
	cres := C.PQexecParams(c.db, ccmd, C.int(len(args)), nil, cargs, nil, nil, 0)
	defer C.PQclear(cres)
	if err = resultError(cres); err != nil {
		return nil, err
	}

	// get modified rows
	nrows, err := getNumRows(cres)
	if err != nil {
		return nil, err
	}

	return libpqResult(nrows), nil
}

func (c *libpqConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	if len(args) != 0 {
		return c.execParams(query, args)
	}

	var res libpqResult
	if err := c.exec(query, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (c *libpqConn) Prepare(query string) (driver.Stmt, error) {
	// check our connection's query cache to see if we've already prepared this
	cached, ok := c.stmtCache[query]
	if ok {
		return cached, nil
	}

	// check to see if this is a LISTEN
	if strings.HasPrefix(strings.ToLower(query), "listen") {
		return c.prepareListen(query)
	}

	// create unique statement name
	// NOTE: do NOT free cname here because it is cached; free it in c.Close()
	cname := C.CString(strconv.Itoa(c.stmtNum))
	c.stmtNum++
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))

	// initial query preparation
	cres := C.PQprepare(c.db, cname, cquery, 0, nil)
	defer C.PQclear(cres)
	if err := resultError(cres); err != nil {
		C.PQclear(cres)
		return nil, err
	}

	// get number of parameters in this query
	cinfo := C.PQdescribePrepared(c.db, cname)
	defer C.PQclear(cinfo)
	if err := resultError(cinfo); err != nil {
		return nil, err
	}
	nparams := int(C.PQnparams(cinfo))

	// save in cache
	c.stmtCache[query] = &libpqStmt{c: c, name: cname, nparams: nparams}

	return c.stmtCache[query], nil
}

type libpqStmt struct {
	c       *libpqConn
	name    *C.char
	nparams int
}

func (s *libpqStmt) Close() error {
	// nothing to do - prepared statement names are cached and will be
	// freed in s.c.Close()
	return nil
}

func (s *libpqStmt) NumInput() int {
	return s.nparams
}

func (s *libpqStmt) exec(args []driver.Value) (*C.PGresult, error) {
	// convert args into C array-of-strings
	cargs, err := buildCArgs(args)
	if err != nil {
		return nil, err
	}
	defer returnCharArrayToPool(len(args), cargs)

	// execute
	cres := C.PQexecPrepared(s.c.db, s.name, C.int(len(args)), cargs, nil, nil, 0)
	if err = resultError(cres); err != nil {
		C.PQclear(cres)
		return nil, err
	}
	return cres, nil
}

func (s *libpqStmt) Exec(args []driver.Value) (driver.Result, error) {
	// execute prepared statement
	cres, err := s.exec(args)
	if err != nil {
		return nil, err
	}
	defer C.PQclear(cres)

	nrows, err := getNumRows(cres)
	if err != nil {
		return nil, err
	}

	return libpqResult(nrows), nil
}

func (s *libpqStmt) Query(args []driver.Value) (driver.Rows, error) {
	// execute prepared statement
	cres, err := s.exec(args)
	if err != nil {
		return nil, err
	}
	return &libpqRows{
		s:       s,
		res:     cres,
		ncols:   int(C.PQnfields(cres)),
		nrows:   int(C.PQntuples(cres)),
		currRow: 0,
		cols:    nil,
	}, nil
}

type libpqRows struct {
	s       *libpqStmt
	res     *C.PGresult
	ncols   int
	nrows   int
	currRow int
	cols    []string
}

func resultError(res *C.PGresult) error {
	status := C.PQresultStatus(res)
	if status == C.PGRES_COMMAND_OK || status == C.PGRES_TUPLES_OK {
		return nil
	}
	return errors.New("libpq: result error: " + C.GoString(C.PQresultErrorMessage(res)))
}

func getNumRows(cres *C.PGresult) (int64, error) {
	rowstr := C.GoString(C.PQcmdTuples(cres))
	if rowstr == "" {
		return 0, nil
	}

	return strconv.ParseInt(rowstr, 10, 64)
}

func (r *libpqRows) Close() error {
	C.PQclear(r.res)
	return nil
}

func (r *libpqRows) Columns() []string {
	if r.cols == nil {
		r.cols = make([]string, r.ncols)
		for i := 0; i < r.ncols; i++ {
			r.cols[i] = C.GoString(C.PQfname(r.res, C.int(i)))
		}
	}
	return r.cols
}

func (r *libpqRows) Next(dest []driver.Value) error {
	if r.currRow >= r.nrows {
		return io.EOF
	}
	currRow := C.int(r.currRow)
	r.currRow++

	for i := 0; i < len(dest); i++ {
		ci := C.int(i)

		// check for NULL
		if int(C.PQgetisnull(r.res, currRow, ci)) == 1 {
			dest[i] = nil
			continue
		}

		val := C.GoString(C.PQgetvalue(r.res, currRow, ci))
		var err error
		switch vtype := uint(C.PQftype(r.res, C.int(i))); vtype {
		case C.BOOLOID:
			if val == "t" {
				dest[i] = "true"
			} else {
				dest[i] = "false"
			}
		case C.BYTEAOID:
			if !strings.HasPrefix(val, `\x`) {
				return errors.New("libpq: invalid byte string format")
			}
			dest[i], err = hex.DecodeString(val[2:])
			if err != nil {
				return errors.New(fmt.Sprint("libpq: could not decode hex string: %s", err))
			}
		case C.CHAROID, C.BPCHAROID, C.VARCHAROID, C.TEXTOID,
			C.INT2OID, C.INT4OID, C.INT8OID, C.OIDOID, C.XIDOID,
			C.FLOAT8OID, C.FLOAT4OID,
			C.DATEOID, C.TIMEOID, C.TIMESTAMPOID, C.TIMESTAMPTZOID, C.INTERVALOID, C.TIMETZOID,
			C.NUMERICOID:
			dest[i] = val
		default:
			return errors.New(fmt.Sprintf("unsupported type oid: %d", vtype))
		}
	}

	return nil
}

type libpqResult int64

func (r libpqResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

func (r libpqResult) LastInsertId() (int64, error) {
	return 0, ErrLastInsertId
}
