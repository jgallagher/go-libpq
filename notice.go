package libpq

/*
#include <stdlib.h>
#include <libpq-fe.h>

extern void go_callback_log(char * message);

static void notice(void * arg, char * message)
{
    go_callback_log(message);
}

static void setNotice(PGconn *conn)
{
    // we need that cast to suppress issue with const qualifier
    // that is not supported by cgo
    PQsetNoticeProcessor(conn, (PQnoticeProcessor)notice,NULL);
}

*/
import "C"

func redirectOutput(db *C.PGconn) {
	if logger != nil {
		C.setNotice(db)
	}
}

type Logger func(message string)

var logger Logger

func SetLogger(l Logger) {
	if l == nil {
		panic("can't set empty logger")
	}
	logger = l
}

//export go_callback_log
func go_callback_log(message *C.char) {
	// this is possible only if logger is not null
	logger(C.GoString(message))
}
