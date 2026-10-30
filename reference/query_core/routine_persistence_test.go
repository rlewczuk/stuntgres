package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineCorePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "routine_saved")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA routine_saved",
			"CREATE FUNCTION routine_saved.add1(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE FUNCTION routine_saved.add2(integer) RETURNS integer LANGUAGE SQL AS $$SELECT routine_saved.add1(routine_saved.add1($1))$$",
			"CREATE TABLE routine_saved.data(x integer DEFAULT routine_saved.add1(4))",
			"CREATE VIEW routine_saved.result AS SELECT routine_saved.add2(x) AS v FROM routine_saved.data",
			"CREATE TABLE routine_saved.cycle_data(x integer)",
			"CREATE FUNCTION routine_saved.cycle_read() RETURNS integer LANGUAGE SQL AS $$SELECT max(x) FROM routine_saved.cycle_data$$",
			"ALTER TABLE routine_saved.cycle_data ALTER COLUMN x SET DEFAULT routine_saved.cycle_read()")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_saved.data DEFAULT VALUES", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_saved.cycle_data VALUES (7)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_saved.cycle_data DEFAULT VALUES", 1))
		// Reading the stored view is source-column metadata, not a direct
		// scalar-call projection: native view columns are nullable and known.
		routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM routine_saved.result", "v", "INT4", int64(7), true, true)
		routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_saved.cycle_data", "v", "INT4", int64(14))
		if b.PG {
			return
		}

		// Hold an eager result across atomic replacement, before consuming it.
		held, err := c.QueryContext(p.Ctx, "SELECT routine_saved.add1(4) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "SET check_function_bodies = false;") || !strings.Contains(dump.String(), "CREATE FUNCTION") {
			t.Fatalf("missing portable routine declarations: %s", dump.String())
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var retained int64
		if !held.Next() {
			t.Fatal("held eager result disappeared", held.Err())
		}
		if err := held.Scan(&retained); err != nil || retained != 5 {
			t.Fatal(retained, err)
		}
		if held.Next() || held.Err() != nil {
			t.Fatal("held rows", held.Err())
		}
		held.Close()
		routineValue(t, p, b, c, "SELECT routine_saved.add1(4)", "add1", "INT4", int64(5))
		routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM routine_saved.result", "v", "INT4", int64(7), true, true)
		routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_saved.cycle_data", "v", "INT4", int64(14))

		// The exact bytes emitted by native Dump must execute on the pinned
		// PostgreSQL connection, including the text-body/default cycle.
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS routine_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted-stream PostgreSQL replay", err)
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT routine_saved.add1(4) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}),
				fixture.Select("SELECT v FROM routine_saved.result", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}),
				fixture.Select("SELECT sum(x)::integer AS v FROM routine_saved.cycle_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(14)}}),
			} {
				fixture.RunStep(t, p.Ctx, pg, p.PostgresRecord, s)
			}
			if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies = true; DROP SCHEMA routine_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
		}

		before := dump.String()
		assertUnchanged := func() {
			t.Helper()
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if after.String() != before {
				t.Fatal("failed Restore changed target")
			}
			routineValue(t, p, b, c, "SELECT routine_saved.add1(4)", "add1", "INT4", int64(5))
		}
		bad := "SET check_function_bodies = false; CREATE FUNCTION public.routine_invalid() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_routine_table$$; CREATE TABLE routine_reached(v integer DEFAULT public.routine_invalid()); INSERT INTO routine_reached DEFAULT VALUES;"
		want := quotedReplayDiagnostic(true, "routine_invalid", "SELECT x FROM missing_routine_table", "15", "42P01", "relation \"missing_routine_table\" does not exist", "", "", false)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		assertUnchanged()
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(before), routineReadFailure{})); err == nil {
			t.Fatal("read failure accepted")
		}
		assertUnchanged()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(before)); err == nil {
			t.Fatal("canceled Restore accepted")
		}
		assertUnchanged()
		// Public off also stores deferred source. Reached execution is still
		// validated; Restore retains its separate atomic publication contract.
		routineExec(t, p, b, c, "SET check_function_bodies = false")
		show := fixture.Select("SHOW check_function_bodies", []string{"check_function_bodies"}, []string{"TEXT"}, [][]any{{"off"}})
		show.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 {
				t.Fatal(columns, err)
			}
			column := columns[0]
			nullable, nullableKnown := column.Nullable()
			length, lengthKnown := column.Length()
			precision, scale, decimalKnown := column.DecimalSize()
			if nullable || !nullableKnown || column.ScanType() != reflect.TypeFor[string]() || length != math.MaxInt64 || !lengthKnown || precision != 0 || scale != 0 || decimalKnown {
				t.Fatal("native SHOW metadata", column)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, show)
		routineExec(t, p, b, c, "CREATE FUNCTION public.routine_invalid() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_routine_table$$")
		checkBodiesCount(t, p, b, c, "routine_invalid", 1)
		reached := quotedReplayDiagnostic(true, "routine_invalid", "SELECT x FROM missing_routine_table", "15", "42P01", "relation \"missing_routine_table\" does not exist", "", "", false)
		checkBodiesFailure(t, p, b, c, "SELECT public.routine_invalid()", reached, true)
		routineExec(t, p, b, c, "SET check_function_bodies = true")
		public := fixture.ControlsReferenceDiagnostic("42P01", "relation \"missing_routine_table\" does not exist")
		checked := "CREATE FUNCTION public.routine_invalid_checked() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_routine_table$$"
		public.Position = strconv.Itoa(strings.Index(checked, "missing_routine_table") + 1)
		checkBodiesFailure(t, p, b, c, checked, public, false)
		checkBodiesCount(t, p, b, c, "routine_invalid_checked", 0)
		routineExec(t, p, b, c, "DROP FUNCTION public.routine_invalid()")
		assertUnchanged()
	})
}

func TestReferenceRoutineStoredArgumentIdentity(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE FUNCTION routine_capture(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+1000$$",
			"CREATE VIEW routine_capture_view AS SELECT routine_capture(1) AS v",
			"CREATE FUNCTION routine_capture(integer) RETURNS bigint LANGUAGE SQL AS $$SELECT $1+2000$$")
		check := func() {
			routineValueWithNativeNullability(t, p, b, c, "SELECT v FROM routine_capture_view", "v", "INT8", int64(1001), true, true)
			routineValue(t, p, b, c, "SELECT routine_capture(1) AS v", "v", "INT8", int64(2001))
		}
		check()
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check()
	})
}

type routineReadFailure struct{}

func (routineReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("routine replay injected read failure")
}
