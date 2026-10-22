package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineParametersWrittenCapturePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_written")
	parametersScenario(t, parametersWrittenPersistence)
}

func parametersWrittenPersistence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	userOperatorExec(t, p, b, c,
		"CREATE SCHEMA rp_written",
		"CREATE TABLE rp_written.data(id int,a int)",
		"CREATE FUNCTION rp_written.leaf(bigint) RETURNS bigint LANGUAGE SQL AS 'SELECT $1'",
		"CREATE FUNCTION rp_written.map(a bigint,b text DEFAULT 'd',c int DEFAULT 9) RETURNS bigint LANGUAGE SQL AS 'SELECT $1*100+$3'",
		"CREATE VIEW rp_written.literal AS SELECT rp_written.map(c=>2,a=>1) AS v",
		"CREATE VIEW rp_written.column_call AS SELECT id,rp_written.map(c=>2,a=>rp_written.leaf(a)) AS v FROM rp_written.data",
		"CREATE FUNCTION rp_written.frozen() RETURNS bigint BEGIN ATOMIC SELECT rp_written.map(c=>2,a=>1); SELECT rp_written.map(c=>2,a=>rp_written.leaf(1)); END",
		"CREATE FUNCTION rp_written.map(a int,b text DEFAULT 'd',c int DEFAULT 9) RETURNS bigint LANGUAGE SQL AS 'SELECT 10000+$1*100+$3'",
		"CREATE FUNCTION rp_written.leaf(integer) RETURNS bigint LANGUAGE SQL AS 'SELECT $1+10'")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_written.data VALUES(1,1),(2,2),(3,NULL)", 3))
	check := func(conn *sql.Conn, backend fixture.Backend) {
		userOperatorValue(t, p, backend, conn, "SELECT v FROM rp_written.literal", "INT8", int64(102))
		parametersValue(t, p, backend, conn, "SELECT rp_written.frozen() AS v", "v", "INT8", int64(102))
		parametersValue(t, p, backend, conn, "SELECT rp_written.map(c=>2,a=>1) AS v", "v", "INT8", int64(10102))
		// Nested leaf returns bigint, so fresh outer still selects bigint map;
		// only the newly selected leaf changes this fresh result to1102.
		parametersValue(t, p, backend, conn, "SELECT rp_written.map(c=>2,a=>rp_written.leaf(1)) AS v", "v", "INT8", int64(1102))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,v FROM rp_written.column_call ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(102)}, {int64(2), int64(202)}, {int64(3), nil}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
		}
		parametersHeld(t, p, backend, conn, "SELECT rp_written.map(c=>$2::int,a=>$1::int) AS v", "INT8", [][]any{{1, 2}, {2, 3}, {nil, 2}}, []any{int64(10102), int64(10203), nil})
	}
	parametersReplay(t, p, b, c, "rp_written", check)
}

func TestReferenceRoutineParametersVariadicCapturePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_pack")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_pack",
			"CREATE FUNCTION rp_pack.collect(VARIADIC xs bigint[]) RETURNS bigint[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE VIEW rp_pack.expanded AS SELECT rp_pack.collect(1,2,NULL) AS v",
			"CREATE FUNCTION rp_pack.frozen() RETURNS bigint[] RETURN rp_pack.collect(1,2,NULL)",
			"CREATE VIEW rp_pack.explicit_array AS SELECT rp_pack.collect(VARIADIC xs=>'[0:1]={4,NULL}'::bigint[]) AS v",
			"CREATE VIEW rp_pack.empty_array AS SELECT rp_pack.collect(VARIADIC xs=>'{}'::bigint[]) AS v",
			"CREATE VIEW rp_pack.null_array AS SELECT rp_pack.collect(VARIADIC xs=>NULL::bigint[]) AS v",
			"CREATE FUNCTION rp_pack.collect(VARIADIC xs integer[]) RETURNS bigint[] LANGUAGE SQL AS 'SELECT ARRAY[99::bigint]'")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, tc := range []struct {
				q string
				v any
			}{
				{"SELECT v FROM rp_pack.expanded", "{1,2,NULL}"},
				{"SELECT rp_pack.frozen() AS v", "{1,2,NULL}"},
				{"SELECT v FROM rp_pack.explicit_array", "[0:1]={4,NULL}"},
				{"SELECT v FROM rp_pack.empty_array", "{}"},
				{"SELECT v FROM rp_pack.null_array", nil},
				{"SELECT rp_pack.collect(1,2,NULL) AS v", "{99}"},
			} {
				s := fixture.ArrayReferenceSelect(tc.q, []string{"v"}, []string{"_INT8"}, []string{"_INT8"}, [][]any{{tc.v}}, backend.PG)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			}
		}
		parametersReplay(t, p, b, c, "rp_pack", check)
	})
}

func TestReferenceRoutineParametersPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_saved")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_saved",
			"CREATE DOMAIN rp_saved.element AS integer CHECK(VALUE>0)",
			"CREATE TABLE rp_saved.data(id int,a rp_saved.element[])",
			"CREATE FUNCTION rp_saved.echo(VARIADIC xs rp_saved.element[] DEFAULT '{}'::rp_saved.element[]) RETURNS rp_saved.element[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_saved.out_value(IN x int DEFAULT 4,OUT y int) LANGUAGE SQL AS 'SELECT $1+1'",
			"CREATE FUNCTION rp_saved.hidden(a int DEFAULT 7) RETURNS int LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_saved.standard() RETURNS int BEGIN ATOMIC SELECT rp_saved.hidden(); SELECT rp_saved.hidden()+rp_saved.out_value(); END",
			"CREATE VIEW rp_saved.result AS SELECT id,rp_saved.echo(VARIADIC xs=>a) AS a FROM rp_saved.data")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_saved.data VALUES(1,'[0:1]={7,NULL}'),(2,NULL),(3,'{}')", 3))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			s := fixture.ArrayReferenceSelect("SELECT id,a FROM rp_saved.result ORDER BY id", []string{"id", "a"}, []string{"INT4", "_ELEMENT"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "[0:1]={7,NULL}"}, {int64(2), nil}, {int64(3), "{}"}}, backend.PG)
			// Pinned18.6 pq exposes unknown user-array OIDs with an empty
			// database type name, observed in tests-4.md.
			if backend.PG {
				s.Types = []string{"INT4", ""}
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			parametersValue(t, p, backend, conn, "SELECT rp_saved.standard() AS v", "v", "INT4", int64(12))
			parametersValue(t, p, backend, conn, "SELECT rp_saved.out_value() AS v", "v", "INT4", int64(5))
			parametersHeld(t, p, backend, conn, "SELECT rp_saved.out_value(x=>$1::int) AS v", "INT4", [][]any{{4}, {8}}, []any{int64(5), int64(9)})
		}
		parametersNativeProvenanceReplay(t, p, b, c, check)
		if b.PG {
			return
		}
		// Late failed replacement cannot publish any prefix declarations or
		// erase the target. Retained eager Rows own their old typed payload.
		rows, err := c.QueryContext(p.Ctx, "SELECT rp_saved.standard() AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		bad := dump.String() + "; CREATE FUNCTION public.rp_prefix(a int DEFAULT 3) RETURNS int LANGUAGE SQL AS 'SELECT $1'; CREATE FUNCTION public.rp_invalid(a int DEFAULT (SELECT 1)) RETURNS int LANGUAGE SQL AS 'SELECT $1';"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid late default accepted")
		}
		check(c, b)
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rp_prefix' OR proname='rp_invalid'", "INT8", int64(0))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("canceled replacement accepted")
		}
		check(c, b)
		if names, err := rows.Columns(); err != nil || len(names) != 1 || names[0] != "v" {
			t.Fatal(names, err)
		}
		if !rows.Next() {
			t.Fatal("retained result lost", rows.Err())
		}
		var value int64
		if err := rows.Scan(&value); err != nil || value != 12 {
			t.Fatal(value, err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained result cardinality", rows.Err())
		}
	})
}

func TestReferenceRoutineParametersPortablePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_portable")
	// A separate fresh catalog is required: Dump includes every live table,
	// including native-only provenance if another scenario had installed it.
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_portable",
			"CREATE DOMAIN rp_portable.element AS integer CHECK(VALUE>0)",
			"CREATE TABLE rp_portable.domains(id int,a rp_portable.element[])",
			"CREATE TABLE rp_portable.integers(id int,a integer[])",
			"CREATE FUNCTION rp_portable.echo(VARIADIC xs rp_portable.element[] DEFAULT '{}'::rp_portable.element[]) RETURNS rp_portable.element[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION rp_portable.echo_int(VARIADIC xs integer[] DEFAULT '{}'::integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT $1'",
			"CREATE VIEW rp_portable.domain_result AS SELECT id,rp_portable.echo(VARIADIC xs=>a) AS a FROM rp_portable.domains",
			"CREATE VIEW rp_portable.integer_result AS SELECT id,rp_portable.echo_int(VARIADIC xs=>a) AS a FROM rp_portable.integers")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_portable.domains VALUES(1,'[0:1]={7,8}'),(2,NULL),(3,'{}')", 3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_portable.integers VALUES(1,'[0:1]={7,NULL}'),(2,NULL),(3,'{}')", 3))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, tc := range []struct {
				q, typ string
				rows   [][]any
			}{
				{"SELECT id,a FROM rp_portable.domain_result ORDER BY id", "_ELEMENT", [][]any{{int64(1), "[0:1]={7,8}"}, {int64(2), nil}, {int64(3), "{}"}}},
				{"SELECT id,a FROM rp_portable.integer_result ORDER BY id", "_INT4", [][]any{{int64(1), "[0:1]={7,NULL}"}, {int64(2), nil}, {int64(3), "{}"}}},
			} {
				s := fixture.ArrayReferenceSelect(tc.q, []string{"id", "a"}, []string{"INT4", tc.typ}, []string{"INT4", "_INT4"}, tc.rows, backend.PG)
				if backend.PG && tc.typ == "_ELEMENT" {
					s.Types = []string{"INT4", ""}
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			}
			s := fixture.ArrayReferenceSelect("SELECT rp_portable.echo() AS v", []string{"v"}, []string{"_ELEMENT"}, []string{"_INT4"}, [][]any{{"{}"}}, backend.PG)
			if backend.PG {
				s.Types = []string{""}
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			parametersValue(t, p, backend, conn, "SELECT rp_portable.echo_int() AS v", "v", "_INT4", "{}")
			parametersHeld(t, p, backend, conn, "SELECT rp_portable.echo_int(VARIADIC xs=>$1::integer[]) AS v", "_INT4",
				[][]any{{"[0:1]={7,NULL}"}, {nil}, {"{}"}}, []any{"[0:1]={7,NULL}", nil, "{}"})
		}
		parametersReplay(t, p, b, c, "rp_portable", check)
	})
}

func TestReferenceRoutineParametersSequenceDefaultPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_sequence")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_sequence",
			"CREATE SEQUENCE rp_sequence.counter START 100",
			"CREATE TABLE rp_sequence.progress(expected bigint)",
			"CREATE FUNCTION rp_sequence.tick(a bigint DEFAULT nextval('rp_sequence.counter')) RETURNS bigint LANGUAGE SQL AS 'SELECT $1'",
			"CREATE VIEW rp_sequence.captured AS SELECT rp_sequence.tick() AS v",
			"CREATE FUNCTION rp_sequence.standard() RETURNS bigint RETURN rp_sequence.tick()")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_sequence.progress VALUES(NULL)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			// The initial independently populated sequence is uncalled/NULL.
			// Subsequent dumps must preserve exactly106 alongside the table's
			// independently written106, before any resetting or further calls.
			state := "SELECT s.last_value IS NOT DISTINCT FROM p.expected AS v FROM pg_sequences s CROSS JOIN rp_sequence.progress p WHERE s.schemaname='rp_sequence' AND s.sequencename='counter'"
			userOperatorValue(t, p, backend, conn, state, "BOOL", true)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT setval('rp_sequence.counter',100,false) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(100)}}))
			for i, tc := range []struct {
				q            string
				storedColumn bool
			}{
				{"SELECT rp_sequence.tick() AS v", false},
				{"SELECT v FROM rp_sequence.captured", true},
				{"SELECT rp_sequence.standard() AS v", false},
			} {
				for j, prepared := range []bool{false, true} {
					s := fixture.Select(tc.q, []string{"v"}, []string{"INT8"}, [][]any{{int64(100 + i*2 + j)}})
					s.Prepared = prepared
					// Stored native columns expose known nullability; pq and
					// computed expression columns retain unknown accessors.
					sourceNullable := tc.storedColumn && !backend.PG
					s.CheckMetadata = parametersMetadataNullability(backend, "v", "INT8", sourceNullable, sourceNullable)
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				}
			}
			parametersHeld(t, p, backend, conn, "SELECT rp_sequence.tick(a=>$1::bigint) AS v", "INT8",
				[][]any{{42}, {nil}, {7}}, []any{int64(42), nil, int64(7)})
			// Neither Prepare nor explicit/NULL actuals consume a default.
			s := fixture.Select("SELECT rp_sequence.tick() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(106)}})
			s.CheckMetadata = parametersMetadata(backend, "v", "INT8")
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("UPDATE rp_sequence.progress SET expected=106", 1))
			userOperatorValue(t, p, backend, conn, state, "BOOL", true)
		}
		parametersReplay(t, p, b, c, "rp_sequence", check)
	})
}

func TestReferenceRoutineParametersDomainRecipeRefresh(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rp_recipe")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA rp_recipe",
			"CREATE DOMAIN rp_recipe.generation AS integer",
			"CREATE FUNCTION rp_recipe.value() RETURNS integer RETURN (1::rp_recipe.generation)::integer",
			"CREATE DOMAIN rp_recipe.default_owner AS integer DEFAULT rp_recipe.value()",
			"CREATE DOMAIN rp_recipe.check_owner AS integer CONSTRAINT above_floor CHECK(VALUE>rp_recipe.value())",
			"CREATE TABLE rp_recipe.data(id integer,v rp_recipe.default_owner)",
			"CREATE TABLE rp_recipe.checked(id integer,v rp_recipe.check_owner)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_recipe.data(id) VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rp_recipe.checked VALUES(1,2)", 1))
		userOperatorExec(t, p, b, c, "ALTER DOMAIN rp_recipe.generation RENAME TO generation_renamed")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			// Both consumer trees still expose only integer/bool; the callee's
			// internal frozen domain generation is the changed owned resource.
			parametersValue(t, p, backend, conn, "SELECT rp_recipe.value() AS v", "v", "INT4", int64(1))
			parametersValue(t, p, backend, conn, "SELECT 2::rp_recipe.check_owner AS v", "v", "INT4", int64(2))
			parametersHeld(t, p, backend, conn, "SELECT $1::integer::rp_recipe.check_owner AS v", "INT4", [][]any{{2}, {3}, {nil}}, []any{int64(2), int64(3), nil})
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO rp_recipe.data(id) VALUES(2)", 1))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT id,v FROM rp_recipe.data ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DELETE FROM rp_recipe.data WHERE id=2", 1))
			// Full diagnostic and direct/prepared execution stages observed
			// in tests-47; no native-derived expectation fields.
			want := fixture.ControlsReferenceDiagnostic("23514", `value for domain rp_recipe.check_owner violates check constraint "above_floor"`)
			want.Schema, want.DataType, want.Constraint = "rp_recipe", "check_owner", "above_floor"
			fixture.EnumReferenceErrorChecked(t, p, conn, "INSERT INTO rp_recipe.checked VALUES(2,0)", false, "execute", func(t *testing.T, got fixture.Diagnostic) {
				if got != want {
					t.Errorf("domain recipe CHECK diagnostic: got %#v want %#v", got, want)
				}
			})
			userOperatorValue(t, p, backend, conn, "SELECT count(*) AS v FROM rp_recipe.checked", "INT8", int64(1))
		}
		parametersReplay(t, p, b, c, "rp_recipe", check)
	})
}
