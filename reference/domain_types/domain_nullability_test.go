package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainNullability(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.required AS integer NOT NULL",
			"CREATE DOMAIN domain_s.named AS integer CONSTRAINT named_required NOT NULL",
			"CREATE DOMAIN domain_s.child AS domain_s.required NULL",
			"CREATE DOMAIN domain_s.null_default AS integer DEFAULT NULL NOT NULL",
			"CREATE DOMAIN domain_s.nullable AS integer NULL",
			"CREATE DOMAIN domain_s.truth_order AS integer CHECK(VALUE IS NOT NULL) NOT NULL",
			"CREATE DOMAIN domain_s.init_order AS integer NOT NULL CHECK(1/0>0)",
			"CREATE DOMAIN domain_s.\"é Required\" AS integer CONSTRAINT \"é name\" NOT NULL",
			"CREATE TABLE domain_s.required_values(id integer,v domain_s.required)",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, prepared := range []bool{false, true} {
			for _, name := range []string{"required", "named", "child", "null_default", "truth_order", "\"é Required\""} {
				for _, expr := range []string{"NULL::domain_s." + name, "(NULL::integer)::domain_s." + name, "$1::domain_s." + name} {
					args := []any(nil)
					if expr[0] == '$' {
						args = []any{nil}
					}
					d := domainNotNullDiagnostic("domain_s", name)
					if len(args) != 0 {
						d.Context = "unnamed portal parameter $1"
					}
					run(domainDefaultFailure("SELECT /* é */ "+expr+" AS v FROM public.one", d, prepared, true, args...))
				}
			}
			run(domainDefaultFailure("SELECT NULL::domain_s.init_order AS v FROM public.one", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), prepared, true))
			for _, q := range []string{"INSERT INTO domain_s.required_values(id) VALUES(1)", "INSERT INTO domain_s.required_values VALUES(1,DEFAULT)", "INSERT INTO domain_s.required_values VALUES(1,7),(2,NULL)"} {
				run(domainDefaultFailure(q, domainNotNullDiagnostic("domain_s", "required"), prepared, false))
			}
			run(fixture.Select("SELECT id,v FROM domain_s.required_values", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
			s := fixture.Select("SELECT $1::domain_s.required AS v,NULL::domain_s.nullable AS n FROM public.one", []string{"v", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(9), nil}}, int64(9))
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("INSERT INTO domain_s.required_values VALUES(3,9)", 1))
		run(fixture.Select("SELECT r.v FROM public.one o LEFT JOIN domain_s.required_values r ON false", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		run(fixture.Select("SELECT (SELECT v FROM domain_s.required_values WHERE false)::domain_s.required AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		run(fixture.Exec("SET search_path TO domain_s,public", 0))
		d := domainNotNullDiagnostic("domain_s", "required")
		d.Message = "domain required does not allow null values"
		run(domainDefaultFailure("SELECT NULL::required AS v FROM public.one", d, false, true))
	})
}

func TestReferenceDomainDefaultDiagnostics(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		cases := []struct{ suffix, code, message string }{
			{"DEFAULT VALUE", "0A000", "cannot use column reference in DEFAULT expression"},
			{"DEFAULT some_column", "0A000", "cannot use column reference in DEFAULT expression"},
			{"DEFAULT (SELECT 1)", "0A000", "cannot use subquery in DEFAULT expression"},
			{"DEFAULT $1", "42P02", "there is no parameter $1"},
			{"DEFAULT 1 DEFAULT 2", "42601", "multiple default expressions"},
			{"NULL NOT NULL", "42601", "conflicting NULL/NOT NULL constraints"},
			{"NOT NULL NULL", "42601", "conflicting NULL/NOT NULL constraints"},
			{"NOT NULL NOT NULL", "42P17", "redundant NOT NULL constraint definition"},
			{"CONSTRAINT first_default DEFAULT 1 CONSTRAINT second_default DEFAULT 2", "42601", "multiple default expressions"},
			{"CONSTRAINT first_null NULL CONSTRAINT second_null NOT NULL", "42601", "conflicting NULL/NOT NULL constraints"},
			{"CONSTRAINT first_required NOT NULL CONSTRAINT second_required NOT NULL", "42P17", "redundant NOT NULL constraint definition"},
		}
		for _, prepared := range []bool{false, true} {
			for i, tc := range cases {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				positions := []int{51, 51, 51, 51, 53, 48, 52, 52, 78, 70, 78}
				d.Position = fixture.Position(positions[i])
				run(domainDefaultFailure("CREATE DOMAIN domain_s.\"é bad\" AS integer "+tc.suffix, d, prepared, false))
				run(fixture.Select("SELECT typname AS name FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND (typname='é bad' OR typname='_é bad')", []string{"name"}, []string{"NAME"}, nil))
				run(fixture.Exec("CREATE DOMAIN domain_s.\"é bad\" AS integer DEFAULT 7", 0))
				run(fixture.Exec("DROP DOMAIN domain_s.\"é bad\"", 0))
			}
		}
		run(fixture.Exec("CREATE DOMAIN domain_s.repeated_null AS integer CONSTRAINT a NULL CONSTRAINT b NULL", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.named_default AS integer CONSTRAINT d DEFAULT 7", 0))
		run(fixture.Exec("CREATE TABLE domain_s.named_success(a domain_s.named_default,b domain_s.repeated_null)", 0))
		run(fixture.Exec("INSERT INTO domain_s.named_success DEFAULT VALUES", 1))
		run(fixture.Select("SELECT a,b FROM domain_s.named_success", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), nil}}))
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
			d.Position = "55"
			run(domainDefaultFailure("CREATE DOMAIN domain_s.bad_default AS integer DEFAULT 'bad'", d, prepared, false))
			d = fixture.ControlsReferenceDiagnostic("42804", `column "bad_default" is of type integer but default expression is of type boolean`)
			d.Hint = "You will need to rewrite or cast the expression."
			run(domainDefaultFailure("CREATE DOMAIN domain_s.bad_default AS integer DEFAULT true", d, prepared, false))
			s := fixture.Exec("CREATE DOMAIN domain_s.bad_default AS varchar(3) DEFAULT 'abcd'", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("CREATE TABLE domain_s.bad_use(v domain_s.bad_default)", 0))
			run(domainDefaultFailure("INSERT INTO domain_s.bad_use DEFAULT VALUES", fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(3)"), prepared, false))
			run(fixture.Exec("DROP TABLE domain_s.bad_use", 0))
			run(fixture.Exec("DROP DOMAIN domain_s.bad_default", 0))
		}
		run(fixture.Exec("CREATE DOMAIN domain_s.cook_required AS integer NOT NULL", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.cook_check AS integer CHECK(VALUE>0)", 0))
		// DDL cooks against the immediate base, but constraints execute on demand
		// and identify that base (not the newly declared outer domain).
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ name, base, expr, deparse string }{{"cook_null", "cook_required", "NULL", "NULL::integer"}, {"cook_zero", "cook_check", "0", "0"}} {
				q := "CREATE DOMAIN domain_s." + tc.name + " AS domain_s." + tc.base + " DEFAULT " + tc.expr
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT typname,typdefault FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname='"+tc.name+"'", []string{"typname", "typdefault"}, []string{"NAME", "TEXT"}, [][]any{{tc.name, tc.deparse}}))
				run(fixture.Exec("CREATE TABLE domain_s.cook_use(v domain_s."+tc.name+")", 0))
				d := domainNotNullDiagnostic("domain_s", "cook_required")
				if tc.name == "cook_zero" {
					d = domainCheckDiagnostic("cook_check", "cook_check_check")
				}
				run(domainDefaultFailure("INSERT INTO domain_s.cook_use DEFAULT VALUES", d, prepared, false))
				run(fixture.Exec("DROP TABLE domain_s.cook_use", 0))
				run(fixture.Exec("DROP DOMAIN domain_s."+tc.name, 0))
			}
		}
	})
}
