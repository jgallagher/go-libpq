package libpq

/*
#include <c.h>
#include <catalog/pg_type.h>
#include <libpq-fe.h>
#include <stdlib.h>

static char **makeCharArray(int size) {
	return calloc(sizeof(char *), size);
}

static void setArrayString(char **a, char *s, int n) {
	a[n] = s;
}

static void freeArrayElements(int n, char **a) {
	int i;
	for (i = 0; i < n; i++) {
		free(a[i]);
		a[i] = NULL;
	}
}
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
	"time"
	"unsafe"
)

const timeFormat = "2006-01-02 15:04:05.000000-07"

type poolRequest struct {
	nargs int
	resp  chan (**C.char)
}

type poolReturn struct {
	nargs int
	array **C.char
}

type libpqDriver struct {
	argpool     map[int][]**C.char
	poolRequest chan poolRequest
	poolReturn  chan poolReturn
}

var pqdriver *libpqDriver

type libpqConn struct {
	db        *C.PGconn
	stmtCache map[string]driver.Stmt
	stmtNum   int
}

type libpqTx struct {
	c *libpqConn
}

type libpqStmt struct {
	c       *libpqConn
	name    *C.char
	nparams int
}

type libpqResult struct {
	nrows int64 // number of rows affected
}

type libpqRows struct {
	s       *libpqStmt
	res     *C.PGresult
	ncols   int
	nrows   int
	currRow int
	cols    []string
}

func init() {
	pqdriver = &libpqDriver{
		argpool:     make(map[int][]**C.char),
		poolRequest: make(chan poolRequest),
		poolReturn:  make(chan poolReturn),
	}
	go pqdriver.handleArgpool()
	sql.Register("libpq", pqdriver)
}

func connError(db *C.PGconn) error {
	return errors.New("libpq connection error: " + C.GoString(C.PQerrorMessage(db)))
}

func resultError(res *C.PGresult) error {
	status := C.PQresultStatus(res)
	if status == C.PGRES_COMMAND_OK || status == C.PGRES_TUPLES_OK {
		return nil
	}
	return errors.New("libpq result error: " + C.GoString(C.PQresultErrorMessage(res)))
}

func (d *libpqDriver) Open(dsn string) (driver.Conn, error) {
	if C.PQisthreadsafe() != 1 {
		return nil, errors.New("libpq was not compiled for thread-safe operation")
	}

	params := C.CString(dsn)
	defer C.free(unsafe.Pointer(params))

	db := C.PQconnectdb(params)
	if C.PQstatus(db) != C.CONNECTION_OK {
		defer C.PQfinish(db)
		return nil, connError(db)
	}

	return &libpqConn{
		db,
		make(map[string]driver.Stmt),
		0,
	}, nil
}

func (c *libpqConn) Begin() (driver.Tx, error) {
	if err := c.exec("BEGIN"); err != nil {
		return nil, err
	}
	return &libpqTx{c}, nil
}

func (tx *libpqTx) Commit() error {
	return tx.c.exec("COMMIT")
}

func (tx *libpqTx) Rollback() error {
	return tx.c.exec("ROLLBACK")
}

func getNumRows(cres *C.PGresult) (int64, error) {
	rowstr := C.GoString(C.PQcmdTuples(cres))
	if rowstr == "" {
		return 0, nil
	}

	return strconv.ParseInt(rowstr, 10, 64)
}
func (c *libpqConn) exec(cmd string) error {
	ccmd := C.CString(cmd)
	defer C.free(unsafe.Pointer(ccmd))
	res := C.PQexec(c.db, ccmd)
	defer C.PQclear(res)
	return resultError(res)
}

func (c *libpqConn) Close() error {
	C.PQfinish(c.db)
	// free cached statement names
	for _, v := range c.stmtCache {
		if stmt, ok := v.(*libpqStmt); ok {
			C.free(unsafe.Pointer(stmt.name))
		}
	}
	return nil
}

func (c *libpqConn) uniqueStmtName() *C.char {
	name := strconv.Itoa(c.stmtNum)
	c.stmtNum++
	return C.CString(name)
}

func (c *libpqConn) preparedStmtNumInput(cname *C.char) (int, error) {
	cinfo := C.PQdescribePrepared(c.db, cname)
	defer C.PQclear(cinfo)
	if err := resultError(cinfo); err != nil {
		return 0, err
	}
	return int(C.PQnparams(cinfo)), nil
}

func (c *libpqConn) Prepare(query string) (driver.Stmt, error) {
	// check our connection's query cache to see if we've already prepared this
	cached, ok := c.stmtCache[query]
	if ok {
		return cached, nil
	}

	// create unique statement name
	cname := c.uniqueStmtName()
	cquery := C.CString(query)
	defer C.free(unsafe.Pointer(cquery))

	// initial query preparation
	cres := C.PQprepare(c.db, cname, cquery, 0, nil)
	defer C.PQclear(cres)
	if err := resultError(cres); err != nil {
		C.PQclear(cres)
		return nil, err
	}

	nparams, err := c.preparedStmtNumInput(cname)
	if err != nil {
		C.PQclear(cres)
		return nil, err
	}

	// save in cache
	c.stmtCache[query] = &libpqStmt{c: c, name: cname, nparams: nparams}

	return c.stmtCache[query], nil
}

func (s *libpqStmt) Close() error {
	// nothing to do - prepared statement names are cached and will be
	// freed when s's parent libpqConn is closed
	return nil
}

func (s *libpqStmt) NumInput() int {
	return s.nparams
}

func (s *libpqStmt) exec(args []driver.Value) (*C.PGresult, error) {
	// convert args into C array-of-strings
	cargs, err := s.buildCArgs(args)
	if err != nil {
		return nil, err
	}
	defer pqdriver.returnCharArrayToPool(len(args), cargs)

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

	return &libpqResult{nrows}, nil
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

func (s *libpqStmt) buildCArgs(args []driver.Value) (**C.char, error) {
	carray := pqdriver.getCharArrayFromPool(len(args))

	for i, v := range args {
		var str string
		switch v := v.(type) {
		case int64:
			str = strconv.FormatInt(v, 10)
		case float64:
			str = strconv.FormatFloat(v, 'E', -1, 64)
		case bool:
			if v {
				str = "t"
			} else {
				str = "f"
			}
		case []byte:
			str = `\x` + hex.EncodeToString(v)
		case string:
			str = v
		case time.Time:
			str = v.Format(timeFormat)
		case nil:
			str = "NULL"
		default:
			pqdriver.returnCharArrayToPool(len(args), carray)
			return nil, errors.New("unsupported type")
		}

		C.setArrayString(carray, C.CString(str), C.int(i))
	}

	return carray, nil
}

func (r *libpqResult) RowsAffected() (int64, error) {
	return r.nrows, nil
}

func (r *libpqResult) LastInsertId() (int64, error) {
	return 0, errors.New("libpq: LastInsertId() not supported")
}

func (d *libpqDriver) getCharArrayFromPool(nargs int) **C.char {
	ch := make(chan **C.char)
	req := poolRequest{nargs, ch}
	d.poolRequest <- req
	return <-ch
}

func (d *libpqDriver) returnCharArrayToPool(nargs int, array **C.char) {
	C.freeArrayElements(C.int(nargs), array)
	d.poolReturn <- poolReturn{nargs, array}
}

func (d *libpqDriver) handleArgpool() {
	for {
		select {
		case req := <-d.poolReturn:
			list := append(d.argpool[req.nargs], req.array)
			d.argpool[req.nargs] = list

		case req := <-d.poolRequest:
			list := d.argpool[req.nargs]
			if len(list) == 0 {
				list = append(list, C.makeCharArray(C.int(req.nargs)))
			}
			req.resp <- list[0]
			d.argpool[req.nargs] = list[1:]
		}
	}
}
