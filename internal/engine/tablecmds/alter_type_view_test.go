package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestAlterTypeUnreferencedViewSourceUsage(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,7)")
	enginetest.Run(t, e, "CREATE VIEW v AS SELECT b FROM t")
	viewRef := enginetest.RoleTableRef(t, e, "v")
	old := e.Catalog.State.Get(viewRef.Address)
	enginetest.Run(t, e, "ALTER TABLE t ALTER a TYPE TEXT USING a")
	if e.Catalog.State.Get(viewRef.Address) != old {
		t.Fatal("rewrote unrelated view definition")
	}
	if rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT b FROM v")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(7)}}) {
		t.Fatal(rows)
	}
	if enginetest.Run(t, e, "SELECT b FROM v").Rows.Schema()[0].Usage.Type.ID() != 23 {
		t.Fatal("changed view result type")
	}
}

func TestAlterTypeQuotedViewIdentityCollationAndCascade(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE SCHEMA s")
	enginetest.Run(t, e, "CREATE TABLE t(c VARCHAR(5),discard INT,keep INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES('seven',1,9)")
	enginetest.Run(t, e, `CREATE VIEW s."Odd View" AS SELECT c,discard FROM public.t`)
	enginetest.Run(t, e, "CREATE VIEW v_keep AS SELECT keep FROM t")
	survivor := enginetest.RoleTableRef(t, e, "v_keep")
	survivorDefinition := e.Catalog.State.Get(survivor.Address)
	ref := enginetest.RoleTableRef(t, e, "t")
	old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot alter type of a column used by a view or rule", Detail: `rule _RETURN on view s."Odd View" depends on column "c"`}
	for _, action := range []string{"TYPE VARCHAR(5)", `TYPE VARCHAR(5) COLLATE "C"`, `TYPE VARCHAR(5) COLLATE "default"`} {
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE public.t ALTER c "+action), nil)
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
			t.Fatal(got, err)
		}
		if rows := enginetest.Tuples(enginetest.Run(t, e, `SELECT * FROM s."Odd View"`)); !reflect.DeepEqual(rows, [][]driver.Value{{"seven", int64(1)}}) {
			t.Fatal(rows)
		}
	}
	enginetest.Run(t, e, "ALTER TABLE t ALTER c TYPE TEXT, DROP discard CASCADE")
	if rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{"seven", int64(9)}}) {
		t.Fatal(rows)
	}
	if schema := enginetest.Run(t, e, "SELECT * FROM t").Rows.Schema(); len(schema) != 2 || schema[0].Usage.Type.ID() != 25 || schema[0].Usage.Collation.ID != 100 || schema[1].OriginAttribute != 3 {
		t.Fatal(schema)
	}
	if e.Catalog.State.Get(survivor.Address) != survivorDefinition {
		t.Fatal("removed or rewrote unrelated view")
	}
	if rows := enginetest.Tuples(enginetest.Run(t, e, "SELECT keep FROM v_keep")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(9)}}) {
		t.Fatal(rows)
	}
}
