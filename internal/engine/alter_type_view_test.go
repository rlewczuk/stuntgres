package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterTypeUnreferencedViewSourceUsage(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b INT)")
	run(t, e, "INSERT INTO t VALUES(1,7)")
	run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	viewRef := roleTableRef(t, e, "v")
	old := e.catalog.state.Get(viewRef.Address)
	run(t, e, "ALTER TABLE t ALTER a TYPE TEXT USING a")
	if e.catalog.state.Get(viewRef.Address) != old {
		t.Fatal("rewrote unrelated view definition")
	}
	if rows := tuples(run(t, e, "SELECT b FROM v")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(7)}}) {
		t.Fatal(rows)
	}
	if run(t, e, "SELECT b FROM v").Rows.Schema()[0].Usage.Type.ID() != 23 {
		t.Fatal("changed view result type")
	}
}

func TestAlterTypeQuotedViewIdentityCollationAndCascade(t *testing.T) {
	e := New()
	run(t, e, "CREATE SCHEMA s")
	run(t, e, "CREATE TABLE t(c VARCHAR(5),discard INT,keep INT)")
	run(t, e, "INSERT INTO t VALUES('seven',1,9)")
	run(t, e, `CREATE VIEW s."Odd View" AS SELECT c,discard FROM public.t`)
	run(t, e, "CREATE VIEW v_keep AS SELECT keep FROM t")
	survivor := roleTableRef(t, e, "v_keep")
	survivorDefinition := e.catalog.state.Get(survivor.Address)
	ref := roleTableRef(t, e, "t")
	old, storage := e.catalog.state.Get(ref.Address), e.storage.relations[ref.Address]
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view s."Odd View" depends on column "c"`}
	for _, action := range []string{"TYPE VARCHAR(5)", `TYPE VARCHAR(5) COLLATE "C"`, `TYPE VARCHAR(5) COLLATE "default"`} {
		_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE public.t ALTER c "+action), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || e.catalog.state.Get(ref.Address) != old || e.storage.relations[ref.Address] != storage {
			t.Fatal(got, err)
		}
		if rows := tuples(run(t, e, `SELECT * FROM s."Odd View"`)); !reflect.DeepEqual(rows, [][]driver.Value{{"seven", int64(1)}}) {
			t.Fatal(rows)
		}
	}
	run(t, e, "ALTER TABLE t ALTER c TYPE TEXT, DROP discard CASCADE")
	if rows := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{"seven", int64(9)}}) {
		t.Fatal(rows)
	}
	if schema := run(t, e, "SELECT * FROM t").Rows.Schema(); len(schema) != 2 || schema[0].Usage.Type.ID() != 25 || schema[0].Usage.Collation.ID != 100 || schema[1].OriginAttribute != 3 {
		t.Fatal(schema)
	}
	if e.catalog.state.Get(survivor.Address) != survivorDefinition {
		t.Fatal("removed or rewrote unrelated view")
	}
	if rows := tuples(run(t, e, "SELECT keep FROM v_keep")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(9)}}) {
		t.Fatal(rows)
	}
}
