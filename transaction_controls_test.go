package stuntgres_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
)

func controlsDiagnostic(code, message string) stuntgres.Error {
	return stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func controlsAborted() stuntgres.Error {
	return controlsDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
}
func controlsNull() stuntgres.Error {
	return stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "v"}
}
func controlsFixture(t *testing.T) (*sql.DB, *sql.Conn, *sql.Conn, *[]stuntgres.Error) {
	t.Helper()
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	notices := new([]stuntgres.Error)
	db := sql.OpenDB(connector.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { *notices = append(*notices, n) }))
	t.Cleanup(func() { db.Close() })
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.ExecContext(context.Background(), "ROLLBACK"); a.Close(); b.Close() })
	transactionAffected(t, context.Background(), b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	return db, a, b, notices
}
