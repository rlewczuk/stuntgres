package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
)

func queryDistinctConsumerWitness(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
	t.Helper()
	run(referenceExec("CREATE VIEW dv AS SELECT DISTINCT a,b FROM t", 0))
	s := queryDistinctOrdered()
	s.query = "SELECT a,b FROM dv ORDER BY a ASC NULLS LAST,b DESC NULLS FIRST"
	queryDistinctBoth(run, s)
	stmt, err := c.PrepareContext(p.ctx, s.query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, s.query}, b.recorder, s)
	}
	// pg_get_viewdef has a concrete independently reasoned layout; root must
	// confirm its release presentation, not merely search for a modifier.
	run(referenceSelect("SELECT pg_get_viewdef('dv'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT DISTINCT a,\n    b\n   FROM t;"}}))
	run(referenceExec("CREATE TABLE nums(n NUMERIC)", 0))
	run(referenceExec("INSERT INTO nums VALUES(1.1),(1.1),(1.2),(1.2)", 4))
	run(referenceExec("CREATE TABLE dest(n INTEGER)", 0))
	for _, prepared := range []bool{false, true} {
		insert := referenceExec("INSERT INTO dest SELECT DISTINCT n FROM nums", 2)
		insert.prepared = prepared
		run(insert)
		run(referenceSelect("SELECT n FROM dest ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		if !prepared {
			run(referenceExec("DELETE FROM dest", 2))
		}
	}
	run(referenceExec("CREATE TABLE identity_dest(id INT GENERATED ALWAYS AS IDENTITY,n INT)", 0))
	run(referenceExec("INSERT INTO identity_dest(id,n) OVERRIDING USER VALUE SELECT DISTINCT id,a FROM t", 7))
	run(referenceSelect("SELECT count(*) AS n FROM identity_dest", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
	if !b.pg {
		queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT a FROM t ORDER BY b;\n", "b;")
	}
}

func queryDistinctNativeReplay(t *testing.T, p *referencePair, view referenceStep, suffix, positionMarker string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/distinct-copy")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	stringCoreControlExec(t, db, "CREATE TABLE sentinel(id INT)")
	stringCoreControlExec(t, db, "INSERT INTO sentinel VALUES(17)")
	old, err := db.Query("SELECT id AS retained FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	check := func() {
		rows, err := db.Query("SELECT id FROM sentinel")
		stringCoreRetainedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	}
	invalid := append(append([]byte(nil), dump.Bytes()...), suffix...)
	err = stuntgres.Restore(p.ctx, db, bytes.NewReader(invalid))
	want := controlsReferenceDiagnostic("42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list")
	want.Position = referencePosition(utf8.RuneCount(dump.Bytes()) + utf8.RuneCountInString(suffix[:strings.LastIndex(suffix, positionMarker)]) + 1)
	referenceAssertDiagnostic(t, err, &want)
	check()
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes()))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	check()
	for range 2 {
		if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(view.query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				rows, err = stmt.Query(view.args...)
				stringCoreRetainedRows(t, rows, err, view.columns, view.types, view.rows, view.bag)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				rows, err = db.Query(view.query, view.args...)
				stringCoreRetainedRows(t, rows, err, view.columns, view.types, view.rows, view.bag)
			}
		}
	}
	stringCoreRetainedRows(t, old, nil, []string{"retained"}, []string{"INT4"}, [][]any{{int64(17)}}, false)
	retained, err := db.Query(view.query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	stringCoreRetainedRows(t, retained, nil, view.columns, view.types, view.rows, view.bag)
}

func TestReferenceQueryDistinctConsumers(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctSetup(t, run)
		queryDistinctConsumerWitness(t, p, b, c, run)
		queryDistinctArrayWitness(t, run)
		run(referenceExec("CREATE DOMAIN replay_element AS INT", 0))
		run(referenceExec("CREATE TABLE replay_arrays(a replay_element[])", 0))
		run(referenceExec("INSERT INTO replay_arrays VALUES('{1,NULL}'),('{1,NULL}'),(NULL),(NULL)", 4))
		run(referenceExec("CREATE VIEW replay_dv AS SELECT DISTINCT a FROM replay_arrays", 0))
		types := []string{"_REPLAY_ELEMENT"}
		if b.pg {
			types = []string{""}
		}
		s := arrayReferenceSelect("SELECT a FROM replay_dv", []string{"a"}, types, []string{"_INT4"}, [][]any{{"{1,NULL}"}, {nil}}, b.pg)
		s.bag = true
		queryDistinctBoth(run, s)
		if !b.pg {
			queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT a FROM t ORDER BY b;\n", "b;")
		}
	})
}
