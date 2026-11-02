package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func cuOwnedSeed(run func(fixture.Step), schema string) {
	for _, role := range cuRoles {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	for _, q := range []string{
		"CREATE SCHEMA " + schema,
		"GRANT USAGE,CREATE ON SCHEMA " + schema + " TO cu_type_owner,cu_parent_owner,cu_child_owner,cu_table_owner,cu_reader",
		"CREATE TYPE " + schema + ".e AS ENUM('x')",
		"CREATE DOMAIN " + schema + ".p AS " + schema + ".e",
		"CREATE DOMAIN " + schema + ".c AS " + schema + ".p",
		"CREATE TABLE " + schema + ".t(x " + schema + ".e,y " + schema + ".p,z " + schema + ".c)",
		"ALTER TYPE " + schema + ".e OWNER TO cu_type_owner",
		"ALTER DOMAIN " + schema + ".p OWNER TO cu_parent_owner",
		"ALTER DOMAIN " + schema + ".c OWNER TO cu_child_owner",
		"ALTER TABLE " + schema + ".t OWNER TO cu_table_owner",
		"GRANT cu_type_owner TO cu_member WITH INHERIT TRUE, SET TRUE",
		"GRANT cu_type_owner TO cu_middle WITH INHERIT TRUE, SET TRUE",
		"GRANT cu_middle TO cu_leaf WITH INHERIT TRUE, SET TRUE",
		"GRANT cu_middle TO cu_blocked WITH INHERIT FALSE, SET TRUE",
		"GRANT cu_type_owner TO cu_set WITH INHERIT FALSE, SET TRUE",
	} {
		run(fixture.Exec(q, 0))
	}
}

func cuOwnedRows(database, schema string) [][]any {
	return [][]any{cuRow(database, schema, "e", schema, "t", "x"), cuRow(database, schema, "e", schema, "t", "y"), cuRow(database, schema, "p", schema, "t", "z")}
}

func TestReferenceInfoColumnUdtUsageVisibility(t *testing.T) {
	for _, phase := range []string{"ownership_memberships", "source_select"} {
		t.Run(phase, func(t *testing.T) {
			cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				cuOwnedSeed(run, "cu_scope")
				want := cuOwnedRows(database, "cu_scope")
				where := "table_schema='cu_scope'"
				held := cduHold(t, p, c, cuProjection(b.PG, where, nil).Query)
				check := func(rows [][]any) { cuCheck(t, p, c, r, held, cuProjection(b.PG, where, rows)) }
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
					defer func() {
						if _, err := admin.ExecContext(p.Ctx, "GRANT SELECT ON information_schema.column_udt_usage TO PUBLIC"); err != nil {
							t.Error(err)
						}
					}()
					adminRun("REVOKE SELECT ON information_schema.column_udt_usage FROM PUBLIC,cu_reader")
					run(fixture.Exec("SET SESSION AUTHORIZATION cu_reader", 0))
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view column_udt_usage")
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(held.SQL, d)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						run(s)
					}
					failure := fixture.ErrorDiagnostic(held.SQL, d)
					failure.SelectRows, failure.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, r, failure)
					adminRun("GRANT SELECT ON information_schema.column_udt_usage TO PUBLIC")
					check(nil)
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					actor("cu_type_owner", want[:2])
					check(want)
					return
				}
				check(want)
				for _, x := range []struct {
					role string
					rows [][]any
				}{
					{"cu_type_owner", want[:2]}, {"cu_parent_owner", want[2:]}, {"cu_child_owner", nil}, {"cu_table_owner", nil}, {"cu_reader", nil}, {"cu_member", want[:2]}, {"cu_leaf", want[:2]}, {"cu_blocked", nil}, {"cu_set", nil},
				} {
					actor(x.role, x.rows)
				}
				run(fixture.Exec("SET SESSION AUTHORIZATION cu_reader", 0))
				for _, q := range []string{
					"GRANT USAGE ON SCHEMA cu_scope TO cu_reader WITH GRANT OPTION",
					"GRANT SELECT ON cu_scope.t TO cu_reader WITH GRANT OPTION",
					"GRANT SELECT(x) ON cu_scope.t TO cu_reader WITH GRANT OPTION",
					"GRANT USAGE ON TYPE cu_scope.e TO cu_reader WITH GRANT OPTION",
					"GRANT USAGE ON DOMAIN cu_scope.p,cu_scope.c TO cu_reader WITH GRANT OPTION",
					"GRANT USAGE ON TYPE cu_scope.e TO PUBLIC",
					"GRANT USAGE ON DOMAIN cu_scope.p,cu_scope.c TO PUBLIC",
				} {
					adminRun(q)
					check(nil)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				for _, q := range []string{"REVOKE ALL ON SCHEMA cu_scope FROM PUBLIC,cu_type_owner,cu_parent_owner,cu_reader", "REVOKE ALL ON cu_scope.t FROM PUBLIC,cu_type_owner,cu_parent_owner,cu_reader", "REVOKE SELECT(x) ON cu_scope.t FROM cu_reader", "REVOKE ALL ON TYPE cu_scope.e FROM PUBLIC,cu_reader", "REVOKE ALL ON DOMAIN cu_scope.p,cu_scope.c FROM PUBLIC,cu_reader"} {
					adminRun(q)
				}
				actor("cu_type_owner", want[:2])
				actor("cu_parent_owner", want[2:])
				run(fixture.Exec("SET SESSION AUTHORIZATION cu_set", 0))
				check(nil)
				run(fixture.Exec("SET ROLE cu_type_owner", 0))
				check(want[:2])
				fixture.RunStep(t, p.Ctx, admin, r, cuProjection(b.PG, where, want))
				run(fixture.Exec("RESET ROLE", 0))
				check(nil)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION cu_member", 0))
				check(want[:2])
				adminRun("GRANT cu_type_owner TO cu_member WITH INHERIT FALSE")
				check(nil)
				adminRun("GRANT cu_type_owner TO cu_member WITH INHERIT TRUE")
				check(want[:2])
				adminRun("REVOKE cu_type_owner FROM cu_member")
				check(nil)
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				adminRun("GRANT USAGE,CREATE ON SCHEMA cu_scope TO cu_type_owner,cu_parent_owner,cu_reader")
				adminRun("ALTER TYPE cu_scope.e OWNER TO cu_reader")
				actor("cu_type_owner", nil)
				actor("cu_reader", want[:2])
				actor("cu_parent_owner", want[2:])
				adminRun("ALTER DOMAIN cu_scope.p OWNER TO cu_type_owner")
				actor("cu_parent_owner", nil)
				actor("cu_type_owner", want[2:])
				actor("cu_reader", want[:2])
				adminRun("ALTER DOMAIN cu_scope.c OWNER TO cu_reader")
				adminRun("ALTER TABLE cu_scope.t OWNER TO cu_reader")
				actor("cu_reader", want[:2])
				actor("cu_type_owner", want[2:])
				check(want)
			})
		})
	}
}
