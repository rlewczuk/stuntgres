package tablecmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestAlterRenameCollision(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE u(a INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(7,'keep')")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.Get(ref.Address)
	storage := e.Storage.Relations[ref.Address]
	frontier := e.Catalog.NextRevision()
	for _, tc := range []struct{ q, code, message string }{
		{"ALTER TABLE t RENAME missing TO b", "42703", `column "missing" does not exist`},
		{"ALTER TABLE t RENAME a TO b", "42701", `column "b" of relation "t" already exists`},
		{"ALTER TABLE t RENAME a TO a", "42701", `column "a" of relation "t" already exists`},
		{"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`},
		{"ALTER TABLE t RENAME TO u", "42P07", `relation "u" already exists`},
	} {
		_, err := e.Execute(op, enginetest.Statement(t, tc.q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc, err, got)
		}
		if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage || e.Catalog.NextRevision() != frontier {
			t.Fatal("failed rename changed identity/storage/frontier")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER TABLE t RENAME TO canceled"), nil)
	if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old {
		t.Fatal(err)
	}
}
