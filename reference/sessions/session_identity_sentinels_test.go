package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityRoleSentinels(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"default", "NONE", "identity_sentinel_selected"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		b := struct {
			name, bootstrap string
			db              *sql.DB
			record          *fixture.Recorder
		}{backend.Name, "postgres", backend.DB, backend.Recorder}
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
				fixture.RunStep(t, p.Ctx, conn, b.record, s)
			}
			bootstrapIdentity := func() {
				s := fixture.Select("SELECT session_user,current_user,session_user", []string{"session_user", "current_user", "session_user"}, []string{"NAME", "NAME", "NAME"}, [][]any{{b.bootstrap, b.bootstrap, b.bootstrap}})
				s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var session, current, repeated string
					if err := rows.Scan(&session, &current, &repeated); err != nil || session != b.bootstrap || current != b.bootstrap || repeated != b.bootstrap {
						t.Fatal(session, current, repeated, err)
					}
				}
				run(s)
			}
			for _, q := range []string{`CREATE ROLE "default"`, `CREATE ROLE "NONE"`, "CREATE ROLE identity_sentinel_selected"} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				selectRole := func(query, want string) {
					t.Helper()
					s := fixture.Exec(query, 0)
					s.Prepared = prepared
					run(s)
					var current string
					if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&current); err != nil || current != want {
						t.Fatal(query, current, want, err)
					}
					run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{want, b.bootstrap}}))
					if want == b.bootstrap {
						bootstrapIdentity()
					}
				}
				for _, reset := range []string{"SET ROLE TO DEFAULT", "SET ROLE = DEFAULT", `SET ROLE "none"`, "SET ROLE 'none'"} {
					selectRole("SET ROLE identity_sentinel_selected", "identity_sentinel_selected")
					selectRole(reset, b.bootstrap)
				}
				for _, query := range []string{`SET ROLE "default"`, "SET ROLE 'default'"} {
					selectRole(query, "default")
				}
				for _, query := range []string{`SET ROLE "NONE"`, "SET ROLE 'NONE'"} {
					selectRole(query, "NONE")
				}
				selectRole("RESET ROLE", b.bootstrap)
			}
			run(fixture.Exec("CREATE TABLE identity_sentinel(id INT)", 0))
			for _, q := range []string{"INSERT INTO identity_sentinel VALUES(7); SET ROLE NONE garbage", "SET ROLE NONE garbage", "RESET ROLE garbage"} {
				_, err := conn.ExecContext(p.Ctx, q)
				fixture.State(t, err, "42601")
				run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{b.bootstrap, b.bootstrap}}))
				bootstrapIdentity()
			}
			run(fixture.Select("SELECT id FROM identity_sentinel", []string{"id"}, []string{"INT4"}, [][]any{}))
			if !backend.PG {
				canceled, cancel := context.WithCancel(p.Ctx)
				cancel()
				if rows, err := conn.QueryContext(canceled, "SELECT current_user"); rows != nil || !errors.Is(err, context.Canceled) {
					if rows != nil {
						rows.Close()
					}
					t.Fatal(rows, err)
				}
				if stmt, err := conn.PrepareContext(canceled, "SELECT current_user"); stmt != nil || !errors.Is(err, context.Canceled) {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatal(stmt, err)
				}
			} else {
				// A fast pq Query/Prepare can succeed despite a pre-canceled
				// context. Cancel only after actual server admission instead.
				heldIdentity, err := conn.PrepareContext(p.Ctx, "SELECT current_user,session_user")
				if err != nil {
					t.Fatal(err)
				}
				defer heldIdentity.Close()
				for _, prepared := range []bool{false, true} {
					fixture.SessionCancel(t, p.Ctx, conn, prepared, true)
					run(fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{b.bootstrap, b.bootstrap}}))
					run(fixture.Select("SELECT id FROM identity_sentinel", []string{"id"}, []string{"INT4"}, [][]any{}))
					var current, session string
					if err := heldIdentity.QueryRowContext(p.Ctx).Scan(&current, &session); err != nil || current != b.bootstrap || session != b.bootstrap {
						t.Fatal("held identity after admitted cancellation", current, session, err)
					}
				}
			}
			run(fixture.Exec(`DROP ROLE "default","NONE",identity_sentinel_selected`, 0))
		})
	}
}
