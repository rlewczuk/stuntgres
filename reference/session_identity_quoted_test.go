package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSessionIdentityQuotedViewRoundTrip(t *testing.T) {
	roleReferenceCleanup(t, []string{"identity_quoted_owner"})
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			bootstrap := "postgres"
			var system any
			if backend.pg {
				bootstrap, system = "stuntgres", "scram-sha-256:stuntgres"
			}
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE ROLE identity_quoted_owner", 0))
			if backend.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO identity_quoted_owner", 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION identity_quoted_owner", 0))
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, s := range []referenceStep{referenceExec("CREATE TABLE identity_quoted_source(id INT)", 0), referenceExec("INSERT INTO identity_quoted_source VALUES(1)", 1), referenceExec(`CREATE VIEW identity_quoted_view AS SELECT "current_user"() AS effective,"session_user"() AS session_name,"system_user"() AS authentication FROM identity_quoted_source`, 0), referenceExec("RESET SESSION AUTHORIZATION", 0), referenceExec("SET ROLE identity_quoted_owner", 0)} {
				run(s)
			}
			const query = "SELECT effective,session_name,authentication FROM identity_quoted_view"
			held, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			assert := func(want string) {
				s := referenceSelect(query, []string{"effective", "session_name", "authentication"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{want, bootstrap, system}})
				s.checkMetadata = sessionIdentityMetadata(backend.pg)
				s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
					var effective, session string
					var auth any
					if err := rows.Scan(&effective, &session, &auth); err != nil || effective != want || session != bootstrap || referenceNormalize(auth) != system {
						t.Fatal(effective, session, auth, err)
					}
				}
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}, backend.recorder, s)
			}
			assert("identity_quoted_owner")
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			assert("identity_quoted_owner")
			run(referenceExec("RESET ROLE", 0))
			assert(bootstrap)
		})
	}
}
