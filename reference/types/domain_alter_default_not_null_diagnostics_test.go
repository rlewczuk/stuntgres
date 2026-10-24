package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterDefaultDiagnostics(t *testing.T) {
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec(`CREATE DOMAIN domain_s."é d" AS integer DEFAULT 7`, 0))
		for _, tc := range []struct {
			expr, code, message, token, hint string
			syntax                           bool
		}{
			{"VALUE", "0A000", "cannot use column reference in DEFAULT expression", "", "", false},
			{"some_column", "0A000", "cannot use column reference in DEFAULT expression", "", "", false},
			{"(SELECT 1)", "0A000", "cannot use subquery in DEFAULT expression", "", "", false},
			{"$1", "42P02", "there is no parameter $1", "", "", false},
			{"true", "42804", `column "é d" is of type integer but default expression is of type boolean`, "", "You will need to rewrite or cast the expression.", false},
			{"7, DROP DEFAULT", "42601", `syntax error at or near ","`, ",", "", true},
			{"7 nonsense", "42601", `syntax error at or near "nonsense"`, "nonsense", "", true},
		} {
			for _, prepared := range []bool{false, true} {
				q := `/*é*/ ALTER DOMAIN domain_s."é d" SET DEFAULT ` + tc.expr
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				if tc.token != "" {
					d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				}
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared && tc.syntax {
					s.ErrorStage = "prepare"
				}
				t.Run(q+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
				run(fixture.Select(`SELECT typdefault,typdefaultbin IS NOT NULL AS recipe FROM pg_type WHERE oid='domain_s."é d"'::regtype::oid`, []string{"typdefault", "recipe"}, []string{"TEXT", "BOOL"}, [][]any{{"7", true}}))
			}
		}
		// Bare cooked builtin NULL clears; immediate-domain and typmod wrappers
		// remain recipes. ALTER never interprets nil raw syntax as inheritance.
		for _, q := range []string{
			"CREATE DOMAIN domain_s.base AS integer DEFAULT 7 NOT NULL",
			"CREATE DOMAIN domain_s.inner_null AS domain_s.base",
			"CREATE DOMAIN domain_s.bare AS integer DEFAULT 7",
			"CREATE DOMAIN domain_s.num AS numeric(4,1) DEFAULT 7",
			"ALTER DOMAIN domain_s.inner_null SET DEFAULT NULL",
			"ALTER DOMAIN domain_s.bare SET DEFAULT NULL",
			"ALTER DOMAIN domain_s.num SET DEFAULT NULL",
			"CREATE TABLE domain_s.demand(v domain_s.inner_null)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select("SELECT typname,typdefault,typdefaultbin IS NOT NULL AS recipe FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND typname IN('bare','inner_null','num') ORDER BY typname", []string{"typname", "typdefault", "recipe"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"bare", nil, false}, {"inner_null", "NULL::integer", true}, {"num", "NULL::numeric", true}}))
		run(domainDefaultFailure("INSERT INTO domain_s.demand DEFAULT VALUES", domainNotNullDiagnostic("domain_s", "base"), false, false))
		run(fixture.Exec("ALTER DOMAIN domain_s.inner_null DROP DEFAULT", 0))
		run(domainDefaultFailure("INSERT INTO domain_s.demand DEFAULT VALUES", domainNotNullDiagnostic("domain_s", "inner_null"), true, false))
	})
}

func TestReferenceDomainAlterNotNullDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"domain_alter_values_actor"})
	domainAlterValueScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.d AS integer DEFAULT 7", "CREATE ROLE domain_alter_values_actor", "GRANT USAGE ON SCHEMA domain_s TO domain_alter_values_actor"} {
			run(fixture.Exec(q, 0))
		}
		for _, action := range []string{"SET DEFAULT 11", "DROP DEFAULT", "SET NOT NULL", "DROP NOT NULL"} {
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ target, code, message string }{
					{"domain_s.missing", "42704", `type "domain_s.missing" does not exist`},
					{"pg_catalog.int4", "42809", "integer is not a domain"},
				} {
					s := fixture.ErrorDiagnostic("ALTER DOMAIN "+tc.target+" "+action, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
					s.Prepared, s.ErrorStage = prepared, "execute"
					t.Run(tc.target+action+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
				}
				run(fixture.Exec("SET ROLE domain_alter_values_actor", 0))
				s := fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.d "+action, fixture.ControlsReferenceDiagnostic("42501", "must be owner of type domain_s.d"))
				s.Prepared, s.ErrorStage = prepared, "execute"
				t.Run("owner_"+action+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
				run(fixture.Exec("RESET ROLE", 0))
			}
			// A held utility prepares quietly, changes only its owner, and rolls back.
			t.Run("transaction_"+action, func(t *testing.T) {
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				q := "ALTER DOMAIN domain_s.d " + action
				stmt, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("quiet Prepare", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
				s := fixture.Exec(q, 0)
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, r, s)
				wantDefault, wantNull := any("7"), false
				switch action {
				case "SET DEFAULT 11":
					wantDefault = "11"
				case "DROP DEFAULT":
					wantDefault = nil
				case "SET NOT NULL":
					wantNull = true
				}
				fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("SELECT typdefault,typnotnull FROM pg_type WHERE oid='domain_s.d'::regtype::oid", []string{"typdefault", "typnotnull"}, []string{"TEXT", "BOOL"}, [][]any{{wantDefault, wantNull}}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("readonly_"+action, func(t *testing.T) {
				tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				s := fixture.Prepared(fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.d "+action, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER DOMAIN in a read-only transaction")))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, tx, r, s)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			})
		}
		run(fixture.Select("SELECT typdefault,typnotnull FROM pg_type WHERE oid='domain_s.d'::regtype::oid", []string{"typdefault", "typnotnull"}, []string{"TEXT", "BOOL"}, [][]any{{"7", false}}))
		// A genuine namespace denial is not disguised as a type owner failure.
		run(fixture.Exec("REVOKE USAGE ON SCHEMA domain_s FROM domain_alter_values_actor", 0))
		run(fixture.Exec("SET ROLE domain_alter_values_actor", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		q := "ALTER DOMAIN domain_s.d SET NOT NULL"
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema domain_s")
		t.Run("namespace_usage", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic(q, d)) })
		run(fixture.Exec("RESET ROLE", 0))
		for _, q := range []string{"CREATE DOMAIN domain_s.secret AS integer", "REVOKE USAGE ON TYPE domain_s.secret FROM PUBLIC", "CREATE DOMAIN domain_s.outer_d AS domain_s.secret", "ALTER DOMAIN domain_s.outer_d OWNER TO domain_alter_values_actor", "GRANT USAGE ON SCHEMA domain_s TO domain_alter_values_actor", "SET ROLE domain_alter_values_actor"} {
			run(fixture.Exec(q, 0))
		}
		t.Run("base_type_usage", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("ALTER DOMAIN domain_s.outer_d SET DEFAULT 2", fixture.ControlsReferenceDiagnostic("42501", "permission denied for type domain_s.secret")))
		})
		run(fixture.Exec("RESET ROLE", 0))
	})
}
