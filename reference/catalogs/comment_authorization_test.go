package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCommentSchemaUsage(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "comment_acl_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{role})
			fixture.SchemaReferenceCleanup(t, "comment_private")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, admin := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
					run("CREATE USER " + role)
					run("CREATE SCHEMA comment_private")
					run("CREATE TABLE comment_private.t(v INT)")
					fixture.LockExec(t, p.Ctx, admin, "INSERT INTO comment_private.t VALUES(7)", 1)
					run("COMMENT ON TABLE comment_private.t IS 'old'")
					run("COMMENT ON COLUMN comment_private.t.v IS 'old column'")
					run("REVOKE ALL ON SCHEMA comment_private FROM PUBLIC")
					fixture.LockExec(t, p.Ctx, c, "SET SESSION AUTHORIZATION "+role, 0)
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					denied := func(q, code, message string) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RoleReferenceError(q, code, message, "", prepared, "execute"))
					}
					denied("COMMENT ON TABLE comment_private.missing IS NULL", "42501", "permission denied for schema comment_private")
					denied("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42501", "permission denied for schema comment_private")
					for _, tc := range []struct {
						q, position string
						selectRows  bool
					}{{"SELECT v FROM comment_private.t", "15", true}, {"INSERT INTO comment_private.t VALUES(9)", "13", false}} {
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema comment_private", Position: tc.position})
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, tc.selectRows, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					fixture.LockExec(t, p.Ctx, c, "SET search_path TO comment_private", 0)
					denied("COMMENT ON TABLE t IS NULL", "42P01", `relation "t" does not exist`)
					run("GRANT USAGE, CREATE ON SCHEMA comment_private TO " + role)
					run("GRANT SELECT,INSERT ON comment_private.t TO " + role)
					denied("COMMENT ON TABLE comment_private.t IS NULL", "42501", "must be owner of table t")
					denied("COMMENT ON COLUMN comment_private.t.v IS NULL", "42501", "must be owner of relation t")
					denied("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42703", `column "missing" of relation "comment_private.t" does not exist`)
					fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT v FROM comment_private.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT obj_description('comment_private.t'::regclass,'pg_class') AS table_text,col_description('comment_private.t'::regclass,1) AS column_text", []string{"table_text", "column_text"}, []string{"TEXT", "TEXT"}, [][]any{{"old", "old column"}}))
				})
			}
		})
	}
}
