package reference_test

import (
	"database/sql"
	"github.com/lib/pq"
	"stuntgres"
	"testing"
)

func TestReferenceSessionIdentityFormsAndFreshness(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_reference_role", "IdentityRole"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		b := struct {
			name, bootstrap string
			db              *sql.DB
			record          *referenceRecorder
			native          bool
		}{backend.name, "postgres", backend.db, backend.recorder, !backend.pg}
		if backend.pg {
			b.bootstrap = "stuntgres"
		}
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				if s.selectRows {
					s.checkMetadata = sessionIdentityMetadata(backend.pg)
				}
				referenceRunStep(t, p.ctx, conn, b.record, s)
			}
			system := any(nil)
			if !b.native {
				system = "scram-sha-256:stuntgres"
			}
			run(referenceExec("CREATE ROLE identity_reference_role", 0))
			if !b.native {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_reference_role", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION identity_reference_role", 0))
			run(referenceExec("CREATE TABLE identity_reference_t(id INT,who TEXT DEFAULT current_user,session_name TEXT DEFAULT session_user)", 0))
			run(referenceExec("CREATE VIEW identity_reference_v AS SELECT current_user AS effective,system_user AS auth FROM identity_reference_t", 0))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			held, err := conn.PrepareContext(p.ctx, "SELECT current_role,current_user,session_user,user")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			originalHeld, err := conn.PrepareContext(p.ctx, "SELECT current_user,session_user")
			if err != nil {
				t.Fatal(err)
			}
			defer originalHeld.Close()
			identity := func(want string) {
				t.Helper()
				var role, current, session, user string
				if err := held.QueryRowContext(p.ctx).Scan(&role, &current, &session, &user); err != nil || role != want || current != want || session != b.bootstrap || user != want {
					t.Fatal(role, current, session, user, err)
				}
				if err := originalHeld.QueryRowContext(p.ctx).Scan(&current, &session); err != nil || current != want || session != b.bootstrap {
					t.Fatal(current, session, err)
				}
			}
			identity(b.bootstrap)
			run(referenceExec(`CREATE ROLE "IdentityRole"`, 0))
			run(referenceExec(`SET ROLE "IdentityRole"`, 0))
			identity("IdentityRole")
			run(referenceExec("RESET ROLE", 0))
			identity(b.bootstrap)
			run(referenceExec(`DROP ROLE "IdentityRole"`, 0))
			for _, q := range []string{"SET ROLE identity_reference_role", "SET SESSION ROLE 'identity_reference_role'", "SET ROLE TO identity_reference_role", "SET ROLE = identity_reference_role"} {
				run(referenceExec(q, 0))
				identity("identity_reference_role")
				run(referenceExec("SET ROLE NONE", 0))
				identity(b.bootstrap)
			}
			run(referenceExec("SET ROLE identity_reference_role", 0))
			run(referenceExec("SET ROLE TO DEFAULT", 0))
			identity(b.bootstrap)
			if b.native {
				run(referenceExec("BEGIN", 0))
				run(referenceImplicitError("SET ROLE identity_reference_role", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
				run(referenceExec("ROLLBACK", 0))
				identity(b.bootstrap)
			} else {
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SET ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(referenceExec("ROLLBACK", 0))
				identity(b.bootstrap)
				run(referenceExec("BEGIN", 0))
				run(referenceExec("SET LOCAL ROLE identity_reference_role", 0))
				identity("identity_reference_role")
				run(referenceExec("COMMIT", 0))
				identity(b.bootstrap)
			}
			run(referenceExec("SET ROLE identity_reference_role", 0))
			run(referenceExec("INSERT INTO identity_reference_t(id) VALUES(1)", 1))
			run(referenceSelect("SELECT who,session_name FROM identity_reference_t", []string{"who", "session_name"}, []string{"TEXT", "TEXT"}, [][]any{{"identity_reference_role", b.bootstrap}}))
			view, err := conn.PrepareContext(p.ctx, "SELECT effective,auth FROM identity_reference_v")
			if err != nil {
				t.Fatal(err)
			}
			viewIdentity := func(want string) {
				t.Helper()
				var effective string
				var auth any
				if err := view.QueryRowContext(p.ctx).Scan(&effective, &auth); err != nil || effective != want || auth != system {
					t.Fatal(effective, auth, err)
				}
			}
			viewIdentity("identity_reference_role")
			run(referenceExec("RESET ROLE", 0))
			viewIdentity(b.bootstrap)
			view.Close()
			if b.native {
				// DROP VIEW is not a native capability. Reset is the existing
				// public control for removing this view and its source table.
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			} else {
				run(referenceExec("DROP VIEW identity_reference_v", 0))
				run(referenceExec("DROP TABLE identity_reference_t", 0))
			}
			if !b.native {
				run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM identity_reference_role", 0))
			}
			run(referenceExec("DROP ROLE identity_reference_role", 0))
		})
	}
}

// Immutable form and authentication values are a separate axis from the
// held role/session/default/view lifetime transitions above.
func TestReferenceSessionIdentityFormsAndAuthentication(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		bootstrap, database := "postgres", "reference/"+t.Name()
		database = database[:min(len(database), 63)]
		system := any(nil)
		if backend.pg {
			bootstrap, database, system = "stuntgres", "stuntgres_reference", "scram-sha-256:stuntgres"
		}
		t.Run(backend.name, func(t *testing.T) {
			var catalog, role, session bool
			if err := backend.db.QueryRowContext(p.ctx, "SELECT current_catalog=current_database(), current_role=current_user, session_user=user").Scan(&catalog, &role, &session); err != nil || !catalog || !role || !session {
				t.Fatal(catalog, role, session, err)
			}
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				t.Logf("SQL (prepared=%t): %s", s.prepared, s.query)
				if s.selectRows {
					s.checkMetadata = sessionIdentityMetadata(backend.pg)
				}
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT current_catalog=current_database(),current_role=current_user,session_user=user", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
				s.prepared = prepared
				run(s)
				s = referenceSelect("SELECT current_catalog,current_database(),current_role,current_user,session_user,user", []string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user"}, []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}, [][]any{{database, database, bootstrap, bootstrap, bootstrap, bootstrap}})
				s.prepared = prepared
				run(s)
			}
			run(referenceSelect("SELECT system_user", []string{"system_user"}, []string{"TEXT"}, [][]any{{system}}))
			conditional := system
			if conditional == nil {
				conditional = "trusted"
			}
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT CAST(COALESCE(system_user,'trusted') AS TEXT) AS identity_text", []string{"identity_text"}, []string{"TEXT"}, [][]any{{conditional}})
				s.prepared = prepared
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
					s := referenceSelect(query, []string{"current_database", "current_user", "session_user", "system_user"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{database, bootstrap, bootstrap, system}})
					s.prepared = prepared
					run(s)
				}
			}
		})
	}
}
