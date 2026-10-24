package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceOwnership(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner := fmt.Sprintf("replace_owner_%v", prepared)
			actor := fmt.Sprintf("replace_actor_%v", prepared)
			successor := fmt.Sprintf("replace_successor_%v", prepared)
			fixture.RoleReferenceCleanup(t, []string{actor, successor, owner})
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, base := fixture.QueryTableCreateSession(t, p, b)
					run := func(s fixture.Step) { s.Prepared = prepared; base(s) }
					admin, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					grant := func(q string) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(q, 0)) }
					for _, q := range []string{"CREATE USER " + owner, "CREATE USER " + actor, "CREATE USER " + successor, "CREATE TABLE t(id INT)"} {
						run(fixture.Exec(q, 0))
					}
					grant("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC")
					grant("GRANT SELECT ON t TO " + owner)
					defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id FROM t", "ALTER VIEW v RENAME TO renamed", "DROP VIEW v"} {
						run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of view v"}))
					}
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(fixture.ImplicitError("ALTER VIEW v OWNER TO "+successor, fixture.Diagnostic{Code: "42501", Message: "must be able to SET ROLE \"" + successor + "\""}))
					grant("GRANT " + successor + " TO " + owner + " WITH SET TRUE, INHERIT FALSE")
					grant("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
					grant("GRANT CREATE ON SCHEMA public TO " + owner)
					run(fixture.ImplicitError("ALTER VIEW v OWNER TO "+successor, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema public"}))
					grant("GRANT CREATE ON SCHEMA public TO " + successor)
					run(fixture.Exec("ALTER VIEW v OWNER TO "+successor, 0))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					grant("GRANT " + successor + " TO " + actor + " WITH INHERIT TRUE, SET FALSE")
					run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
					run(fixture.Exec("ALTER VIEW v SET(security_barrier=true)", 0))
					run(fixture.Exec("DROP VIEW v", 0))
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				})
			}
		})
	}
}

func TestReferenceViewReplaceNotices(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, tc := range []struct{ query, message string }{
				{"ALTER VIEW IF EXISTS absent RENAME TO z", "relation \"absent\" does not exist, skipping"},
				{"DROP VIEW IF EXISTS absent", "view \"absent\" does not exist, skipping"},
				{"DROP VIEW IF EXISTS absent_schema.v", "schema \"absent_schema\" does not exist, skipping"},
			} {
				fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec(tc.query, 0))
				want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatalf("notices got %#v want %#v", notices, want)
				}
				notices = nil
			}
		})
	}
}
