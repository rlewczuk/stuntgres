package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCatalogInputs(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(id INT)", 0),
				fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0),
				fixture.Prepared(fixture.Select("SELECT rulename FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename"}, []string{"NAME"}, nil, "-1")),
				fixture.Prepared(fixture.Select("SELECT rulename FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename"}, []string{"NAME"}, nil, "-2147483648")),
				fixture.Prepared(fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, "vv")),
				fixture.Prepared(fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, `\166`)),
				fixture.Prepared(fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='v' AND relkind<$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}, "é")),
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			}
		})
	}
}
