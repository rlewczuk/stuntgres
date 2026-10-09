package reference_test

import "testing"

// Full, column-only and pretty definitions are independently pinned by the
// PostgreSQL18 oracle; no expectation is learned from native rendering.
func TestReferenceIndexMaintenanceExpressionDeparse(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		for _, q := range []string{"CREATE TABLE t(n INT,s TEXT)", "CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", "CREATE INDEX folded ON t((n+(2-1)))"} {
			run(referenceExec(q, 0))
		}
		run(referenceExec(`CREATE INDEX ci ON t((s COLLATE "C"),(s COLLATE "POSIX"),s)`, 0))
		cases := []struct{ call, want string }{
			{"pg_get_indexdef('ei'::regclass)", "CREATE INDEX ei ON public.t USING btree (lower(s), ((n + 1)), n DESC NULLS LAST)"},
			{"pg_get_indexdef('ei'::regclass,0,false)", "CREATE INDEX ei ON public.t USING btree (lower(s), ((n + 1)), n DESC NULLS LAST)"},
			{"pg_get_indexdef('ei'::regclass,0,true)", "CREATE INDEX ei ON t USING btree (lower(s), (n + 1), n DESC NULLS LAST)"},
			{"pg_get_indexdef('ei'::regclass,1,false)", "lower(s)"},
			{"pg_get_indexdef('ei'::regclass,1,true)", "lower(s)"},
			{"pg_get_indexdef('ei'::regclass,2,false)", "((n + 1))"},
			{"pg_get_indexdef('ei'::regclass,2,true)", "(n + 1)"},
			{"pg_get_indexdef('ei'::regclass,3,false)", "n"},
			{"pg_get_indexdef('folded'::regclass,1,false)", "((n + (2 - 1)))"},
			{"pg_get_indexdef('ci'::regclass)", `CREATE INDEX ci ON public.t USING btree (s COLLATE "C", s COLLATE "POSIX", s)`},
			{"pg_get_indexdef('ci'::regclass,1,false)", "s"},
			{"pg_get_indexdef('ci'::regclass,1,true)", "s"},
		}
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				route := "/direct"
				if prepared {
					route = "/prepared"
				}
				t.Run(c.call+route, func(t *testing.T) {
					step := referenceSelect("SELECT "+c.call+" AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{c.want}})
					if prepared {
						step = referencePrepared(step)
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, step)
				})
			}
		}
		run(referenceExec("ALTER TABLE t RENAME COLUMN s TO renamed_s", 0))
		run(referenceExec("ALTER INDEX ei RENAME TO renamed_ei", 0))
		run(referenceExec("REINDEX TABLE t", 0))
		t.Run("renamed_expression", func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(referenceSelect("SELECT pg_get_indexdef('renamed_ei'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX renamed_ei ON public.t USING btree (lower(renamed_s), ((n + 1)), n DESC NULLS LAST)"}})))
		})
		t.Run("renamed_collated_attributes", func(t *testing.T) {
			referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(referenceSelect("SELECT pg_get_indexdef('ci'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX ci ON public.t USING btree (renamed_s COLLATE "C", renamed_s COLLATE "POSIX", renamed_s)`}})))
		})
	})
}
