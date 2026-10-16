package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// The declaration expectations come from REL_18_0 uuid-ossp--1.1.sql.
// Demand and diagnostic expectations were independently verified against pinned
// PostgreSQL18.0; random values are checked structurally, never as goldens.
func TestReferenceObjectGrantUUIDOsspAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "fresh_prepared"
		}
		t.Run(name, func(t *testing.T) {
			uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
				actor := func(body func()) { fixture.RoleGrantActor(run, "uuid_ossp_owner", body) }
				const call = "uuid_ossp_part.uuid_generate_v4()"
				const query = "SELECT " + call + " AS u"
				const signature = "uuid_ossp_part.uuid_generate_v4()"
				const explicit = "12345678-1234-4234-9234-123456789abc"

				// Every retained statement is closed before uuidOsspCase tears down
				// the database-global extension, schema and DROP OWNED role state.
				var statements []*sql.Stmt
				defer func() {
					for _, s := range statements {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					}
				}()
				hold := func(q string) fixture.HeldStatement {
					t.Helper()
					s, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatalf("prepare %s: %v", q, err)
					}
					statements = append(statements, s)
					return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
				}
				read := func(q string, count int, nullable ...bool) []string {
					t.Helper()
					var e fixture.Executor = c
					if prepared {
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatalf("fresh prepare %s: %v", q, err)
						}
						defer s.Close()
						e = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
					}
					return uuidOsspRead(t, p, e, r, pg, q, count, 1, nullable...)
				}
				denial := func(q string) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function uuid_generate_v4"})
					s.SelectRows = true
					s.ErrorStage = "execute"
					return s
				}
				grant := func() { run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO uuid_ossp_owner", 0)) }
				revoke := func() { run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM uuid_ossp_owner", 0)) }

				run(fixture.Exec("CREATE USER uuid_ossp_owner", 0))
				run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
				run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
				var admin string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&admin); err != nil {
					t.Fatal(err)
				}
				uuidOsspInventory(t, p, c, admin, admin)
				var member, namespace, owner, nargs, result int64
				var strict bool
				var volatility, parallel string
				if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,p.pronamespace,p.proowner,p.pronargs,p.prorettype,p.proisstrict,p.provolatile,p.proparallel FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname='uuid_generate_v4'").Scan(&member, &namespace, &owner, &nargs, &result, &strict, &volatility, &parallel); err != nil || member <= 0 || namespace <= 0 || owner != 10 || nargs != 0 || result != 2950 || !strict || volatility != "v" || parallel != "s" {
					t.Fatalf("member=%d namespace=%d owner=%d nargs=%d result=%d strict=%v volatility=%s parallel=%s: %v", member, namespace, owner, nargs, result, strict, volatility, parallel, err)
				}
				// Native pg_proc does not project proacl; its raw default ACL is
				// covered by the descriptor unit, and PUBLIC use is exercised below.
				if pg {
					run(fixture.Select("SELECT p.proacl IS NULL AS default_acl FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname='uuid_generate_v4'", []string{"default_acl"}, []string{"BOOL"}, [][]any{{true}}))
				}
				// Full IN/kind/C-provider flags are private declaration proof, not
				// a reason to expand unsupported pg_proc columns in this scenario.
				actor(func() { read(query, 1) }) // Default NULL ACL means PUBLIC X.

				// Admin owns both the stored DEFAULT and the definer views. Its
				// separate function authority must never be lent to the reader.
				run(fixture.Exec("CREATE TABLE uuid_ossp_part.saved(id integer,u uuid DEFAULT "+call+")", 0))
				run(fixture.Exec("INSERT INTO uuid_ossp_part.saved(id,u) VALUES(1,'"+explicit+"')", 1))
				run(fixture.Exec("INSERT INTO uuid_ossp_part.saved(id) VALUES(2)", 1))
				stored := read("SELECT u FROM uuid_ossp_part.saved ORDER BY id", 2, true)
				run(fixture.Exec("CREATE VIEW uuid_ossp_part.generated AS SELECT id,"+call+" AS u FROM uuid_ossp_part.saved", 0))
				run(fixture.Exec("CREATE VIEW uuid_ossp_part.nested AS SELECT id,u FROM uuid_ossp_part.generated", 0))
				run(fixture.Exec("GRANT SELECT ON uuid_ossp_part.generated,uuid_ossp_part.nested TO uuid_ossp_owner", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))

				var held fixture.HeldStatement
				actor(func() { held = hold(query) }) // Analysis is not invocation.
				checkDenied := func() {
					actor(func() {
						fixture.RunStep(t, p.Ctx, held, r, denial(query))
						run(denial(query))
					})
				}
				checkAllowed := func() {
					actor(func() {
						uuidOsspRead(t, p, held, r, pg, query, 1, 1)
						read(query, 1)
					})
				}
				checkDenied()
				grant()
				checkAllowed()
				revoke()
				checkDenied()
				grant()
				checkAllowed()
				revoke()

				// The reader has view SELECT, but no base-table SELECT. The owner
				// can compute; default view relation authority cannot supply X.
				views := []string{"SELECT u FROM uuid_ossp_part.generated ORDER BY id", "SELECT u FROM uuid_ossp_part.nested ORDER BY id"}
				var viewHeld []fixture.HeldStatement
				for _, q := range views {
					read(q, 2, true)
					viewHeld = append(viewHeld, hold(q))
				}
				actor(func() {
					for i, q := range views {
						run(denial(q))
						fixture.RunStep(t, p.Ctx, viewHeld[i], r, denial(q))
					}
				})
				grant()
				actor(func() {
					for i, q := range views {
						read(q, 2, true)
						uuidOsspRead(t, p, viewHeld[i], r, pg, q, 2, 1, true)
					}
				})
				revoke()
				run(fixture.Exec("GRANT SELECT,INSERT ON uuid_ossp_part.saved TO uuid_ossp_owner", 0))

				// PostgreSQL-first demand hypotheses: a surviving volatile call is
				// initialized even with zero/filtered input. Constant-false CASE
				// removes the call; a row-dependent untaken CASE retains it.
				// These are ordinary/derived input sources, NOT scalar-function
				// FROM support (which the native source whitelist does not admit).
				actor(func() {
					for _, q := range []string{
						query + " WHERE FALSE",
						query + " FROM uuid_ossp_part.saved WHERE id<0",
						query + " FROM (SELECT id FROM uuid_ossp_part.saved WHERE FALSE) AS empty_input",
						"SELECT CASE WHEN id<0 THEN " + call + " ELSE NULL END AS u FROM uuid_ossp_part.saved ORDER BY id",
					} {
						run(denial(q))
					}
					run(fixture.Select("SELECT CASE WHEN FALSE THEN "+call+" ELSE NULL END AS u", []string{"u"}, []string{"UUID"}, [][]any{{nil}}))
				})

				const insert = "INSERT INTO uuid_ossp_part.saved(id) VALUES(3),(4) RETURNING u AS u"
				var insertHeld fixture.HeldStatement
				actor(func() { insertHeld = hold(insert) })
				unchanged := func() {
					actor(func() {
						got := read("SELECT u FROM uuid_ossp_part.saved ORDER BY id", 2, true)
						for i := range stored {
							if got[i] != stored[i] {
								t.Fatalf("stored UUID recomputed: row %d got=%q want=%q", i, got[i], stored[i])
							}
						}
						run(fixture.Select("SELECT id FROM uuid_ossp_part.saved ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					})
				}
				denyInsert := func() {
					actor(func() {
						fixture.RunStep(t, p.Ctx, insertHeld, r, denial(insert))
						run(denial(insert))
						// An explicit first row cannot escape as a committed prefix
						// when a later DEFAULT actually requires denied invocation.
						run(denial("INSERT INTO uuid_ossp_part.saved(id,u) VALUES(3,'" + explicit + "'),(4,DEFAULT) RETURNING u AS u"))
						// Volatile DEFAULT initialization with no input is a distinct
						// PG-first question, not the immutable folding oracle.
						run(denial("INSERT INTO uuid_ossp_part.saved(id) SELECT 9 FROM uuid_ossp_part.saved WHERE FALSE RETURNING u AS u"))
					})
					unchanged()
				}
				denyInsert()
				grant()
				actor(func() { uuidOsspRead(t, p, insertHeld, r, pg, insert, 2, 1, true) })
				run(fixture.Exec("DELETE FROM uuid_ossp_part.saved WHERE id IN (3,4)", 2))
				revoke()
				denyInsert()
				grant()
				actor(func() { uuidOsspRead(t, p, insertHeld, r, pg, insert, 2, 1, true) })
				run(fixture.Exec("DELETE FROM uuid_ossp_part.saved WHERE id IN (3,4)", 2))
				revoke()
				unchanged()

				// Installed UUID is a genuine C provider, not a SQL body invoking
				// either builtin alias. Revoking either side cannot revoke the other.
				actor(func() {
					for _, builtin := range []string{"uuidv4", "gen_random_uuid"} {
						read("SELECT pg_catalog."+builtin+"() AS u", 1)
					}
				})
				// Builtin ACLs outlive schema teardown on real PostgreSQL. Restore
				// them even on a fatal assertion, after restoring the admin actor.
				defer func() {
					for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION pg_catalog.uuidv4(),pg_catalog.gen_random_uuid() TO PUBLIC"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}()
				grant()
				for _, builtin := range []string{"uuidv4", "gen_random_uuid"} {
					run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+builtin+"() FROM PUBLIC", 0))
					actor(func() {
						read(query, 1)
						q := "SELECT pg_catalog." + builtin + "() AS u"
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + builtin})
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
					})
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog."+builtin+"() TO PUBLIC", 0))
				}
				// No Dump of modified routine ACLs: the independent ACL-free UUID
				// controls retain their existing honest serialization boundary.
			})
		})
	}
}
