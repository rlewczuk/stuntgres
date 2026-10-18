package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterRenameCollision(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT)")
	roleExecute(t, e, op, "CREATE TABLE u(a INT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(7,'keep')")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.descriptors[ref.Address]
	storage := e.storage[ref.Address]
	frontier := e.catalog.nextRevision
	for _, tc := range []struct{ q, code, message string }{
		{"ALTER TABLE t RENAME missing TO b", "42703", `column "missing" does not exist`},
		{"ALTER TABLE t RENAME a TO b", "42701", `column "b" of relation "t" already exists`},
		{"ALTER TABLE t RENAME a TO a", "42701", `column "a" of relation "t" already exists`},
		{"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`},
		{"ALTER TABLE t RENAME TO u", "42P07", `relation "u" already exists`},
	} {
		_, err := e.Execute(op, statement(t, tc.q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc, err, got)
		}
		if e.catalog.state.descriptors[ref.Address] != old || e.storage[ref.Address] != storage || e.catalog.nextRevision != frontier {
			t.Fatal("failed rename changed identity/storage/frontier")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testExecute(e, ctx, statement(t, "ALTER TABLE t RENAME TO canceled"), nil)
	if !errors.Is(err, context.Canceled) || e.catalog.state.descriptors[ref.Address] != old {
		t.Fatal(err)
	}
}
