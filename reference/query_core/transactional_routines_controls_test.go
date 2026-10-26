package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalRoutinesNativeControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE FUNCTION routine_unit_saved(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$", "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)", "CREATE AGGREGATE txr_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')")
				routineValue(t, p, b, c, "SELECT routine_unit_saved(4) AS v", "v", "INT4", int64(5))
				routineValue(t, p, b, c, "SELECT 2 OPERATOR(public.#~#) 3 AS v", "v", "INT4", int64(5))
				routineValueWithNativeNullability(t, p, b, c, "SELECT txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "v", "INT4", int64(6), true, true)
				if b.PG {
					return
				} // Native controls have no PostgreSQL analogue.
				var baseline bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
					t.Fatal(err)
				}
				q := "SELECT routine_unit_saved(4) AS v"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				retained, err := c.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				routineScalarMetadata(b, "v", "INT4", false, false)(t, retained)
				routineExec(t, p, b, c, "BEGIN", "CREATE FUNCTION txr_private() RETURNS integer RETURN 99", "SAVEPOINT before_replace")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				var private bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &private); err != nil || !bytes.Equal(private.Bytes(), baseline.Bytes()) {
					t.Fatal("Dump included private closure", err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(baseline.Bytes()), routineReadFailure{})); err == nil {
					t.Fatal("reader failure accepted")
				}
				bad := "SET check_function_bodies = false; CREATE FUNCTION invalid_sql() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_table$$; CREATE TABLE txr_reached(v integer DEFAULT invalid_sql()); INSERT INTO txr_reached DEFAULT VALUES;"
				want := quotedReplayDiagnostic(false, "invalid_sql", "SELECT x FROM missing_table", "15", "42P01", "relation \"missing_table\" does not exist", "", "", false)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
				routineValue(t, p, b, c, "SELECT txr_private() AS v", "v", "INT4", int64(99))
				// Replay the actual committed stream into an independent Driver.
				connector, err := stuntgres.NewDriver().OpenConnector("transactional-routine-replay")
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				defer db.Close()
				recorded, replayRecorder := fixture.RecordedNativePool(t, connector)
				if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(baseline.Bytes())); err != nil {
					t.Fatal(err)
				}
				for _, step := range []fixture.Step{
					fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}),
					fixture.Select("SELECT 2 OPERATOR(public.#~#) 3 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}),
					fixture.Select("SELECT txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", []string{"v"}, []string{"INT4"}, [][]any{{int64(6)}}),
					fixture.Select("SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_private'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}),
				} {
					fixture.RunStep(t, p.Ctx, recorded, replayRecorder, step)
				}
				for _, target := range []string{"routine_unit_saved(integer)", "txr_sum(integer)"} {
					if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(baseline.Bytes())); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, recorded, replayRecorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
					var saved bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, db, &saved); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(saved.Bytes())); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, recorded, replayRecorder, fixture.Select("SELECT has_function_privilege('public',$1::text,'EXECUTE') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, target))
					fixture.RunStep(t, p.Ctx, recorded, replayRecorder, fixture.Select("SELECT pg_get_acl(1255,oid,0)::text AS acl FROM pg_proc WHERE oid=$1::regprocedure", []string{"acl"}, []string{"TEXT"}, [][]any{{"{postgres=X/postgres}"}}, target))
				}
				if replacement == "reset" {
					err = stuntgres.Reset(p.Ctx, p.Native)
				} else {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(baseline.Bytes()))
				}
				if err != nil {
					t.Fatal(err)
				}
				routineScalarMetadata(b, "v", "INT4", false, false)(t, retained)
				fixture.ScalarRows(t, false, retained, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, false)
				ordinary := func(err error) {
					t.Helper()
					var state interface{ SQLState() string }
					if err == nil || err.Error() != "stuntgres: catalog replaced before publication" || errors.As(err, &state) {
						t.Fatal("replacement fence", err)
					}
				}
				_, err = c.ExecContext(p.Ctx, "ROLLBACK TO before_replace")
				ordinary(err)
				_, err = s.QueryContext(p.Ctx)
				ordinary(err)
				_, err = c.ExecContext(p.Ctx, "COMMIT")
				ordinary(err)
				c.ExecContext(p.Ctx, "ROLLBACK")
				transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_private'", 0)
				if replacement == "restore" {
					routineValue(t, p, b, c, q, "v", "INT4", int64(5))
					routineValueWithNativeNullability(t, p, b, c, "SELECT txr_sum(v) AS v FROM (VALUES(1),(2),(3)) s(v)", "v", "INT4", int64(6), true, true)
				}
				routineExec(t, p, b, c, "CREATE FUNCTION txr_healthy() RETURNS integer RETURN 5")
				routineValue(t, p, b, c, "SELECT txr_healthy() AS v", "v", "INT4", int64(5))
			})
		})
	}
}
