package reference_test

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func explainStoredAliasLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			viewSQL := func(base string) string {
				return fmt.Sprintf(`CREATE OR REPLACE VIEW explain_alias_view AS
					SELECT "left side"."left key" AS l,"right side"."right key" AS r,
					       "derived side"."derived key" AS d,"function side"."function key" AS g,
					       "values side"."values key" AS v
					FROM %[1]s AS "left side"("left key")
					CROSS JOIN %[1]s AS "right side"("right key")
					CROSS JOIN (SELECT id FROM %[1]s) AS "derived side"("derived key")
					CROSS JOIN generate_series(1,1) AS "function side"("function key")
					CROSS JOIN (VALUES(1)) AS "values side"("values key")`, base)
			}
			run(fixture.Exec("CREATE TABLE explain_alias_base(id integer)", 0))
			run(fixture.Exec("INSERT INTO explain_alias_base VALUES(1)", 1))
			run(fixture.Exec(viewSQL("explain_alias_base"), 0))
			const query = "EXPLAIN (VERBOSE) SELECT l,r,d,g,v FROM explain_alias_view"
			held, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var retained, snapshot []string
			assertPlan := func(lines []string, base string) {
				t.Helper()
				if b.PG {
					// Inlining, join order and physical methods belong to PG.
					explainContains(t, lines, base)
				} else {
					explainContains(t, lines, base,
						`"left side"."left key"`, `"right side"."right key"`,
						`"derived side"."derived key"`, `"function side"."function key"`,
						`"values side"."values key"`)
				}
			}
			check := func(phase, base string) {
				t.Helper()
				t.Run(phase, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						assertPlan(explainQuery(t, p, b, query, prepared), base)
					}
					rows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					lines := explainTextRows(t, rows)
					assertPlan(lines, base)
					if retained == nil {
						retained = lines
						snapshot = append([]string(nil), lines...)
					}
					run(fixture.Select("SELECT l,r,d,g,v FROM explain_alias_view", []string{"l", "r", "d", "g", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1), int64(1), int64(1)}}))
					run(fixture.Select("SELECT id FROM "+base, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
			check("published", "explain_alias_base")
			run(fixture.Exec("ALTER TABLE explain_alias_base RENAME TO explain_alias_renamed", 0))
			check("renamed", "explain_alias_renamed")
			if !b.PG {
				// Real native publication/control boundary, not a reconstructed
				// fixture or identity remapper. PG independently covers SQL DDL.
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				check("restored", "explain_alias_renamed")
			}
			run(fixture.Exec(viewSQL("explain_alias_renamed"), 0))
			check("replaced", "explain_alias_renamed")
			if !reflect.DeepEqual(retained, snapshot) {
				t.Fatal("published plan text was mutated by stored-definition lifecycle")
			}
		})
	}
}
