package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyAtomicity(t *testing.T) {
	for _, route := range []string{"direct", "prepared", "held"} {
		t.Run(route, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"pkey_atomic_reader"})
			pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE ROLE pkey_atomic_reader",
					"CREATE TABLE pkey_atomic_source(a integer,b integer,PRIMARY KEY(b,a))",
					"CREATE TABLE pkey_atomic_marker(id integer)",
					"GRANT USAGE ON SCHEMA public,pk_ext TO pkey_atomic_reader",
					"GRANT SELECT,INSERT ON pkey_atomic_marker TO pkey_atomic_reader",
					"CREATE FUNCTION pkey_atomic_fail(rel text) RETURNS integer LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO pkey_atomic_marker VALUES(5); RETURN (SELECT count(*)::integer FROM pk_ext.dblink_get_pkey(rel)); END$$",
					"CREATE FUNCTION pkey_atomic_arg() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO pkey_atomic_marker VALUES(7); RETURN 'pkey_atomic_source'; END$$",
					"CREATE FUNCTION pkey_atomic_null() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO pkey_atomic_marker VALUES(9); RETURN NULL; END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				const q = "SELECT pkey_atomic_fail($1::text) AS n"
				var x fixture.Executor = c
				if route == "held" {
					x = pkeyHeld(t, p, c, q)
				}
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				for _, denied := range []bool{false, true} {
					if denied {
						run(fixture.Exec("SET SESSION AUTHORIZATION pkey_atomic_reader", 0))
					}
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT pkey_atomic", 0))
					s := pkeyError(q, "42P01", `relation "missing_pkey" does not exist`, "missing_pkey")
					if denied {
						s = pkeyError(q, "42501", "permission denied for table pkey_atomic_source", "pkey_atomic_source")
					}
					// Pinned 18.6: tests-6.md:48,52,55,58 exercises the nested
					// /route/postgres leaves, unlike tests-3's /postgres filter.
					s.Diagnostic.Context = "PL/pgSQL expression \"(SELECT count(*)::integer FROM pk_ext.dblink_get_pkey(rel))\"\nPL/pgSQL function pkey_atomic_fail(text) line 1 at RETURN"
					s.Prepared = route == "prepared"
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					run(fixture.Exec("ROLLBACK TO pkey_atomic", 0))
					run(fixture.Select("SELECT id FROM pkey_atomic_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
					run(fixture.Exec("ROLLBACK", 0))
					if denied {
						run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					}
				}
				for _, tc := range []struct {
					query  string
					want   [][]any
					marker int64
				}{
					{"SELECT * FROM pk_ext.dblink_get_pkey(pkey_atomic_arg()) ORDER BY position", [][]any{{int64(1), "b"}, {int64(2), "a"}}, 7},
					{"SELECT * FROM pk_ext.dblink_get_pkey(pkey_atomic_null()) ORDER BY position", nil, 9},
				} {
					run(fixture.Exec("TRUNCATE pkey_atomic_marker", 0))
					s := fixture.Select(tc.query, []string{"position", "colname"}, []string{"INT4", "TEXT"}, tc.want)
					s.Prepared = route == "prepared"
					var demand fixture.Executor = c
					if route == "held" {
						demand = pkeyHeld(t, p, c, tc.query)
					}
					fixture.RunStep(t, p.Ctx, demand, b.Recorder, s)
					run(fixture.Select("SELECT id FROM pkey_atomic_marker ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{tc.marker}}))
				}
				run(pkeyRows([][]any{{int64(1), "b"}, {int64(2), "a"}}, "pkey_atomic_source"))
			})
		})
	}
	for _, route := range []string{"direct", "held"} {
		t.Run("cancel-"+route, func(t *testing.T) {
			pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE pkey_atomic_cancel(id integer PRIMARY KEY)",
					"CREATE TABLE pkey_cancel_marker(id integer)",
					"CREATE FUNCTION pkey_cancel_arg() RETURNS text LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO pkey_cancel_marker VALUES(8); RETURN 'pkey_atomic_cancel'; END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				const q = "SELECT * FROM pk_ext.dblink_get_pkey(pkey_cancel_arg()) ORDER BY position"
				var x fixture.Executor = c
				if route == "held" {
					x = pkeyHeld(t, p, c, q)
				}
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE pkey_atomic_cancel IN ACCESS EXCLUSIVE MODE", 0))
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("SAVEPOINT pkey_cancel", 0))
				fixture.RelationLockActiveCancel(t, p, b, c, q, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, q)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec("ROLLBACK TO pkey_cancel", 0))
				run(fixture.Select("SELECT id FROM pkey_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{"position", "colname"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "id"}}))
				run(fixture.Select("SELECT id FROM pkey_cancel_marker ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
			})
		})
	}
}
