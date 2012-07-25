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

TODO: Example

## Testing

Describe how to test.
Add to go-sql-test?

## Thanks

Initial work was based heavily on this driver:

https://github.com/andrewzeneski/gopgsqldriver
