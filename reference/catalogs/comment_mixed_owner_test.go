package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCommentMixedOwnerRoundtrip(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, texts := range []struct{ name, table, a, b string }{{"table", "table text", "", ""}, {"columns", "", "first", "second"}, {"both", "table text", "first", "second"}} {
			t.Run(fmt.Sprintf("%s/held=%v", texts.name, held), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"comment_table_owner", "comment_schema_owner"})
				fixture.SchemaReferenceCleanup(t, "comment_mixed")
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(q string) { fixture.LockExec(t, p.Ctx, c, q, 0) }
						for _, q := range []string{"CREATE USER comment_table_owner", "CREATE USER comment_schema_owner", "CREATE SCHEMA comment_mixed AUTHORIZATION comment_table_owner", "SET SESSION AUTHORIZATION comment_table_owner", "CREATE TABLE comment_mixed.t(a INT,b TEXT)", "RESET SESSION AUTHORIZATION", "ALTER SCHEMA comment_mixed OWNER TO comment_schema_owner", "COMMENT ON ROLE comment_table_owner IS 'shared table owner'", "COMMENT ON ROLE comment_schema_owner IS 'shared schema owner'"} {
							run(q)
						}
						fixture.LockExec(t, p.Ctx, c, "INSERT INTO comment_mixed.t VALUES(7,'sentinel')", 1)
						for _, v := range []struct{ target, text string }{{"TABLE comment_mixed.t", texts.table}, {"COLUMN comment_mixed.t.a", texts.a}, {"COLUMN comment_mixed.t.b", texts.b}} {
							if v.text == "" {
								continue
							}
							q := "COMMENT ON " + v.target + " IS '" + v.text + "'"
							if held {
								s, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								x := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
								fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
								s.Close()
							} else {
								run(q)
							}
						}
						if !b.PG {
							dump := commentInquiryDump(t, p)
							if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
								t.Fatal(err)
							}
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
								t.Fatal(err)
							}
							if got := commentInquiryDump(t, p); got != dump {
								t.Fatal("canonical replay changed", got, dump)
							}
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a,b FROM comment_mixed.t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "sentinel"}}))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n.nspowner=(SELECT oid FROM pg_roles WHERE rolname='comment_schema_owner') AS schema_owner,c.relowner=(SELECT oid FROM pg_roles WHERE rolname='comment_table_owner') AS table_owner,n.nspacl IS NULL AS schema_acl,c.relacl IS NULL AS table_acl FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='comment_mixed.t'::regclass", []string{"schema_owner", "table_owner", "schema_acl", "table_acl"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
						for _, v := range []struct{ expr, text string }{{"obj_description('comment_mixed.t'::regclass,'pg_class')", texts.table}, {"col_description('comment_mixed.t'::regclass,1)", texts.a}, {"col_description('comment_mixed.t'::regclass,2)", texts.b}} {
							var want any
							if v.text != "" {
								want = v.text
							}
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+v.expr+" AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT rolname,shobj_description(oid,'pg_authid') AS text FROM pg_roles WHERE rolname IN ('comment_schema_owner','comment_table_owner') ORDER BY rolname", []string{"rolname", "text"}, []string{"NAME", "TEXT"}, [][]any{{"comment_schema_owner", "shared schema owner"}, {"comment_table_owner", "shared table owner"}}))
						run("SET SESSION AUTHORIZATION comment_table_owner")
						defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.RoleReferenceError("COMMENT ON TABLE comment_mixed.t IS 'denied'", "42501", "permission denied for schema comment_mixed", "", false, "execute"))
						s := fixture.ImplicitError("SELECT a FROM comment_mixed.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema comment_mixed", Position: "15"})
						s.SelectRows = true
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					})
				}
			})
		}
	}
}
