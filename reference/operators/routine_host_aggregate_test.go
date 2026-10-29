package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The logical oracle executes authentic PL/Python; native executes the shared
// registered compiled Go programs. Neither branch substitutes sum()+5 for the
// actual transition/final aggregate invocation.
func TestReferenceRoutineHostAggregateSupport(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"host_agg_owner", "host_agg_reader"})
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return d.RegisterHostLanguage(hostPrimitiveDefinition())
	})
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer routineLanguagesCleanup(t, c, "DROP EXTENSION IF EXISTS plpython3u CASCADE")()
			if b.PG {
				hostExec(t, p, b, c, "CREATE EXTENSION plpython3u")
			}
			hostPrimitiveFunction(t, p, b, c, "host_agg_final", "v bigint", "bigint", "int8-offset-v1", "return v + 5", "STRICT")
			hostPrimitiveFunction(t, p, b, c, "host_agg_transition", "s bigint,v bigint", "bigint", "int8-add-v1", "return s + v", "STRICT")
			for _, q := range []string{
				"CREATE TABLE host_agg_input(v bigint)",
				"CREATE AGGREGATE host_agg_sum(bigint) (SFUNC=pg_catalog.int8pl,STYPE=bigint,FINALFUNC=host_agg_final,INITCOND='0')",
				"CREATE AGGREGATE host_agg_compiled(bigint) (SFUNC=host_agg_transition,STYPE=bigint,FINALFUNC=host_agg_final,INITCOND='0')",
				"CREATE AGGREGATE host_agg_unseeded(bigint) (SFUNC=host_agg_transition,STYPE=bigint,FINALFUNC=host_agg_final)",
				"CREATE FUNCTION host_agg_sql_transition(bigint,bigint) RETURNS bigint LANGUAGE SQL STRICT AS 'SELECT $1+$2'",
				"CREATE FUNCTION host_agg_sql_final(bigint) RETURNS bigint LANGUAGE SQL STRICT AS 'SELECT $1+5'",
				"CREATE AGGREGATE host_agg_sql(bigint) (SFUNC=host_agg_sql_transition,STYPE=bigint,FINALFUNC=host_agg_sql_final,INITCOND='0')",
			} {
				hostExec(t, p, b, c, q)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_agg_input VALUES(1),(2),(3),(NULL)", 4))
			hostAggregateValues(t, p, b, c)
			hostAggregateCatalog(t, p, b, c)
			hostAggregateRestrictions(t, p, b, c)
			hostAggregateAuthority(t, p, b, c, true)
			hostAggregateRollback(t, p, b, c)
			if !b.PG {
				var stream bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
					t.Fatal(err)
				}
				for round := range 2 {
					definition := hostPrimitiveDefinition()
					raw, replay := hostOpenNative(t, &definition)
					// Dump intentionally excludes roles. Provision those referenced by
					// the genuine aggregate ownership and ACLs before atomic Restore.
					for _, role := range []string{"host_agg_owner", "host_agg_reader"} {
						if _, err := raw.ExecContext(p.Ctx, "CREATE ROLE "+role); err != nil {
							t.Fatal(err)
						}
					}
					if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(stream.Bytes())); err != nil {
						t.Fatalf("aggregate fresh-Driver restore round %d: %v", round, err)
					}
					current, err := replay.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					hostAggregateValues(t, p, replay, current)
					hostAggregateCatalog(t, p, replay, current)
					hostAggregateAuthority(t, p, replay, current, false)
					current.Close()
					var next bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, raw, &next); err != nil {
						t.Fatal(err)
					}
					stream.Reset()
					stream.Write(next.Bytes())
				}
			}
		})
	}
}

func hostAggregateValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, name := range []string{"host_agg_sum", "host_agg_compiled", "host_agg_sql", "host_agg_unseeded"} {
		q := "SELECT " + name + "(v) AS v FROM host_agg_input WHERE v <= $1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			limit int64
			want  any
		}{{3, int64(11)}, {2, int64(8)}, {0, int64(5)}, {3, int64(11)}} {
			if name == "host_agg_unseeded" && tc.limit == 0 {
				tc.want = nil
			}
			for _, executor := range []fixture.Executor{c, held} {
				hostRows(t, p, b, executor, q, []string{"v"}, []string{"INT8"}, [][]any{{tc.want}}, tc.limit)
			}
		}
		stmt.Close()
		want := any(int64(5))
		if name == "host_agg_unseeded" {
			want = nil
		}
		hostRows(t, p, b, c, "SELECT "+name+"(v) AS v FROM host_agg_input WHERE v IS NULL", []string{"v"}, []string{"INT8"}, [][]any{{want}})
		for _, suffix := range []string{"", " HAVING false"} {
			hostAggregateMetadata(t, p, c, "SELECT "+name+"(v) AS v FROM host_agg_input"+suffix)
		}
	}
}

func hostAggregateMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatalf("aggregate columns=%v err=%v", columns, err)
	}
	col := columns[0]
	check := func() {
		if col.Name() != "v" || col.DatabaseTypeName() != "INT8" || col.ScanType() != reflect.TypeFor[int64]() {
			t.Errorf("aggregate metadata=%q/%q/%v", col.Name(), col.DatabaseTypeName(), col.ScanType())
		}
		if n, ok := col.Nullable(); n || ok {
			t.Errorf("aggregate nullable=%v/%v", n, ok)
		}
		if n, ok := col.Length(); n != 0 || ok {
			t.Errorf("aggregate length=%v/%v", n, ok)
		}
		if n, s, ok := col.DecimalSize(); n != 0 || s != 0 || ok {
			t.Errorf("aggregate decimal=%v/%v/%v", n, s, ok)
		}
	}
	check()
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != int64(11) {
			t.Errorf("raw aggregate=%#v (%T), want int64(11)", value, value)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check() // Retained metadata remains exact after cursor close.
}

func hostAggregateCatalog(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hostRows(t, p, b, c, "SELECT a.aggtransfn='host_agg_transition(bigint,bigint)'::regprocedure AS transition,a.aggfinalfn='host_agg_final(bigint)'::regprocedure AS final,a.aggtranstype=20::oid AS state,p.prorettype=20::oid AS result FROM pg_aggregate a JOIN pg_proc p ON p.oid=a.aggfnoid WHERE p.proname='host_agg_compiled'", []string{"transition", "final", "state", "result"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}})
	for _, ref := range []string{"host_agg_transition(bigint,bigint)", "host_agg_final(bigint)"} {
		hostRows(t, p, b, c, "SELECT d.deptype::text AS kind FROM pg_depend d WHERE d.classid='pg_proc'::regclass AND d.objid='host_agg_compiled(bigint)'::regprocedure AND d.refclassid='pg_proc'::regclass AND d.refobjid='"+ref+"'::regprocedure", []string{"kind"}, []string{"TEXT"}, [][]any{{"n"}})
	}
}

func hostAggregateAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, setup bool) {
	t.Helper()
	if setup {
		for _, q := range []string{
			"CREATE ROLE host_agg_owner", "CREATE ROLE host_agg_reader",
			"GRANT USAGE,CREATE ON SCHEMA public TO host_agg_owner", "GRANT USAGE ON SCHEMA public TO host_agg_reader",
			"GRANT SELECT ON host_agg_input TO host_agg_reader",
			"REVOKE EXECUTE ON FUNCTION host_agg_final(bigint),host_agg_transition(bigint,bigint) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION host_agg_final(bigint),host_agg_transition(bigint,bigint) TO host_agg_owner",
			"SET SESSION AUTHORIZATION host_agg_owner",
			"CREATE AGGREGATE host_agg_owned(bigint) (SFUNC=host_agg_transition,STYPE=bigint,FINALFUNC=host_agg_final,INITCOND='0')",
			"REVOKE EXECUTE ON FUNCTION host_agg_owned(bigint) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION host_agg_owned(bigint) TO host_agg_reader", "RESET SESSION AUTHORIZATION",
		} {
			hostExec(t, p, b, c, q)
		}
	}
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_agg_reader")
	const q = "SELECT host_agg_owned(v) AS v FROM host_agg_input WHERE v <= $1"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	check := func() {
		for _, tc := range []struct{ limit, want int64 }{{3, 11}, {2, 8}, {0, 5}, {3, 11}} {
			for _, ex := range []fixture.Executor{c, held} {
				hostRows(t, p, b, ex, q, []string{"v"}, []string{"INT8"}, [][]any{{tc.want}}, tc.limit)
			}
		}
	}
	check() // Reader cannot call support directly: aggregate owner's EXECUTE is used.
	hostError(t, p, c, "SELECT host_agg_final(6::bigint)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function host_agg_final"))
	for _, support := range []string{"host_agg_final(bigint)", "host_agg_transition(bigint,bigint)"} {
		hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		hostExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+support+" FROM host_agg_owner")
		hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_agg_reader")
		name := strings.Split(support, "(")[0]
		for _, limit := range []int64{3, 0} {
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + name}, limit)
			failure.SelectRows, failure.ErrorStage = true, "execute"
			for _, ex := range []fixture.Executor{c, held} {
				fixture.RunStep(t, p.Ctx, ex, b.Recorder, failure)
			}
		}
		hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		hostExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION "+support+" TO host_agg_owner")
		hostExec(t, p, b, c, "SET SESSION AUTHORIZATION host_agg_reader")
		check() // The exact held handle recovers after current support authority returns.
	}
	hostExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
}

func hostAggregateRollback(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE host_agg_prefix(v bigint)",
		"CREATE TABLE host_agg_baseline(v bigint)",
		"CREATE FUNCTION host_agg_attempt(v bigint) RETURNS bigint LANGUAGE SQL VOLATILE SET application_name='host aggregate body' BEGIN ATOMIC INSERT INTO host_agg_prefix VALUES(v); SELECT host_agg_compiled(x) FROM (VALUES(v)) s(x); END",
		"SET application_name='host aggregate outside'",
	} {
		hostExec(t, p, b, c, q)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO host_agg_baseline VALUES(79)", 1))
	const q = "SELECT host_agg_attempt($1::bigint) AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, prepared := range []bool{false, true} {
		var value any
		if prepared {
			err = stmt.QueryRowContext(p.Ctx, int64(9223372036854775807)).Scan(&value)
		} else {
			err = c.QueryRowContext(p.Ctx, q, int64(9223372036854775807)).Scan(&value)
		}
		if err == nil {
			t.Fatal("overflowing compiled aggregate final unexpectedly succeeded")
		}
		if b.PG {
			want := fixture.ControlsReferenceDiagnostic("22003", `value "9223372036854775812" is out of range for type bigint`)
			want.Context = "while creating return value\nPL/Python function \"host_agg_final\"\nSQL function \"host_agg_attempt\" statement 2"
			if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Errorf("aggregate overflow execute diagnostic=%#v want=%#v", got, want)
			}
		} else {
			if !strings.Contains(err.Error(), "primitive: int8 overflow") {
				t.Errorf("compiled overflow=%v", err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Errorf("Go overflow fabricated SQLSTATE: %v", err)
			}
		}
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM host_agg_prefix", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostRows(t, p, b, c, "SELECT v FROM host_agg_baseline", []string{"v"}, []string{"INT8"}, [][]any{{int64(79)}})
		hostRows(t, p, b, c, "SELECT current_user=session_user AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"BOOL", "TEXT"}, [][]any{{true, "host aggregate outside"}})
		hostRows(t, p, b, held, q, []string{"v"}, []string{"INT8"}, [][]any{{int64(11)}}, int64(6))
		hostRows(t, p, b, c, "SELECT v FROM host_agg_prefix", []string{"v"}, []string{"INT8"}, [][]any{{int64(6)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM host_agg_prefix", 1))
	}
}

func hostAggregateRestrictions(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, code, message, detail string }{
		{"CREATE AGGREGATE host_agg_rejected(bigint) (SFUNC=host_agg_transition,STYPE=bigint,FINALFUNC=host_agg_transition,FINALFUNC_EXTRA)", "42P13", "final function with extra arguments must not be declared STRICT", ""},
		{"CREATE AGGREGATE host_agg_rejected(text,text) (SFUNC=pg_catalog.string_agg_transfn,STYPE=internal,COMBINEFUNC=pg_catalog.string_agg_combine,SERIALFUNC=pg_catalog.string_agg_serialize,DESERIALFUNC=pg_catalog.string_agg_deserialize)", "42P13", "unsafe use of pseudo-type \"internal\"", "A result of type internal requires at least one input of type internal."},
	} {
		want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
		want.Detail = tc.detail
		hostError(t, p, c, tc.q, want)
	}
	hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_agg_rejected'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
	// Ordinary callers cannot manufacture an opaque aggregate-context state.
	q := "SELECT pg_catalog.ordered_set_transition_multi(NULL,1)"
	want := fixture.ControlsReferenceDiagnostic("42883", "function pg_catalog.ordered_set_transition_multi(unknown, integer) does not exist")
	want.Position = "8"
	want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	hostErrorAtStage(t, p, c, q, want, "prepare")
}
