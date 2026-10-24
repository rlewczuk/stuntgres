package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryAliasesPostfixCastKeywords(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(7)", 1))
			for _, tc := range []struct {
				query string
				alias string
				value int64
			}{
				{"SELECT v::int4 unknown FROM t", "unknown", 7},
				{"SELECT v::int4 delete FROM t", "delete", 7},
				{"SELECT 1::int4 reindex", "reindex", 1},
				{"SELECT 1::int4 index", "index", 1},
			} {
				t.Run(tc.alias, func(t *testing.T) {
					step := fixture.Select(tc.query, []string{tc.alias}, []string{"INT4"}, [][]any{{tc.value}})
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				})
			}
		})
	}
}
