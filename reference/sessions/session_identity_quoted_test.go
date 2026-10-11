package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionIdentityQuotedViewRoundTrip(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"identity_quoted_owner"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap := "postgres"
			var system any
			if backend.PG {
				bootstrap, system = "stuntgres", "scram-sha-256:stuntgres"
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("CREATE ROLE identity_quoted_owner", 0))
			if backend.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO identity_quoted_owner", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION identity_quoted_owner", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE identity_quoted_source(id INT)", 0), fixture.Exec("INSERT INTO identity_quoted_source VALUES(1)", 1), fixture.Exec(`CREATE VIEW identity_quoted_view AS SELECT "current_user"() AS effective,"session_user"() AS session_name,"system_user"() AS authentication FROM identity_quoted_source`, 0), fixture.Exec("RESET SESSION AUTHORIZATION", 0), fixture.Exec("SET ROLE identity_quoted_owner", 0)} {
				run(s)
			}
			const query = "SELECT effective,session_name,authentication FROM identity_quoted_view"
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			assert := func(want string) {
				s := fixture.Select(query, []string{"effective", "session_name", "authentication"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{want, bootstrap, system}})
				s.CheckMetadata = fixture.SessionIdentityMetadata(backend.PG)
				s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var effective, session string
					var auth any
					if err := rows.Scan(&effective, &session, &auth); err != nil || effective != want || session != bootstrap || fixture.Normalize(auth) != system {
						t.Fatal(effective, session, auth, err)
					}
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, s)
			}
			assert("identity_quoted_owner")
			if !backend.PG {
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
			}
			assert("identity_quoted_owner")
			run(fixture.Exec("RESET ROLE", 0))
			assert(bootstrap)
		})
	}
}
