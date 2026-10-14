package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckDiagnostics(t *testing.T) {
	for _, spelling := range []string{"VALUE", "value", `"value"`} {
		t.Run("accepted_"+spelling, func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.spelling AS integer CHECK ("+spelling+">0)", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT 1::domain_s.spelling AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	for _, tc := range []struct{ expression, state, message string }{
		{`"VALUE">0`, "42703", `column "VALUE" does not exist`},
		{"missing>0", "42703", `column "missing" does not exist`},
		{"t.value>0", "42P01", `missing FROM-clause entry for table "t"`},
		{"(SELECT 1)>0", "0A000", "cannot use subquery in check constraint"},
		{"EXISTS (SELECT 1)", "0A000", "cannot use subquery in check constraint"},
		{"VALUE IN (SELECT 1)", "0A000", "cannot use subquery in check constraint"},
		{"VALUE", "42804", "argument of CHECK must be type boolean, not type integer"},
		{"$1>0", "42P02", "there is no parameter $1"},
	} {
		for _, utf8 := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.expression+map[bool]string{false: "_ascii", true: "_utf8"}[utf8]+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
					domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
						prefix := "CREATE DOMAIN domain_s.rejected AS integer CHECK ("
						if utf8 {
							prefix = "CREATE DOMAIN /* é */ domain_s.rejected AS integer CHECK ("
						}
						q := prefix + tc.expression + ")"
						want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
						// PG18.0 omits Position for domain CHECK binding errors,
						// including UTF8-prefixed and prepared declarations.
						s := fixture.ErrorDiagnostic(q, want)
						s.Prepared, s.ErrorStage = prepared, "execute"
						// Check publication independently even if diagnostics regress.
						t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
						fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.rejected AS integer CHECK (VALUE>0)", 0))
					})
				})
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "evaluation_direct", true: "evaluation_prepared"}[prepared], func(t *testing.T) {
			domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.divide AS integer CHECK (10/VALUE>0)", 0))
				s := fixture.ErrorDiagnostic("SELECT 0::domain_s.divide AS v FROM public.one", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
			})
		})
	}
	for _, tc := range []struct{ suffix, state, message, position string }{
		{" CHECK (VALUE>0) NO INHERIT", "42P17", "check constraints for domains cannot be marked NO INHERIT", "44"},
		{" CHECK (VALUE>0) NOT VALID", "42601", `syntax error at or near "VALID"`, "64"},
		{" UNIQUE", "42601", "unique constraints not possible for domains", "44"},
		{" REFERENCES public.one(v)", "42601", "foreign key constraints not possible for domains", "44"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run("boundary_"+tc.suffix+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
				domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
					want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
					want.Position = tc.position
					s := fixture.ErrorDiagnostic("CREATE DOMAIN domain_s.boundary AS integer"+tc.suffix, want)
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared && tc.suffix == " CHECK (VALUE>0) NOT VALID" {
						s.ErrorStage = "prepare"
					}
					t.Run("diagnostic", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.boundary AS integer CHECK (TRUE)", 0))
				})
			})
		}
	}
	// PG18.0 plans constant division before the earlier FALSE check can run.
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.constant_error AS integer CONSTRAINT a_false CHECK (FALSE) CONSTRAINT z_error CHECK (1/0>0)", 0))
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "constant_direct", true: "constant_prepared"}[prepared], func(t *testing.T) {
				s := fixture.ErrorDiagnostic("SELECT 1::domain_s.constant_error AS v FROM public.one", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, r, s)
			})
		}
	})
	t.Run("qualification_and_location", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN domain_s.located AS integer CONSTRAINT positive CHECK (VALUE>0)", 0))
			for _, visible := range []bool{false, true} {
				if visible {
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO domain_s,public", 0))
				}
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{
						"SELECT /* é */ 0::domain_s.located AS v FROM public.one",
						"SELECT /* é */ '0'::domain_s.located AS v FROM public.one",
					} {
						t.Run(q+map[bool]string{false: "_qualified", true: "_visible"}[visible]+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
							s := domainCheckFailure(q, "located", "positive", true, prepared)
							if visible {
								s.Diagnostic.Message = `value for domain located violates check constraint "positive"`
							}
							fixture.RunStep(t, p.Ctx, c, r, s)
						})
					}
				}
			}
		})
	})
}
