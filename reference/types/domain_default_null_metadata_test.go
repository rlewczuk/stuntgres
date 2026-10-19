package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainDefaultNullMetadata(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		for _, q := range []string{
			"CREATE DOMAIN domain_s.dd_null_plain AS integer DEFAULT NULL",
			"CREATE DOMAIN domain_s.dd_null_base AS integer DEFAULT 11",
			"CREATE DOMAIN domain_s.dd_null_mask AS domain_s.dd_null_base DEFAULT NULL",
			"CREATE DOMAIN domain_s.dd_null_numeric AS numeric(5,2) DEFAULT NULL",
			"CREATE TABLE domain_s.dd_null_rows(v domain_s.dd_null_plain,m domain_s.dd_null_mask,n domain_s.dd_null_numeric)",
		} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO domain_s.dd_null_rows DEFAULT VALUES", 1))
		for _, prepared := range []bool{false, true} {
			step := fixture.Select("SELECT v,m,n FROM domain_s.dd_null_rows", []string{"v", "m", "n"}, []string{"INT4", "INT4", "NUMERIC"}, [][]any{{nil, nil, nil}})
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, r, step)
		}
		for _, prepared := range []bool{false, true} {
			// Only the final bare Const NULL is elided. The inherited-domain
			// conversion and numeric typmod wrapper remain real recipes.
			step := fixture.Select("SELECT t.typname,t.typdefault,t.typdefaultbin IS NOT NULL AS treepresent FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typtype='d' ORDER BY t.typname",
				[]string{"typname", "typdefault", "treepresent"}, []string{"NAME", "TEXT", "BOOL"},
				[][]any{
					{"dd_null_base", "11", true},
					{"dd_null_mask", "NULL::integer", true},
					{"dd_null_numeric", "NULL::numeric", true},
					{"dd_null_plain", nil, false},
				})
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, r, step)
		}
	})
}
