package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				run(fixture.Exec(`CREATE DOMAIN domain_s."Δ Domain" AS integer CONSTRAINT "é positive" CHECK(VALUE>0)`, 0))
				for _, tc := range []struct{ q, code, message string }{
					{`ALTER DOMAIN domain_s."Δ Domain" ADD CONSTRAINT "é positive" CHECK(missing>0)`, "42710", `constraint "é positive" for domain "Δ Domain" already exists`},
					{`ALTER DOMAIN domain_s."Δ Domain" DROP CONSTRAINT absent`, "42704", `constraint "absent" of domain "domain_s.Δ Domain" does not exist`},
					{`ALTER DOMAIN domain_s."Δ Domain" VALIDATE CONSTRAINT absent`, "42704", `constraint "absent" of domain "domain_s.Δ Domain" does not exist`},
					{`ALTER DOMAIN domain_s.missing DROP CONSTRAINT IF EXISTS absent`, "42704", `type "domain_s.missing" does not exist`},
					{`ALTER DOMAIN integer ADD CONSTRAINT positive CHECK(VALUE>0)`, "42704", `type "integer" does not exist`},
					{`ALTER DOMAIN pg_catalog.int4 ADD CONSTRAINT positive CHECK(VALUE>0)`, "42809", `integer is not a domain`},
				} {
					t.Run(tc.code+tc.q, func(t *testing.T) {
						s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
				*notices = nil
				s := fixture.Exec(`ALTER DOMAIN domain_s."Δ Domain" DROP CONSTRAINT IF EXISTS absent CASCADE`, 0)
				s.Prepared = prepared
				run(s)
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "absent" of domain "domain_s.Δ Domain" does not exist, skipping`}
				if len(*notices) != 1 || (*notices)[0] != want {
					t.Fatalf("IF EXISTS notices=%+v want=%+v", *notices, want)
				}
			})
		})
	}
	for _, tc := range []struct {
		suffix, code, message, token string
		syntax                       bool
	}{
		{"CHECK(missing>0)", "42703", `column "missing" does not exist`, "", false},
		{"CHECK((SELECT 1)>0)", "0A000", "cannot use subquery in check constraint", "", false},
		{"CHECK(VALUE)", "42804", "argument of CHECK must be type boolean, not type integer", "", false},
		{"CHECK($1>0)", "42P02", "there is no parameter $1", "", false},
		{"CHECK(VALUE >)", "42601", `syntax error at or near ")"`, ")", true},
		{"CHECK(1/0>0)", "22012", "division by zero", "", false},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.suffix+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.d AS integer", 0))
					q := "ALTER DOMAIN /* é */ domain_s.d ADD CONSTRAINT rejected " + tc.suffix
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.token != "" {
						want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
					}
					s := fixture.ErrorDiagnostic(q, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.syntax {
						s.ErrorStage = "prepare"
					}
					// Isolate diagnostics so publication/repair is still checked after a mismatch.
					t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
					fixture.RunStep(t, p.Ctx, c, r, domainAlterFlags("d", [][]any{}))
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT rejected CHECK(VALUE>0)", 0))
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("CHECK(VALUE>0) NO INHERIT"+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.d AS integer", 0))
				// Pinned18.0 accepts ALTER's modifier but stores an inheritable
				// CHECK. CREATE DOMAIN's separate NO INHERIT rejection is unchanged.
				s := fixture.Exec("ALTER DOMAIN /* é */ domain_s.d ADD CONSTRAINT accepted CHECK(VALUE>0) NO INHERIT", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT conname,convalidated,connoinherit FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid", []string{"conname", "convalidated", "connoinherit"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"accepted", true, false}}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.child AS domain_s.d", 0))
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT (-1)::domain_s.child AS v FROM public.one", "child", "accepted", true, false))
			})
		})
	}
	t.Run("unnamed_and_collision", func(t *testing.T) {
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer CONSTRAINT d_check CHECK(VALUE<100)", "ALTER DOMAIN domain_s.d ADD CHECK(VALUE>0) NOT VALID", "ALTER DOMAIN domain_s.d ADD CHECK(VALUE<10)"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, r, domainAlterFlags("d", [][]any{{"d_check", true}, {"d_check1", false}, {"d_check2", true}}))
		})
	})
	t.Run("effective_owner", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"domain_alter_intruder"})
		domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{"CREATE ROLE domain_alter_intruder", "CREATE DOMAIN domain_s.d AS integer", "GRANT USAGE ON SCHEMA domain_s TO domain_alter_intruder", "SET ROLE domain_alter_intruder"} {
				run(fixture.Exec(q, 0))
			}
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			for _, q := range []string{"ADD CONSTRAINT denied CHECK(VALUE>0)", "DROP CONSTRAINT absent", "VALIDATE CONSTRAINT absent"} {
				t.Run(q, func(t *testing.T) {
					s := fixture.Prepared(fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.d "+q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of type domain_s.d")))
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, r, s)
				})
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(domainAlterFlags("d", [][]any{}))
			run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT denied CHECK(VALUE>0)", 0))
		})
	})
}
