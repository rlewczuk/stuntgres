package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationControlConsent(t *testing.T) {
	for _, oldSide := range []bool{true, false} {
		name := "new_unlabeled_target"
		if oldSide {
			name = "old_unlabeled_target"
		}
		t.Run(name, func(t *testing.T) {
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				sepgsqlOrdinaryTable(t, p, c, run)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DELETE FROM sepgsql_items", 2))
				run(fixture.Exec("ROLLBACK", 0))
				run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			}, func(config *stuntgres.SepgsqlSimulationConfig) {
				// Make every explicit P association admissible. The one effective
				// U table association must still be checked in its actual direction.
				config.Policy.Rules = append(config.Policy.Rules,
					stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "*", Allow: []string{"*"}},
					stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_table", Allow: []string{"setattr", "relabelfrom"}},
				)
				if oldSide {
					config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_table", Deny: []string{"relabelfrom"}})
				}
			}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				sepgsqlTable(run)
				run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS NULL", 0))
				unlabeled := fixture.ExtensionDump(t, p.Ctx, p.Native)
				if !strings.Contains(unlabeled, `SECURITY LABEL FOR selinux ON AGGREGATE "pg_catalog"."count"(*) IS `) {
					t.Fatal("dump lost the actual zero-argument aggregate label identity")
				}
				if !oldSide {
					// Existing target is P; only the imported target is unlabeled.
					run(fixture.Exec("SECURITY LABEL ON TABLE sepgsql_items IS '"+sepgsqlP+"'", 0))
				}
				before := fixture.ExtensionDump(t, p.Ctx, p.Native)
				reader := sql.OpenDB(p.NativeConnector.WithSepgsqlContext(sepgsqlR))
				defer reader.Close()
				var err error
				if oldSide {
					err = stuntgres.Reset(p.Ctx, reader)
				} else {
					err = stuntgres.Restore(p.Ctx, reader, strings.NewReader(unlabeled))
				}
				if got, want := fixture.ExtractDiagnostic(t, err), (fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "SELinux: security policy violation"}); got != want {
					t.Fatalf("effective-unlabeled replacement consent: got %#v want %#v", got, want)
				}
				if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
					t.Fatal("denied replacement changed durable objects, labels or rows")
				}
				run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
			})
		})
	}
	for _, roleChange := range []bool{false, true} {
		name := "peer_label_during_reader_io"
		if roleChange {
			name = "peer_role_during_reader_io"
		}
		t.Run(name, func(t *testing.T) {
			if roleChange {
				fixture.RoleReferenceCleanup(t, []string{"sepgsql_control_peer"})
			}
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				sepgsqlOrdinaryTable(t, p, c, run)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("UPDATE sepgsql_items SET v='changed' WHERE id=1", 1))
				run(fixture.Exec("ROLLBACK", 0))
				run(sepgsqlCount("SELECT count(*) FROM sepgsql_items WHERE v='one'", 1))
			}, nil, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				sepgsqlTable(run)
				run(fixture.Exec("CREATE SEQUENCE sepgsql_control_fence START 53 CACHE 1", 0))
				original := fixture.ExtensionDump(t, p.Ctx, p.Native)
				var callbackErr error
				var afterPeer string
				source := &sepgsqlControlReader{source: strings.NewReader(original), before: func() {
					query := "SECURITY LABEL ON TABLE sepgsql_items IS '" + sepgsqlS + "'"
					if roleChange {
						query = "CREATE ROLE sepgsql_control_peer"
					}
					_, callbackErr = c.ExecContext(p.Ctx, query)
					if callbackErr == nil {
						afterPeer = fixture.ExtensionDump(t, p.Ctx, p.Native)
					}
				}}
				err := stuntgres.Restore(p.Ctx, p.Native, source)
				if callbackErr != nil {
					t.Fatal(callbackErr)
				}
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				if afterPeer == "" || fixture.ExtensionDump(t, p.Ctx, p.Native) != afterPeer {
					t.Fatal("stale control overwrote peer publication")
				}
				run(sepgsqlCount("SELECT count(*) FROM sepgsql_items", 2))
				sepgsqlSequenceState(run, "sepgsql_control_fence", 53, false)
				sepgsqlSequenceAssociation(run, "sepgsql_control_fence", sepgsqlP, true)
				if roleChange {
					run(sepgsqlCount("SELECT count(*) FROM pg_roles WHERE rolname='sepgsql_control_peer'", 1))
				}
			})
		})
	}
}
