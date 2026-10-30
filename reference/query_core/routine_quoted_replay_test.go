package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const quotedReplaySource = "\n-- retain the original spelling, whitespace and comment\nSELECT a FROM qrr.source\n"

func TestReferenceRoutineQuotedReplayPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"qrr_owner"})
	fixture.SchemaReferenceCleanup(t, "qrr")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE ROLE qrr_owner", "CREATE SCHEMA qrr AUTHORIZATION qrr_owner",
			"CREATE TABLE qrr.source(a integer)",
			"CREATE FUNCTION qrr.soft() RETURNS integer LANGUAGE SQL AS $$"+quotedReplaySource+"$$",
			"CREATE FUNCTION qrr.hard() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM qrr.source; END",
			"CREATE FUNCTION qrr.capture(bigint) RETURNS bigint RETURN $1+1000",
			"CREATE FUNCTION qrr.with_default(v bigint DEFAULT qrr.capture(1)) RETURNS bigint RETURN v",
			"CREATE FUNCTION qrr.soft_call() RETURNS bigint LANGUAGE SQL AS $$SELECT qrr.capture(1)$$",
			"ALTER FUNCTION qrr.capture(bigint) RENAME TO captured",
			"CREATE FUNCTION qrr.capture(integer) RETURNS bigint RETURN $1+2000",
			"ALTER FUNCTION qrr.soft() OWNER TO qrr_owner",
			"ALTER FUNCTION qrr.hard() OWNER TO qrr_owner",
			"CREATE TABLE qrr.dropped(a integer)",
			"CREATE FUNCTION qrr.dropped_soft() RETURNS integer LANGUAGE SQL AS $$SELECT a FROM qrr.dropped$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qrr.source VALUES(7)", 1))
		q := "SELECT qrr.soft() AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		routineValue(t, p, b, c, q, "v", "INT4", int64(7))
		routineExec(t, p, b, c, "ALTER TABLE qrr.source RENAME COLUMN a TO renamed_a")
		quotedReplayFailure(t, p, b, c, nil, q, "soft", quotedReplaySource, "65", "42703", "column \"a\" does not exist", "", "")
		routineExec(t, p, b, c, "ALTER TABLE qrr.source RENAME TO saved", "DROP TABLE qrr.dropped RESTRICT")
		check := func(conn *sql.Conn, backend fixture.Backend, initializing bool) {
			t.Helper()
			routineValue(t, p, backend, conn, "SELECT qrr.hard() AS v", "v", "INT4", int64(7))
			routineValue(t, p, backend, conn, "SELECT qrr.with_default() AS v", "v", "INT8", int64(1001))
			routineValue(t, p, backend, conn, "SELECT qrr.soft_call() AS v", "v", "INT8", int64(2001))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='qrr.soft_call()'::regprocedure AND refclassid='pg_proc'::regclass AND refobjid IN ('qrr.captured(bigint)'::regprocedure,'qrr.capture(integer)'::regprocedure)",
				[]string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT p.prosrc,p.prokind,p.pronargs::integer,p.prorettype='integer'::regtype AS returns_integer,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='qrr.soft()'::regprocedure",
				[]string{"prosrc", "prokind", "pronargs", "returns_integer", "rolname"}, []string{"TEXT", "CHAR", "INT4", "BOOL", "NAME"},
				[][]any{{quotedReplaySource, "f", int64(0), true, "qrr_owner"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT p.proname,p.prokind,p.pronargs::integer,p.prorettype='integer'::regtype AS returns_integer,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='qrr.hard()'::regprocedure",
				[]string{"proname", "prokind", "pronargs", "returns_integer", "rolname"}, []string{"NAME", "CHAR", "INT4", "BOOL", "NAME"},
				[][]any{{"hard", "f", int64(0), true, "qrr_owner"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT p.proname,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND p.oid IN ('qrr.soft()'::regprocedure,'qrr.hard()'::regprocedure) AND d.refclassid='pg_class'::regclass AND d.refobjid='qrr.saved'::regclass ORDER BY p.proname,d.refobjsubid,d.deptype",
				[]string{"proname", "refobjsubid", "deptype"}, []string{"NAME", "INT4", "CHAR"}, [][]any{{"hard", int64(1), "n"}}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(
				"SELECT refobjsubid,deptype FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='qrr.with_default(bigint)'::regprocedure AND refclassid='pg_proc'::regclass AND refobjid='qrr.captured(bigint)'::regprocedure ORDER BY refobjsubid,deptype",
				[]string{"refobjsubid", "deptype"}, []string{"INT4", "CHAR"}, [][]any{{int64(0), "n"}}))
			if initializing {
				want := quotedReplayDiagnostic(true, "soft", quotedReplaySource, "72", "42P01", "relation \"qrr.source\" does not exist", "", "", false)
				checkBodiesFailure(t, p, backend, conn, q, want, true)
			} else {
				quotedReplayFailure(t, p, backend, conn, nil, q, "soft", quotedReplaySource, "72", "42P01", "relation \"qrr.source\" does not exist", "", "")
			}
			want := quotedReplayDiagnostic(true, "dropped_soft", "SELECT a FROM qrr.dropped", "15", "42P01", "relation \"qrr.dropped\" does not exist", "", "", false)
			checkBodiesFailure(t, p, backend, conn, "SELECT qrr.dropped_soft() AS v", want, true)
			for _, query := range []string{"DROP TABLE qrr.saved RESTRICT", "DROP FUNCTION qrr.captured(bigint) RESTRICT"} {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Error(query, "2BP01"))
			}
		}
		check(c, b, false)
		if !b.PG {
			old, err := c.QueryContext(p.Ctx, "SELECT qrr.hard() AS v")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			recorded, freshRecorder := fixture.RecordedNativePool(t, connector)
			if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE qrr_owner"); err != nil {
				t.Fatal(err)
			}
			fc, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fc.Close()
			for range 2 {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				for _, target := range []*sql.DB{p.Native, fresh} {
					if err := stuntgres.Reset(p.Ctx, target); err != nil {
						t.Fatal(err)
					}
					// A successful replacement fences a live transaction and its
					// held consumer, independently of fresh invocation recovery.
					fence, err := target.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer fence.Close()
						if _, err := fence.ExecContext(p.Ctx, "BEGIN"); err != nil {
							t.Fatal(err)
						}
						defer fence.ExecContext(p.Ctx, "ROLLBACK")
						stmt, err := fence.PrepareContext(p.Ctx, "SELECT 7 AS v")
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal("stale quoted definition Restore", err)
						}
						rows, err := stmt.QueryContext(p.Ctx)
						if rows != nil {
							rows.Close()
						}
						var state interface{ SQLState() string }
						if err == nil || err.Error() != "stuntgres: catalog replaced before publication" || errors.As(err, &state) {
							t.Fatal("successful replacement generation fence", err)
						}
					}()
				}
				check(c, b, true)
				check(fc, fixture.Backend{Recorder: freshRecorder}, true)
				if p.Postgres != nil {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					var count int
					if err := pg.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='qrr_owner'").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count == 0 {
						if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE qrr_owner"); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS qrr CASCADE"); err != nil {
						t.Fatal(err)
					}
					if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("unchanged emitted PostgreSQL replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, true)
					if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA qrr CASCADE; DROP ROLE qrr_owner"); err != nil {
						t.Fatal(err)
					}
					pg.Close()
				}
			}
			fixture.CatalogControlNativeValues(t, old, 7)
			// Replacements invalidate old held generations. Recovery uses handles
			// prepared against the current declaration, then retained across DDL.
			held.Close()
			held, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
		}
		routineExec(t, p, b, c, "ALTER TABLE qrr.saved RENAME TO source", "ALTER TABLE qrr.source RENAME COLUMN renamed_a TO a", "CREATE TABLE qrr.dropped(a integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO qrr.dropped VALUES(7)", 1))
		routineValue(t, p, b, c, q, "v", "INT4", int64(7))
		routineDropHeld(t, p, b, c, held, q, "INT4", int64(7))
		routineValue(t, p, b, c, "SELECT qrr.dropped_soft() AS v", "v", "INT4", int64(7))
	})
}
