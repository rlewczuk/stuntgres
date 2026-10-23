package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// get_with_clause/get_setop_query in REL_18_6 ruleutils.c ground these full
// candidates independently of the stored-rule replay renderer. CREATE RULE
// admission, generated-column references and exact layout need the pinned oracle.
func TestReferencePGRulesRecursiveDefinitions(t *testing.T) {
	cases := []struct {
		name, ddl, definition string
	}{
		{
			name:       "search_and_default_cycle_identifiers",
			ddl:        "CREATE RULE r AS ON INSERT TO pgr_src DO WITH RECURSIVE walk(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM walk WHERE n<2) SEARCH DEPTH FIRST BY n SET seq CYCLE n SET cyc USING path SELECT n,seq,cyc,path FROM walk",
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  WITH RECURSIVE walk(n) AS (\n         SELECT 1 AS \"?column?\"\n        UNION ALL\n         SELECT (walk_1.n + 1)\n           FROM walk walk_1\n          WHERE (walk_1.n < 2)\n        ) SEARCH DEPTH FIRST BY n SET seq CYCLE n SET cyc USING path\n SELECT walk.n,\n    walk.seq,\n    walk.cyc,\n    walk.path\n   FROM walk;",
		},
		{
			name:       "quoted_cycle_custom_text_marks",
			ddl:        `CREATE RULE r AS ON INSERT TO pgr_src DO WITH RECURSIVE "Walk"("N") AS (SELECT 1 UNION ALL SELECT "N" FROM "Walk") CYCLE "N" SET "Mark" TO 'Y' DEFAULT 'N' USING "Path" SELECT "N","Mark","Path" FROM "Walk"`,
			definition: "CREATE RULE r AS\n    ON INSERT TO public.pgr_src DO  WITH RECURSIVE \"Walk\"(\"N\") AS (\n         SELECT 1 AS \"?column?\"\n        UNION ALL\n         SELECT \"Walk_1\".\"N\"\n           FROM \"Walk\" \"Walk_1\"\n        ) CYCLE \"N\" SET \"Mark\" TO 'Y'::text DEFAULT 'N'::text USING \"Path\"\n SELECT \"Walk\".\"N\",\n    \"Walk\".\"Mark\",\n    \"Walk\".\"Path\"\n   FROM \"Walk\";",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					for _, q := range []string{
						"CREATE TABLE pgr_src(id INT,v INT)",
						"CREATE TABLE pgr_log(id INT,v INT)",
						c.ddl,
					} {
						run(fixture.Exec(q, 0))
					}
					pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_src", "r", c.definition}}, "public", "pgr_src", "r")
					// Presentation does not execute the recursive action or event.
					for _, table := range []string{"pgr_src", "pgr_log"} {
						run(fixture.Select("SELECT count(*) FROM "+table, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
		})
	}
}
