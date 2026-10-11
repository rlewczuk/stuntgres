package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGIndexesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{fixture.PgIndexesProbe, "SELECT * FROM pg_catalog.pg_indexes WHERE FALSE", "SELECT * FROM pg_catalog.pg_indexes LIMIT 0"} {
				s := fixture.PgIndexesSelect(b, q, nil)
				if q == fixture.PgIndexesProbe {
					s.Args = []any{"pgi_shape"}
				}
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("CREATE TABLE pgi_shape_table(n INT)", 0))
			run(fixture.Exec("CREATE INDEX pgi_shape ON pgi_shape_table(n)", 0))
			row := fixture.PgIndexesRow("public", "pgi_shape_table", "pgi_shape", "CREATE INDEX pgi_shape ON public.pgi_shape_table USING btree (n)")
			run(fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, [][]any{row}, "pgi_shape"))
			run(fixture.Prepared(fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, [][]any{row}, "pgi_shape")))
			for _, arg := range []any{"pgi_absent", nil} {
				run(fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, nil, arg))
				run(fixture.Prepared(fixture.PgIndexesSelect(b, fixture.PgIndexesProbe, nil, arg)))
			}
		})
	}
}
