package libpq

/*
#cgo CFLAGS: -I/usr/include/postgresql/server
#cgo darwin CFLAGS: -I/Applications/Postgres.app/Contents/MacOS/include -I/Applications/Postgres.app/Contents/MacOS/include/server
#cgo darwin LDFLAGS: -L/Applications/Postgres.app/Contents/MacOS/lib
#cgo LDFLAGS: -lpq
*/
import "C"
