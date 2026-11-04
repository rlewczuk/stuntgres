package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func sepgsqlInitializationTextMetadata() func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal("text result metadata", columns, err)
		}
		column := columns[0]
		if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() {
			t.Fatal("text result type", column.DatabaseTypeName(), column.ScanType())
		}
		nullable, known := column.Nullable()
		if nullable || known {
			t.Fatal("computed expression nullability", nullable, known)
		}
	}
}

func sepgsqlInitializationOrdinary(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(step fixture.Step) {
		defer func() {
			if t.Failed() {
				t.Logf("sepgsql initialization statement: %s", step.Query)
			}
		}()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	for _, query := range []string{
		"CREATE FUNCTION init_o_identity(v text) RETURNS text LANGUAGE sql VOLATILE STRICT AS 'SELECT v'",
		// REL_18_6 clauses.c:inline_function rejects non-NULL proconfig.
		// Keep this an actual SQL executor-error/unwind witness, rather than
		// depending on optimizer inlining or copying a backend's error context.
		"CREATE FUNCTION init_o_sql(v integer) RETURNS integer LANGUAGE sql VOLATILE SET search_path=public AS 'SELECT 12 / v'",
		"CREATE FUNCTION init_o_pl(v integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN init_o_sql(v); END$$",
		"CREATE FUNCTION init_o_caught(v integer) RETURNS text LANGUAGE plpgsql AS $$BEGIN BEGIN PERFORM init_o_sql(v); EXCEPTION WHEN division_by_zero THEN RETURN 'caught'; END; RETURN 'success'; END$$",
		"CREATE TABLE init_o_input(id integer,v text,n numeric)",
	} {
		run(fixture.Exec(query, 0))
	}
	run(fixture.Exec("INSERT INTO init_o_input VALUES(1,'alpha',3.14),(2,NULL,NULL),(3,'gamma',5.6)", 3))
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT init_o_identity($1::text) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"argument"}}, "argument")
		step.Prepared, step.CheckMetadata = prepared, sepgsqlInitializationTextMetadata()
		run(step)
		step = fixture.Select("SELECT id,init_o_identity(v) AS value FROM init_o_input ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), nil}, {int64(3), "gamma"}})
		step.Prepared = prepared
		run(step)
		for _, name := range []string{"init_o_sql", "init_o_pl"} {
			step = fixture.Select("SELECT "+name+"($1) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(4)}}, int64(3))
			step.Prepared = prepared
			run(step)
		}
		step = fixture.ImplicitError("SELECT init_o_sql($1)", fixture.Diagnostic{Code: "22012", Message: "division by zero", Context: "SQL function \"init_o_sql\" statement 1"}, int64(0))
		step.Prepared, step.SelectRows = prepared, true
		run(step)
		step = sepgsqlText("SELECT init_o_caught(0) AS value", "value", "caught")
		step.Prepared = prepared
		run(step)
		run(sepgsqlText("SELECT init_o_caught(3) AS value", "value", "success"))
	}
	const query = "SELECT init_o_identity($1::text) AS value"
	held := fixture.IdentityHeld(t, p.Ctx, c, query)
	for _, value := range []any{"first", nil, "last"} {
		step := fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{value}}, value)
		step.CheckMetadata = sepgsqlInitializationTextMetadata()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
	}
	run(fixture.Exec("CREATE EXTENSION hstore", 0))
	vpc := fixture.Select(`SELECT key,value FROM each('a=>one,b=>NULL'::hstore)`, []string{"key", "value"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "one"}, {"b", nil}})
	vpc.Bag = true
	run(vpc)
	run(fixture.Prepared(vpc))

	for _, query := range []string{
		"CREATE FUNCTION init_o_default() RETURNS text LANGUAGE sql VOLATILE AS $$SELECT 'ordinary'::text$$",
		"CREATE FUNCTION init_o_domain(v text) RETURNS boolean LANGUAGE sql VOLATILE STRICT AS 'SELECT char_length(v)>0'",
		"CREATE DOMAIN init_o_guarded AS text CONSTRAINT init_o_nonempty CHECK(init_o_domain(VALUE))",
		"CREATE TABLE init_o_attached(id integer,v init_o_guarded DEFAULT init_o_default())",
		"CREATE FUNCTION init_o_key(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS 'SELECT upper(v)'",
		"CREATE FUNCTION init_o_pred(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS 'SELECT char_length(v)>0'",
		"CREATE INDEX init_o_index ON init_o_attached(init_o_key(v::text)) WHERE init_o_pred(v::text)",
		"CREATE FUNCTION init_o_when(v text) RETURNS boolean LANGUAGE sql VOLATILE AS 'SELECT v IS NOT NULL'",
		"CREATE FUNCTION init_o_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v || '!'; RETURN NEW; END$$",
		"CREATE TRIGGER init_o_before BEFORE INSERT ON init_o_attached FOR EACH ROW WHEN(init_o_when(NEW.v::text)) EXECUTE FUNCTION init_o_trigger()",
	} {
		run(fixture.Exec(query, 0))
	}
	const insert = "INSERT INTO init_o_attached(id) VALUES($1) RETURNING id,v::text AS value"
	insertHeld := fixture.IdentityHeld(t, p.Ctx, c, insert)
	for id := int64(1); id <= 3; id++ {
		step := fixture.Select(insert, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{id, "ordinary!"}}, id)
		if id == 1 {
			run(step)
		} else if id == 2 {
			run(fixture.Prepared(step))
		} else {
			fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, step)
		}
	}
	for _, prepared := range []bool{false, true} {
		bad := fixture.ImplicitError("INSERT INTO init_o_attached VALUES($1,'')", fixture.Diagnostic{Code: "23514", Message: `value for domain init_o_guarded violates check constraint "init_o_nonempty"`, Schema: "public", DataType: "init_o_guarded", Constraint: "init_o_nonempty"}, int64(4))
		bad.Prepared = prepared
		run(bad)
	}
	run(sepgsqlCount("SELECT count(*) FROM init_o_attached", 3))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE init_o_cursor NO SCROLL CURSOR FOR SELECT id,init_o_key(v::text) AS value FROM init_o_attached ORDER BY id", 0))
	run(fixture.Select("FETCH FORWARD 1 FROM init_o_cursor", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "ORDINARY!"}}))
	run(fixture.Select("FETCH ALL FROM init_o_cursor", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "ORDINARY!"}, {int64(3), "ORDINARY!"}}))
	run(fixture.Exec("CLOSE init_o_cursor", 0))
	run(fixture.Exec("COMMIT", 0))

	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT id,(round(n))::text AS value FROM init_o_input ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "3"}, {int64(2), nil}, {int64(3), "6"}})
		step.Prepared = prepared
		run(step)
		missing := fixture.Select("SELECT (round(n))::text AS value FROM init_o_input WHERE n IS NULL", []string{"value"}, []string{"TEXT"}, [][]any{{nil}})
		missing.Prepared, missing.CheckMetadata = prepared, sepgsqlInitializationTextMetadata()
		run(missing)
	}
	sepgsqlInitializationArithmetic(run, false)
	run(fixture.Exec("CREATE FUNCTION init_o_wait() RETURNS void LANGUAGE sql VOLATILE SET search_path=public AS 'SELECT pg_sleep(30)'", 0))
	const wait = "SELECT init_o_wait()"
	waitHeld := fixture.IdentityHeld(t, p.Ctx, c, wait)
	for _, prepared := range []bool{false, true} {
		fixture.SleepActiveCancelWithContext(t, p, b, c, wait, "SQL function \"init_o_wait\" statement 1", nil, func(ctx context.Context) fixture.RelationOutcome {
			var result sql.Result
			var err error
			if prepared {
				result, err = waitHeld.Statement.ExecContext(ctx)
			} else {
				result, err = c.ExecContext(ctx, wait)
			}
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(sepgsqlText("SELECT init_o_caught(0) AS value", "value", "caught"))
	}
}

func sepgsqlInitializationArithmetic(run sepgsqlRun, simulation bool) {
	run(fixture.Exec("CREATE TABLE init_arithmetic(id integer)", 0))
	run(fixture.Exec("INSERT INTO init_arithmetic SELECT generate_series(1,4096)", 4096))
	if simulation {
		run(sepgsqlSet(sepgsqlR))
	}
	for _, prepared := range []bool{false, true} {
		// A real ordinary arithmetic/aggregate workload, not a fake worker
		// counter. Internal owner tests separately prove actual fanout remains.
		step := fixture.Select("SELECT sum(id+1) AS total,count(*) AS rows FROM init_arithmetic", []string{"total", "rows"}, []string{"INT8", "INT8"}, [][]any{{int64(8394752), int64(4096)}})
		step.Prepared = prepared
		run(step)
	}
	if simulation {
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
	}
}
