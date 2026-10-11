package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func queryDistinctConsumerWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("CREATE VIEW dv AS SELECT DISTINCT a,b FROM t", 0))
	s := fixture.QueryDistinctOrdered()
	s.Query = "SELECT a,b FROM dv ORDER BY a ASC NULLS LAST,b DESC NULLS FIRST"
	fixture.QueryDistinctBoth(run, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, s.Query}, b.Recorder, s)
	}
	// pg_get_viewdef has a concrete independently reasoned layout; root must
	// confirm its release presentation, not merely search for a modifier.
	run(fixture.Select("SELECT pg_get_viewdef('dv'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT DISTINCT a,\n    b\n   FROM t;"}}))
	run(fixture.Exec("CREATE TABLE nums(n NUMERIC)", 0))
	run(fixture.Exec("INSERT INTO nums VALUES(1.1),(1.1),(1.2),(1.2)", 4))
	run(fixture.Exec("CREATE TABLE dest(n INTEGER)", 0))
	for _, prepared := range []bool{false, true} {
		insert := fixture.Exec("INSERT INTO dest SELECT DISTINCT n FROM nums", 2)
		insert.Prepared = prepared
		run(insert)
		run(fixture.Select("SELECT n FROM dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		if !prepared {
			run(fixture.Exec("DELETE FROM dest", 2))
		}
	}
	run(fixture.Exec("CREATE TABLE identity_dest(id INT GENERATED ALWAYS AS IDENTITY,n INT)", 0))
	run(fixture.Exec("INSERT INTO identity_dest(id,n) OVERRIDING USER VALUE SELECT DISTINCT id,a FROM t", 7))
	run(fixture.Select("SELECT count(*) AS n FROM identity_dest", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	if !b.PG {
		queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT a FROM t ORDER BY b;\n", "b;")
	}
}

func queryDistinctNativeReplay(t *testing.T, p *fixture.Pair, view fixture.Step, suffix, positionMarker string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/distinct-copy")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	fixture.StringCoreControlExec(t, db, "CREATE TABLE sentinel(id INT)")
	fixture.StringCoreControlExec(t, db, "INSERT INTO sentinel VALUES(17)")
	old, err := db.Query("SELECT id AS retained FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	check := func() {
		rows, err := db.Query("SELECT id FROM sentinel")
		fixture.StringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	}
	invalid := append(append([]byte(nil), dump.Bytes()...), suffix...)
	err = stuntgres.Restore(p.Ctx, db, bytes.NewReader(invalid))
	want := fixture.ControlsReferenceDiagnostic("42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list")
	want.Position = fixture.Position(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(suffix[:strings.LastIndex(suffix, positionMarker)]) + 1)
	fixture.AssertDiagnostic(t, err, &want)
	check()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes()))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check()
	for range 2 {
		if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(view.Query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				rows, err = stmt.Query(view.Args...)
				fixture.StringCoreRetainedRows(t, rows, err, view.Columns, view.Types, view.Rows, view.Bag)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				rows, err = db.Query(view.Query, view.Args...)
				fixture.StringCoreRetainedRows(t, rows, err, view.Columns, view.Types, view.Rows, view.Bag)
			}
		}
	}
	fixture.StringCoreRetainedRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	retained, err := db.Query(view.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.StringCoreRetainedRows(t, retained, nil, view.Columns, view.Types, view.Rows, view.Bag)
}

func TestReferenceQueryDistinctConsumers(t *testing.T) {
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		fixture.QueryDistinctSetup(t, run)
		queryDistinctConsumerWitness(t, p, b, c, run)
		queryDistinctArrayWitness(t, run)
		run(fixture.Exec("CREATE DOMAIN replay_element AS INT", 0))
		run(fixture.Exec("CREATE TABLE replay_arrays(a replay_element[])", 0))
		run(fixture.Exec("INSERT INTO replay_arrays VALUES('{1,NULL}'),('{1,NULL}'),(NULL),(NULL)", 4))
		run(fixture.Exec("CREATE VIEW replay_dv AS SELECT DISTINCT a FROM replay_arrays", 0))
		types := []string{"_REPLAY_ELEMENT"}
		if b.PG {
			types = []string{""}
		}
		s := fixture.ArrayReferenceSelect("SELECT a FROM replay_dv", []string{"a"}, types, []string{"_INT4"}, [][]any{{"{1,NULL}"}, {nil}}, b.PG)
		s.Bag = true
		fixture.QueryDistinctBoth(run, s)
		if !b.PG {
			queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT a FROM t ORDER BY b;\n", "b;")
		}
	})
}
