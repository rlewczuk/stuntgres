package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func arrayTransformStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE DOMAIN at_stored_leaf AS INT",
		"CREATE TABLE at_store(id INT,a at_stored_leaf[],d INT[] DEFAULT (ARRAY[1] || ARRAY[2]),g INT GENERATED ALWAYS AS (cardinality(array_reverse(a))) STORED,CONSTRAINT at_store_check CHECK (cardinality(trim_array(a,0)) > 0))",
		"CREATE INDEX at_store_idx ON at_store ((cardinality(array_sort(a))))",
		"CREATE VIEW at_stored_view AS SELECT id,array_append(a,3::at_stored_leaf)::int[] AS pushed,array_prepend(0::at_stored_leaf,a)::int[] AS front,array_cat(a,a)::int[] AS doubled,array_fill(7,ARRAY[2]) AS filled,array_reverse(a)::int[] AS reversed,array_sort(a)::int[] AS sorted,trim_array(a,1)::int[] AS trimmed,array_to_string(a,',','*') AS joined,d,g FROM at_store",
		"CREATE VIEW at_named_view AS SELECT array_sort(descending => true,\"array\" => a,nulls_first => false)::int[] AS a FROM at_store",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO at_store(id,a) VALUES(1,'{2,1}')", 1))
	arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT a FROM at_named_view", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,1}"}}))
	return arrayTransformSelect(b, "SELECT id,pushed,front,doubled,filled,reversed,sorted,trimmed,joined,d,g FROM at_stored_view ORDER BY id",
		[]string{"id", "pushed", "front", "doubled", "filled", "reversed", "sorted", "trimmed", "joined", "d", "g"},
		[]string{"INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4", "_INT4", "TEXT", "_INT4", "INT4"},
		[][]any{{int64(1), "{2,1,3}", "{0,2,1}", "{2,1,2,1}", "{7,7}", "{1,2}", "{1,2}", "{2}", "2,1", "{1,2}", int64(2)}})
}

func arrayTransformNativeReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	// Replay precisely the emitted SQL, never an equivalent hand-written view.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL emitted SQL replay", err)
		}
		pgCheck := arrayTransformSelect(fixture.Backend{PG: true}, check.Query, check.Columns, check.Types, check.Rows)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, arrayTransformSelect(fixture.Backend{PG: true}, "SELECT a FROM at_named_view", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,1}"}}))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO at_store VALUES(2,'{3,1}',DEFAULT,DEFAULT)", 1))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT sorted,joined,d,g FROM at_stored_view WHERE id=2", []string{"sorted", "joined", "d", "g"}, []string{"_INT4", "TEXT", "_INT4", "INT4"}, [][]any{{"{1,3}", "3,1", "{1,2}", int64(2)}}))
		// Release objects before the subsequent ordinary PG backend callback.
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE at_one,at_store CASCADE; DROP DOMAIN at_stored_leaf CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	// Existing fixture checks old eager cursor, descriptor remint, independent
	// target allocator advancement, replayed rows and byte-identical re-dumps.
	fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
	before := dump.String()
	bad := "CREATE TABLE at_staged(v INT[]); INSERT INTO at_staged VALUES('{1}'); INSERT INTO at_staged VALUES(trim_array(ARRAY[1],2));"
	d := fixture.ControlsReferenceDiagnostic("2202E", "number of elements to trim must be between 0 and 1")
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
		t.Fatal(err)
	}
	if before != after.String() {
		t.Fatal("failed staged restore changed catalog/data/symbolic expressions")
	}
}

func TestReferenceArrayTransformStoredReplay(t *testing.T) {
	arrayTransformScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		check := arrayTransformStoredSetup(t, p, b, c)
		arrayTransformBoth(t, p, b, c, check)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT CASE WHEN id=1 THEN cardinality(array_append(a,3)) ELSE 0 END AS n,cardinality(COALESCE(array_reverse(a),array_fill(0,ARRAY[1]))) AS m FROM at_store", []string{"n", "m"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(2)}}),
			fixture.Select("SELECT cardinality(array_sort(a)) AS n,count(*) AS c FROM at_store GROUP BY cardinality(pg_catalog.array_sort(a))", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}}),
			fixture.Select("SELECT sum(cardinality(array_reverse(a))) FILTER (WHERE cardinality(trim_array(a,0))=2) AS n FROM at_store", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			fixture.Select("WITH choices AS (SELECT id,array_reverse(a) AS a FROM at_store) SELECT cardinality(array_cat(l.a,r.a)) AS n FROM choices AS l JOIN at_store AS r ON cardinality(array_append(l.a,1))=3", []string{"n"}, []string{"INT4"}, [][]any{{int64(4)}}),
		} {
			arrayTransformBoth(t, p, b, c, s)
		}
		// array_to_string is STABLE even for integer leaves. PostgreSQL
		// rejects these DDL statements at execution, including prepared DDL.
		for _, tc := range []struct{ name, query, message string }{
			{"stable_generated", "CREATE TABLE at_bad_generated(a INT[],s TEXT GENERATED ALWAYS AS (array_to_string(a,',')) STORED)", "generation expression is not immutable"},
			{"stable_index", "CREATE INDEX at_bad_index ON at_store ((array_to_string(a,',')))", "functions in index expression must be marked IMMUTABLE"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arrayTransformDiagnostic(t, p, b, c, tc.query, fixture.Diagnostic{Code: "42P17", Message: tc.message}, false)
			})
		}
		// Rename must preserve resolved selected identities and written coercions.
		run(fixture.Exec("ALTER TABLE at_store RENAME COLUMN a TO items", 0))
		arrayTransformBoth(t, p, b, c, check)
		arrayTransformNativeReplay(t, p, b, c, check)
	})
}
