package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func duOwnedSeed(run func(fixture.Step), schema string) {
	for _, role := range duRoles {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	for _, q := range []string{
		"CREATE SCHEMA " + schema,
		"GRANT USAGE,CREATE ON SCHEMA " + schema + " TO du_owner,du_domain_owner,du_reader",
		"CREATE TYPE " + schema + ".e AS ENUM('x')",
		"CREATE DOMAIN " + schema + ".p AS " + schema + ".e",
		"CREATE DOMAIN " + schema + ".c AS " + schema + ".p",
		"ALTER TYPE " + schema + ".e OWNER TO du_owner",
		"ALTER DOMAIN " + schema + ".p OWNER TO du_domain_owner",
		"GRANT du_owner TO du_member WITH INHERIT TRUE, SET TRUE",
		"GRANT du_owner TO du_middle WITH INHERIT TRUE, SET TRUE",
		"GRANT du_middle TO du_leaf WITH INHERIT TRUE, SET TRUE",
		"GRANT du_middle TO du_blocked WITH INHERIT FALSE, SET TRUE",
		"GRANT du_owner TO du_set WITH INHERIT FALSE, SET TRUE",
	} {
		run(fixture.Exec(q, 0))
	}
}
func duOwnedRows(database, schema string) [][]any {
	return [][]any{duRow(database, schema, "e", schema, "p"), duRow(database, schema, "p", schema, "c")}
}

func TestReferenceInfoDomainUdtUsageVisibility(t *testing.T) {
	for _, phase := range []string{"ownership_memberships", "source_select"} {
		t.Run(phase, func(t *testing.T) {
			duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				duOwnedSeed(run, "du_scope")
				want := duOwnedRows(database, "du_scope")
				where := "domain_schema='du_scope'"
				held := cduHold(t, p, c, duProjection(b.PG, where, nil).Query)
				check := func(rows [][]any) { duCheck(t, p, c, r, held, duProjection(b.PG, where, rows)) }
				actor := func(role string, rows [][]any) {
					run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
					check(rows)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				admin, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer admin.Close()
				adminRun := func(q string) { fixture.RunStep(t, p.Ctx, admin, r, fixture.Exec(q, 0)) }
				if phase == "source_select" {
					// SELECT authorization is independent of base ownership disclosure.
					defer func() {
						if _, err := admin.ExecContext(p.Ctx, "GRANT SELECT ON information_schema.domain_udt_usage TO PUBLIC"); err != nil {
							t.Error(err)
						}
					}()
					adminRun("REVOKE SELECT ON information_schema.domain_udt_usage FROM PUBLIC,du_reader")
					run(fixture.Exec("SET SESSION AUTHORIZATION du_reader", 0))
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view domain_udt_usage")
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(held.SQL, d)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						run(s)
					}
					failure := fixture.ErrorDiagnostic(held.SQL, d)
					failure.SelectRows, failure.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, r, failure)
					adminRun("GRANT SELECT ON information_schema.domain_udt_usage TO PUBLIC")
					check(nil)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					check(want)
					return
				}
				check(want)
				for _, x := range []struct {
					role string
					rows [][]any
				}{{"du_owner", want[:1]}, {"du_domain_owner", want[1:]}, {"du_reader", nil}, {"du_member", want[:1]}, {"du_leaf", want[:1]}, {"du_blocked", nil}, {"du_set", nil}} {
					actor(x.role, x.rows)
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION du_reader", 0))
				for _, q := range []string{
					"GRANT USAGE ON TYPE du_scope.e TO du_reader",
					"GRANT USAGE ON TYPE du_scope.e TO du_reader WITH GRANT OPTION",
					"GRANT USAGE ON DOMAIN du_scope.p,du_scope.c TO du_reader WITH GRANT OPTION",
					"GRANT USAGE ON TYPE du_scope.e TO PUBLIC",
					"GRANT USAGE ON DOMAIN du_scope.p,du_scope.c TO PUBLIC",
				} {
					adminRun(q)
					check(nil)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				for _, q := range []string{"REVOKE ALL ON SCHEMA du_scope FROM PUBLIC,du_owner,du_domain_owner,du_reader", "REVOKE ALL ON TYPE du_scope.e FROM PUBLIC,du_reader", "REVOKE ALL ON DOMAIN du_scope.p,du_scope.c FROM PUBLIC,du_reader"} {
					adminRun(q)
				}
				actor("du_owner", want[:1])
				actor("du_domain_owner", want[1:])
				run(fixture.Exec("SET SESSION AUTHORIZATION du_set", 0))
				check(nil)
				run(fixture.Exec("SET ROLE du_owner", 0))
				check(want[:1])
				fixture.RunStep(t, p.Ctx, admin, r, duProjection(b.PG, where, want))
				run(fixture.Exec("RESET ROLE", 0))
				check(nil)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION du_member", 0))
				check(want[:1])
				adminRun("GRANT du_owner TO du_member WITH INHERIT FALSE")
				check(nil)
				adminRun("GRANT du_owner TO du_member WITH INHERIT TRUE")
				check(want[:1])
				adminRun("REVOKE du_owner FROM du_member")
				check(nil)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				// Restoring setup rights permits real owner transfer; those rights do
				// not themselves admit a view row.
				adminRun("GRANT USAGE,CREATE ON SCHEMA du_scope TO du_owner,du_domain_owner,du_reader")
				adminRun("ALTER TYPE du_scope.e OWNER TO du_reader")
				actor("du_owner", nil)
				actor("du_reader", want[:1])
				actor("du_domain_owner", want[1:])
				adminRun("ALTER DOMAIN du_scope.p OWNER TO du_owner")
				actor("du_domain_owner", nil)
				actor("du_owner", want[1:])
				actor("du_reader", want[:1])
				check(want)
			})
		})
	}
}
