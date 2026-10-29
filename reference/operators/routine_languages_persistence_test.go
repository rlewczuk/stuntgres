package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineLanguagesPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rl_saved_owner", "rl_saved_reader"})
	t.Run("objects_authority_and_atomic_replay", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS rl_saved,rl_source CASCADE", "DROP OWNED BY rl_saved_owner,rl_saved_reader CASCADE")()
			defer routineLanguagesRestoreBuiltin(t, c, "rl_saved_sql", "sql")()
			defer routineLanguagesRestoreBuiltin(t, c, "rl_saved_pl", "plpgsql")()
			userOperatorExec(t, p, b, c, "CREATE ROLE rl_saved_owner", "CREATE ROLE rl_saved_reader",
				"CREATE TRUSTED LANGUAGE rl_saved"+routineLanguageFull,
				"CREATE TRUSTED LANGUAGE rl_source"+routineLanguageCall,
				"REVOKE USAGE ON LANGUAGE rl_saved FROM PUBLIC", "GRANT USAGE ON LANGUAGE rl_saved TO rl_saved_reader",
				"ALTER LANGUAGE rl_saved OWNER TO rl_saved_owner",
				"ALTER LANGUAGE sql RENAME TO rl_saved_sql", "ALTER LANGUAGE plpgsql RENAME TO rl_saved_pl",
				"CREATE FUNCTION rl_saved_fn(x integer DEFAULT 4) RETURNS integer LANGUAGE rl_saved AS $$BEGIN RETURN x+1; END$$",
				"ALTER FUNCTION rl_saved_fn(integer) OWNER TO rl_saved_owner",
				"CREATE FUNCTION rl_saved_sql_fn(x integer) RETURNS integer LANGUAGE rl_saved_sql AS 'SELECT $1+1'",
				"CREATE FUNCTION rl_saved_pl_fn(x integer) RETURNS integer LANGUAGE rl_saved_pl AS $$BEGIN RETURN x+1; END$$",
				"CREATE FUNCTION rl_source_fn(x integer) RETURNS integer LANGUAGE rl_source AS $$BEGIN RETURN x+1; END$$",
				"CREATE FUNCTION rl_source_bad() RETURNS integer LANGUAGE rl_source AS 'BEGIN RETURN 1;'",
				"CREATE VIEW rl_saved_view AS SELECT rl_saved_fn(4) AS v",
				"CREATE TABLE rl_saved_data(id integer,v integer DEFAULT rl_saved_fn() CONSTRAINT rl_saved_check CHECK(rl_saved_fn(v)>0))")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_saved_data(id) VALUES(1)", 1))
			check := func(c *sql.Conn, b fixture.Backend) {
				for _, fn := range []string{"rl_saved_fn", "rl_saved_sql_fn", "rl_saved_pl_fn", "rl_source_fn"} {
					parametersHeld(t, p, b, c, "SELECT "+fn+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
				}
				plpgsqlValue(t, p, b, c, "SELECT v FROM rl_saved_view", "INT4", int64(5))
				plpgsqlValue(t, p, b, c, "SELECT v FROM rl_saved_data WHERE id=1", "INT4", int64(5))
				plpgsqlValue(t, p, b, c, "SELECT lanowner=(SELECT oid FROM pg_roles WHERE rolname='rl_saved_owner') AND lanacl IS NOT NULL AS v FROM pg_language WHERE lanname='rl_saved'", "BOOL", true)
				plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('rl_saved_reader','rl_saved','USAGE') AS v", "BOOL", true)
				plpgsqlValue(t, p, b, c, "SELECT has_language_privilege('public','rl_saved','USAGE') AS v", "BOOL", false)
				plpgsqlValue(t, p, b, c, "SELECT p.prolang=l.oid AS v FROM pg_proc p JOIN pg_language l ON l.lanname='rl_saved' WHERE p.proname='rl_saved_fn'", "BOOL", true)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='rl_source_bad'", "INT8", int64(1))
				badDiagnostic := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
				badDiagnostic.InternalPosition, badDiagnostic.InternalQuery = "16", "BEGIN RETURN 1;"
				badDiagnostic.Context = "compilation of PL/pgSQL function \"rl_source_bad\" near line 1"
				routineLanguagesError(t, p, b, c, "SELECT rl_source_bad()", badDiagnostic, true, "execute")
			}
			check(c, b)
			if b.PG {
				return
			} // Dump/Reset/Restore have no PostgreSQL equivalent.
			var saved bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			// A separate driver owns a separate catalog/principal domain. Shift
			// its generated frontier before replay, then check relationships and
			// authority against the destination's actual installed handlers.
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
			if err != nil {
				t.Fatal(err)
			}
			destination := sql.OpenDB(connector)
			defer destination.Close()
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			destinationBackend := fixture.Backend{Recorder: recorder}
			destinationConn, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer destinationConn.Close()
			userOperatorExec(t, p, destinationBackend, destinationConn,
				"CREATE ROLE rl_saved_owner", "CREATE ROLE rl_saved_reader",
				"CREATE TABLE rl_destination_filler(v integer)",
				"CREATE TRUSTED LANGUAGE rl_destination_filler"+routineLanguageCall)
			if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(destinationConn, destinationBackend)
			plpgsqlValue(t, p, destinationBackend, destinationConn, "SELECT l.lanplcallfoid=h.oid AND l.laninline=i.oid AND l.lanvalidator=v.oid AS v FROM pg_language l JOIN pg_proc h ON h.proname='plpgsql_call_handler' AND h.pronamespace='pg_catalog'::regnamespace JOIN pg_proc i ON i.proname='plpgsql_inline_handler' AND i.pronamespace='pg_catalog'::regnamespace JOIN pg_proc v ON v.proname='plpgsql_validator' AND v.pronamespace='pg_catalog'::regnamespace WHERE l.lanname='rl_saved'", "BOOL", true)
			plpgsqlValue(t, p, destinationBackend, destinationConn, "SELECT count(*) AS v FROM pg_class WHERE relname='rl_destination_filler'", "INT8", int64(0))
			check(c, b)
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				routineLanguagesCount(t, p, b, c, "rl_saved", 0)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_language WHERE lanname IN ('sql','plpgsql')", "INT8", int64(2))
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='rl_saved_data'", "INT8", int64(0))
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				check(c, b)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rl_saved_data(id) VALUES(2)", 1))
				plpgsqlValue(t, p, b, c, "SELECT v FROM rl_saved_data WHERE id=2", "INT4", int64(5))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Error("INSERT INTO rl_saved_data VALUES(3,-2)", "23514"))
				if err := stuntgres.Dump(p.Ctx, p.Native, new(bytes.Buffer)); err != nil {
					t.Fatal("redump after replay", err)
				}
			}
			held, closeHeld := routineLanguagesHeld(t, p, b, c, "rl_saved_fn")
			defer closeHeld()
			old, err := c.QueryContext(p.Ctx, "SELECT v FROM rl_saved_data WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			bad := saved.String() + "; CREATE TABLE rl_late_prefix(v integer); CREATE LANGUAGE rl_late HANDLER rl_absent;"
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("late invalid language restore accepted")
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled restore: %v", err)
			}
			check(c, b)
			held(9, 10)
			plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='rl_late_prefix'", "INT8", int64(0))
			plpgsqlValue(t, p, b, c, "SELECT v FROM rl_saved_data WHERE id=2", "INT4", int64(5))
			if !old.Next() {
				t.Fatal("retained row missing", old.Err())
			}
			var v int64
			if err := old.Scan(&v); err != nil || v != 5 {
				t.Fatal(v, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained row changed", old.Err())
			}
		})
	})
	t.Run("bootstrap_name_collision", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// Restore the actual SQL identity before returning the shared oracle.
			defer routineLanguagesRestoreBuiltin(t, c, "rl_collision_sql", "sql")()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var oid int64
				err := c.QueryRowContext(ctx, "SELECT oid FROM pg_language WHERE lanname='sql'").Scan(&oid)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					t.Error(err)
					return
				}
				if err == nil && oid != 14 {
					if _, err := c.ExecContext(ctx, "DROP LANGUAGE sql CASCADE"); err != nil {
						t.Error(err)
					}
				}
			}()
			userOperatorExec(t, p, b, c, "ALTER LANGUAGE sql RENAME TO rl_collision_sql", "CREATE TRUSTED LANGUAGE sql"+routineLanguageFull,
				"CREATE FUNCTION rl_collision_real_sql(x integer) RETURNS integer LANGUAGE rl_collision_sql AS 'SELECT $1+1'",
				"CREATE FUNCTION rl_collision_label_sql(x integer) RETURNS integer LANGUAGE sql AS $$BEGIN RETURN x+1; END$$")
			check := func() {
				for _, fn := range []string{"rl_collision_real_sql", "rl_collision_label_sql"} {
					plpgsqlValue(t, p, b, c, "SELECT "+fn+"(4) AS v", "INT4", int64(5))
				}
			}
			check()
			if b.PG {
				return
			}
			for range 2 {
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
			}
		})
	})
	t.Run("bootstrap_name_swap", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer routineLanguagesRestoreBuiltin(t, c, "rl_swap_tmp", "sql")()
			defer routineLanguagesRestoreBuiltin(t, c, "sql", "plpgsql")()
			defer routineLanguagesRestoreBuiltin(t, c, "plpgsql", "rl_swap_tmp")()
			userOperatorExec(t, p, b, c, "ALTER LANGUAGE sql RENAME TO rl_swap_tmp",
				"ALTER LANGUAGE plpgsql RENAME TO sql", "ALTER LANGUAGE rl_swap_tmp RENAME TO plpgsql",
				"CREATE FUNCTION rl_swap_sql(x integer) RETURNS integer LANGUAGE plpgsql AS 'SELECT $1+1'",
				"CREATE FUNCTION rl_swap_pl(x integer) RETURNS integer LANGUAGE sql AS $$BEGIN RETURN x+1; END$$")
			check := func() {
				plpgsqlValue(t, p, b, c, "SELECT oid=14 AND NOT lanispl AND lanplcallfoid=0 AS v FROM pg_language WHERE lanname='plpgsql'", "BOOL", true)
				plpgsqlValue(t, p, b, c, "SELECT lanispl AND lanplcallfoid<>0 AS v FROM pg_language WHERE lanname='sql'", "BOOL", true)
				for _, fn := range []string{"rl_swap_sql", "rl_swap_pl"} {
					parametersHeld(t, p, b, c, "SELECT "+fn+"($1::integer) AS v", "INT4", [][]any{{4}, {9}}, []any{int64(5), int64(10)})
				}
			}
			check()
			if b.PG {
				return
			}
			for range 2 {
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
			}
		})
	})
}
