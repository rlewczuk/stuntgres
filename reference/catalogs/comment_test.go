package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCommentRoundtrip(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "comment_roundtrip_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{role})
			fixture.SchemaReferenceCleanup(t, "comment_scope")
			fixture.SchemaReferenceCleanup(t, "comment_renamed")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					pgDescriptionPreserveSharedRole(t, p, b)
					c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(q string) {
						s := fixture.Exec(q, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
					probe := func(q string, want any) {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q+" AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
					}
					run("CREATE ROLE " + role)
					run(`CREATE TABLE "t.dot"(a INT,b TEXT)`)
					targets := []struct{ target, query string }{
						{`TABLE "t.dot"`, `SELECT obj_description('public."t.dot"'::regclass,'pg_class')`},
						{`COLUMN "t.dot".a`, `SELECT col_description('public."t.dot"'::regclass,1)`},
						{`COLUMN "t.dot".b`, `SELECT col_description('public."t.dot"'::regclass,2)`},
						{`SCHEMA public`, `SELECT obj_description('public'::regnamespace,'pg_namespace')`},
						{`ROLE postgres`, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='postgres'`},
						{`ROLE ` + role, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='` + role + `'`},
					}
					values := []struct {
						literal string
						want    any
					}{{`E'quote\' slash\\\n雪'`, "quote' slash\\\n雪"}, {`'replacement'`, "replacement"}, {`NULL`, nil}, {`''`, nil}, {`' '`, " "}}
					for _, target := range targets {
						for _, value := range values {
							run("COMMENT ON " + target.target + " IS " + value.literal)
							// A FROM clause needs the alias on the expression, not after WHERE.
							var got sql.NullString
							if err := c.QueryRowContext(p.Ctx, target.query).Scan(&got); err != nil {
								t.Fatal(err)
							}
							want, valid := value.want.(string)
							if got.Valid != valid || got.String != want {
								t.Fatalf("%s: %#v want %#v", target.target, got, value.want)
							}
						}
					}
					var oldID, newID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&oldID); err != nil {
						t.Fatal(err)
					}
					run("COMMENT ON ROLE " + role + " IS 'drop dependency must not exist'")
					run("DROP ROLE " + role)
					run("CREATE ROLE " + role)
					if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&newID); err != nil || newID <= 0 || newID == oldID {
						t.Fatal("role identity", oldID, newID, err)
					}
					probe(fmt.Sprintf("SELECT shobj_description(%d,'pg_authid')", newID), nil)
					run("COMMENT ON ROLE " + role + " IS 'new identity'")
					probe(fmt.Sprintf("SELECT shobj_description(%d,'pg_authid')", newID), "new identity")
					probe(fmt.Sprintf("SELECT shobj_description(%d,'pg_authid')", oldID), nil)
					run(`CREATE TABLE comment_copy(LIKE "t.dot")`)
					probe(`SELECT obj_description('comment_copy'::regclass,'pg_class')`, nil)
					probe(`SELECT col_description('comment_copy'::regclass,1)`, nil)
					probe(`SELECT col_description('comment_copy'::regclass,2)`, nil)
					run(`DROP TABLE "t.dot"`)
					run(`CREATE TABLE "t.dot"(a INT,b TEXT)`)
					probe(targets[0].query, nil)
					run("CREATE SCHEMA comment_scope CREATE TABLE t(v INT)")
					run("COMMENT ON SCHEMA comment_scope IS 'schema lifecycle'")
					run("COMMENT ON TABLE comment_scope.t IS 'table lifecycle'")
					run("COMMENT ON COLUMN comment_scope.t.v IS 'column lifecycle'")
					run("ALTER SCHEMA comment_scope RENAME TO comment_renamed")
					run("ALTER SCHEMA comment_renamed OWNER TO " + role)
					probe(`SELECT obj_description('comment_renamed'::regnamespace,'pg_namespace')`, "schema lifecycle")
					probe(`SELECT obj_description('comment_renamed.t'::regclass,'pg_class')`, "table lifecycle")
					probe(`SELECT col_description('comment_renamed.t'::regclass,1)`, "column lifecycle")
					run("DROP SCHEMA comment_renamed CASCADE")
					run("CREATE SCHEMA comment_renamed CREATE TABLE t(v INT)")
					probe(`SELECT obj_description('comment_renamed'::regnamespace,'pg_namespace')`, nil)
					run("DROP SCHEMA comment_renamed CASCADE")
				})
			}
		})
	}
}

func TestReferenceCommentLookup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
			for _, tc := range []struct{ q, code, message string }{
				{"COMMENT ON TABLE missing IS NULL", "42P01", `relation "missing" does not exist`},
				{"COMMENT ON TABLE public.missing IS NULL", "42P01", `relation "public.missing" does not exist`},
				{"COMMENT ON TABLE missing_schema.t IS NULL", "3F000", `schema "missing_schema" does not exist`},
				{"COMMENT ON COLUMN missing_schema.t.v IS NULL", "3F000", `schema "missing_schema" does not exist`},
				{"COMMENT ON COLUMN t.missing IS NULL", "42703", `column "missing" of relation "t" does not exist`},
				{"COMMENT ON COLUMN public.t.missing IS NULL", "42703", `column "missing" of relation "public.t" does not exist`},
				{"COMMENT ON COLUMN a IS NULL", "42601", "column name must be qualified"},
				{"COMMENT ON SCHEMA missing IS NULL", "3F000", `schema "missing" does not exist`},
				{"COMMENT ON ROLE missing IS NULL", "42704", `role "missing" does not exist`},
				{"COMMENT ON ROLE public IS NULL", "42704", `role "public" does not exist`},
			} {
				p.Step(t, fixture.RoleReferenceError(tc.q, tc.code, tc.message, "", prepared, "execute"))
			}
			invalid := fixture.RoleReferenceError("COMMENT ON TABLE t IS 7", "42601", `syntax error at or near "7"`, "", prepared, "prepare")
			invalid.Diagnostic.Position = "23"
			p.Step(t, invalid)
		})
	}
}
