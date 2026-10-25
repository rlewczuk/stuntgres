package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Final scenarios share the incumbent connection, assertion and prepared helpers.
func aggregateFinalScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func aggregateFinalText(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func aggregateFinalSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION fa_final(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'total='||$1::text$$", 0),
		fixture.Exec("CREATE AGGREGATE fa_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_final)", 0),
		fixture.Exec("CREATE TABLE fa_input(id integer,g integer,x integer)", 0),
		fixture.Exec("INSERT INTO fa_input VALUES(1,1,1),(2,1,2),(3,2,3)", 3))
}

func aggregateFinalMetadata(pg, stored bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		column := columns[len(columns)-1]
		if column.ScanType() != reflect.TypeOf("") {
			t.Fatalf("scan type=%v", column.ScanType())
		}
		if length, known := column.Length(); length != math.MaxInt64 || !known {
			t.Fatalf("length=%d/%v want MaxInt64/true", length, known)
		}
		if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("decimal=%d/%d/%v", precision, scale, known)
		}
		nullable, known := column.Nullable()
		want := stored && !pg
		if nullable != want || known != want {
			t.Fatalf("nullable=%v/%v want %v/%v", nullable, known, want, want)
		}
	}
}

func TestReferenceAggregateFinalValues(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateFinalSetup(t, p, b, c)
		for _, q := range []string{
			"SELECT fa_sum(x) AS v FROM fa_input",
			"SELECT fa_sum(DISTINCT x ORDER BY x) AS v FROM fa_input",
			"SELECT fa_sum(x ORDER BY id DESC) FILTER(WHERE x>0) AS v FROM fa_input",
		} {
			s := aggregateFinalText(q, "total=6")
			s.CheckMetadata = aggregateFinalMetadata(b.PG, false)
			createAggregateBoth(t, p, b, c, s)
		}
		for _, q := range []string{"SELECT fa_sum(x) AS v FROM fa_input WHERE FALSE", "SELECT fa_sum(x) FILTER(WHERE FALSE) AS v FROM fa_input"} {
			createAggregateBoth(t, p, b, c, aggregateFinalText(q, "total=0"))
		}
		const q = "SELECT fa_sum(x) AS v FROM fa_input WHERE id<=$1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			limit int64
			value string
		}{{3, "total=6"}, {0, "total=0"}, {1, "total=1"}, {3, "total=6"}} {
			s := aggregateFinalText(q, tc.value, tc.limit)
			s.CheckMetadata = aggregateFinalMetadata(b.PG, false)
			createAggregateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,fa_sum(x) AS v FROM fa_input GROUP BY g HAVING fa_sum(x)='total=3' ORDER BY g", []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "total=3"}, {int64(2), "total=3"}}))
		empty := fixture.Select("SELECT g,fa_sum(x) AS v FROM fa_input WHERE FALSE GROUP BY g", []string{"g", "v"}, []string{"INT4", "TEXT"}, nil)
		empty.CheckMetadata = aggregateFinalMetadata(b.PG, false)
		createAggregateBoth(t, p, b, c, empty)
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE VIEW fa_view AS SELECT fa_sum(x) AS v FROM fa_input", 0),
			fixture.Exec("CREATE FUNCTION fa_total() RETURNS text BEGIN ATOMIC SELECT fa_sum(x) FROM fa_input; END", 0),
			fixture.Exec("CREATE TABLE fa_destination(g integer,v text)", 0),
			fixture.Exec("INSERT INTO fa_destination SELECT g,fa_sum(x) FROM fa_input GROUP BY g", 2))
		view := aggregateFinalText("SELECT v FROM fa_view", "total=6")
		view.CheckMetadata = aggregateFinalMetadata(b.PG, true)
		createAggregateBoth(t, p, b, c, view)
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_total() AS v", "total=6"))
		createAggregateBoth(t, p, b, c, aggregateFinalText("WITH totals AS(SELECT fa_sum(x) AS v FROM fa_input) SELECT v FROM totals", "total=6"))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,v FROM fa_destination ORDER BY g", []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "total=3"}, {int64(2), "total=3"}}))
	})
}

func TestReferenceAggregateFinalNulls(t *testing.T) {
	aggregateFinalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE FUNCTION fa_empty(integer) RETURNS text LANGUAGE SQL AS $$SELECT coalesce($1::text,'empty')$$", 0),
			fixture.Exec("CREATE FUNCTION fa_null(integer) RETURNS text LANGUAGE SQL AS $$SELECT NULL::text$$", 0),
			fixture.Exec("CREATE AGGREGATE fa_nonstrict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=fa_empty)", 0),
			fixture.Exec("CREATE AGGREGATE fa_strict(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=pg_catalog.abs)", 0),
			fixture.Exec("CREATE AGGREGATE fa_seed(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='-7',FINALFUNC=pg_catalog.abs)", 0),
			fixture.Exec("CREATE AGGREGATE fa_returns_null(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,INITCOND='0',FINALFUNC=fa_null)", 0))
		for _, source := range []string{"(VALUES(NULL::integer),(NULL::integer)) s(x)", "(VALUES(1)) s(x) WHERE FALSE"} {
			createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_nonstrict(x) AS v FROM "+source, "empty"))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT fa_strict(x) AS v FROM "+source, nil))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT fa_seed(x) AS v FROM "+source, int64(7)))
		}
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_nonstrict(x) AS v FROM(VALUES(NULL::int),(1),(2),(3)) s(x)", "6"))
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_returns_null(x) AS v FROM(VALUES(1),(2)) s(x)", nil))
		createAggregateRun(t, p, b, c,
			fixture.Exec("CREATE TABLE fa_final_calls(v integer)", 0),
			fixture.Exec("CREATE FUNCTION fa_record(integer) RETURNS text BEGIN ATOMIC INSERT INTO fa_final_calls VALUES($1); SELECT coalesce($1::text,'empty'); END", 0),
			fixture.Exec("CREATE AGGREGATE fa_recorded(integer) (SFUNC=pg_catalog.int4pl,STYPE=int4,FINALFUNC=fa_record)", 0))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT x,fa_recorded(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE GROUP BY x", []string{"x", "v"}, []string{"INT4", "TEXT"}, nil))
		createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM fa_final_calls", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		createAggregateBoth(t, p, b, c, aggregateFinalText("SELECT fa_recorded(x) AS v FROM(VALUES(1)) s(x) WHERE FALSE", "empty"))
		createAggregateRun(t, p, b, c,
			fixture.Select("SELECT v FROM fa_final_calls", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {nil}}),
			fixture.Exec("DELETE FROM fa_final_calls", 2))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT g,fa_recorded(x) AS v FROM(VALUES(1,1),(1,2),(2,4),(2,5)) s(g,x) GROUP BY g ORDER BY g", []string{"g", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "3"}, {int64(2), "9"}}))
		createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM fa_final_calls ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(9)}, {int64(9)}}))
	})
}
