package reference_test

import (
	"database/sql"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityFormsAndFreshness(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_reference_role", "IdentityRole"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		b := struct {
			name, bootstrap string
			db              *sql.DB
			record          *fixture.Recorder
			native          bool
		}{backend.Name, "postgres", backend.DB, backend.Recorder, !backend.PG}
		if backend.PG {
			b.bootstrap = "stuntgres"
		}
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.Prepared, s.Query)
				if s.SelectRows {
					s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				}
				fixture.RunStep(t, p.Ctx, conn, b.record, s)
			}
			system := any(nil)
			if !b.native {
				system = "scram-sha-256:stuntgres"
			}
			run(fixture.Exec("CREATE ROLE identity_reference_role", 0))
			if !b.native {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO identity_reference_role", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_reference_role", 0))
			run(fixture.Exec("CREATE TABLE identity_reference_t(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", 0))
			run(fixture.Exec("CREATE VIEW identity_reference_v AS SELECT current_user AS effective,system_user AS auth FROM identity_reference_t", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			held, err := conn.PrepareContext(p.Ctx, "SELECT current_role,current_user,session_user,user")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			originalHeld, err := conn.PrepareContext(p.Ctx, "SELECT current_user,session_user")
			if err != nil {
				t.Fatal(err)
			}
			defer originalHeld.Close()
			identity := func(want string) {
				t.Helper()
				var role, current, session, user string
				if err := held.QueryRowContext(p.Ctx).Scan(&role, &current, &session, &user); err != nil || role != want || current != want || session != b.bootstrap || user != want {
					t.Fatal(role, current, session, user, err)
				}
				if err := originalHeld.QueryRowContext(p.Ctx).Scan(&current, &session); err != nil || current != want || session != b.bootstrap {
					t.Fatal(current, session, err)
				}
			}
			identity(b.bootstrap)
			run(fixture.Exec(`CREATE ROLE "IdentityRole"`, 0))
			run(fixture.Exec(`SET ROLE "IdentityRole"`, 0))
			identity("IdentityRole")
			run(fixture.Exec("RESET ROLE", 0))
			identity(b.bootstrap)
			run(fixture.Exec(`DROP ROLE "IdentityRole"`, 0))
			for _, q := range []string{"SET ROLE identity_reference_role", "SET SESSION ROLE 'identity_reference_role'", "SET ROLE TO identity_reference_role", "SET ROLE = identity_reference_role"} {
				run(fixture.Exec(q, 0))
				identity("identity_reference_role")
				run(fixture.Exec("SET ROLE NONE", 0))
				identity(b.bootstrap)
			}
			run(fixture.Exec("SET ROLE identity_reference_role", 0))
			run(fixture.Exec("SET ROLE TO DEFAULT", 0))
			identity(b.bootstrap)
			if b.native {
				run(fixture.Exec("BEGIN", 0))
				run(fixture.ImplicitError("SET ROLE identity_reference_role", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				run(fixture.Exec("ROLLBACK", 0))
				identity(b.bootstrap)
			} else {
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(fixture.Exec("ROLLBACK", 0))
				identity(b.bootstrap)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET LOCAL ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(fixture.Exec("COMMIT", 0))
				identity(b.bootstrap)
			}
			run(fixture.Exec("SET ROLE identity_reference_role", 0))
			run(fixture.Exec("INSERT INTO identity_reference_t(id) VALUES(1)", 1))
			run(fixture.Select("SELECT who,session_name FROM identity_reference_t", []string{"who", "session_name"}, []string{"TEXT", "TEXT"}, [][]any{{"identity_reference_role", b.bootstrap}}))
			view, err := conn.PrepareContext(p.Ctx, "SELECT effective,auth FROM identity_reference_v")
			if err != nil {
				t.Fatal(err)
			}
			viewIdentity := func(want string) {
				t.Helper()
				var effective string
				var auth any
				if err := view.QueryRowContext(p.Ctx).Scan(&effective, &auth); err != nil || effective != want || auth != system {
					t.Fatal(effective, auth, err)
				}
			}
			viewIdentity("identity_reference_role")
			run(fixture.Exec("RESET ROLE", 0))
			viewIdentity(b.bootstrap)
			view.Close()
			if b.native {
				// DROP VIEW is not a native capability. Reset is the existing
				// public control for removing this view and its source table.
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			} else {
				run(fixture.Exec("DROP VIEW identity_reference_v", 0))
				run(fixture.Exec("DROP TABLE identity_reference_t", 0))
			}
			if !b.native {
				run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_reference_role", 0))
			}
			run(fixture.Exec("DROP ROLE identity_reference_role", 0))
		})
	}
}

// Immutable form and authentication values are a separate axis from the
// held role/session/default/view lifetime transitions above.
func TestReferenceSessionIdentityFormsAndAuthentication(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		bootstrap, database := "postgres", "reference/"+t.Name()
		database = database[:min(len(database), 63)]
		system := any(nil)
		if backend.PG {
			bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
		}
		t.Run(backend.Name, func(t *testing.T) {
			var catalog, role, session bool
			if err := backend.DB.QueryRowContext(p.Ctx, "SELECT current_catalog=current_database(), current_role=current_user, session_user=user").Scan(&catalog, &role, &session); err != nil || !catalog || !role || !session {
				t.Fatal(catalog, role, session, err)
			}
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.Prepared, s.Query)
				if s.SelectRows {
					s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT current_catalog=current_database(),current_role=current_user,session_user=user", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
				s.Prepared = prepared
				run(s)
				s = fixture.Select("SELECT current_catalog,current_database(),current_role,current_user,session_user,user", []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}, [][]any{{database, database, bootstrap, bootstrap, bootstrap, bootstrap}})
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Select("SELECT system_user", []string{"system_user"}, []string{"TEXT"}, [][]any{{system}}))
			conditional := system
			if conditional == nil {
				conditional = "trusted"
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) AS identity_text", []string{"identity_text"}, []string{"TEXT"}, [][]any{{conditional}})
				s.Prepared = prepared
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				for _, prefix := range []string{"pg_catalog.", `""`} {
					spell := func(name string) string {
						if prefix == `""` {
							return pq.QuoteIdentifier(name)
						}
						return prefix + name
					}
					query := "SELECT " + spell("current_database") + "()," + spell("current_user") + "()," + spell("session_user") + "()," + spell("system_user") + "()"
					s := fixture.Select(query, []string{"current_database", "current_user", "session_user", "system_user"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{database, bootstrap, bootstrap, system}})
					s.Prepared = prepared
					run(s)
				}
			}
		})
	}
}
