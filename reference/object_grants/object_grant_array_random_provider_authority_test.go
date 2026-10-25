package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceObjectGrantArrayRandomProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, target            string
		literal, parameter, row string
		args                    []any
		strictNull              []string
		sibling                 string
	}{
		{
			name:       "array_shuffle",
			target:     "pg_catalog.array_shuffle(anyarray)",
			literal:    "array_shuffle(ARRAY[7])",
			parameter:  "array_shuffle($1::INT[])",
			row:        "array_shuffle(a)",
			args:       []any{"{7}"},
			strictNull: []string{"array_shuffle(NULL::INT[])"},
			sibling:    "array_sample(ARRAY[7],1)",
		},
		{
			name:      "array_sample",
			target:    "pg_catalog.array_sample(anyarray,integer)",
			literal:   "array_sample(ARRAY[7],1)",
			parameter: "array_sample($1::INT[],$2::INTEGER)",
			row:       "array_sample(a,n)",
			args:      []any{"{7}", int64(1)},
			strictNull: []string{
				"array_sample(NULL::INT[],1)",
				"array_sample(ARRAY[7],NULL::INTEGER)",
				"array_sample(NULL::INT[],NULL::INTEGER)",
			},
			sibling: "array_shuffle(ARRAY[7])",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_array_random_provider(id INT,a INT[],n INT)", 0))
				run(fixture.Exec("INSERT INTO og_array_random_provider VALUES(1,'{7}',1),(2,NULL,1),(3,'{7}',NULL)", 3))
				run(fixture.Exec("GRANT SELECT ON og_array_random_provider TO og_object_reader", 0))
				cases := []objectGrantProviderCase{
					{fixture.Select("SELECT "+tc.literal+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}}), true, true},
					{fixture.Select("SELECT pg_catalog."+tc.literal+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}}), true, true},
					{fixture.Select("SELECT "+tc.parameter+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}}, tc.args...), true, true},
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_array_random_provider WHERE id=1", []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}}), true, true},
					// Runtime NULL does not imply planner elimination of the selected call.
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_array_random_provider WHERE id=2", []string{"v"}, []string{"_INT4"}, [][]any{{nil}}), true, true},
					// Both data-empty and statically false sources retain a nonfolded scalar target.
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_array_random_provider WHERE id>99", []string{"v"}, []string{"_INT4"}, nil), true, true},
					{fixture.Select("SELECT "+tc.row+" AS v FROM og_array_random_provider WHERE FALSE", []string{"v"}, []string{"_INT4"}, nil), true, true},
					// Statically eliminated CASE arm is not current EXECUTE demand.
					{fixture.Select("SELECT CASE WHEN FALSE THEN "+tc.row+" ELSE ARRAY[9] END AS v FROM og_array_random_provider WHERE id=1", []string{"v"}, []string{"_INT4"}, [][]any{{"{9}"}}), false, true},
				}
				for _, expr := range tc.strictNull {
					// Literal strict NULL is proposed to eliminate the call before EXECUTE checks.
					cases = append(cases, objectGrantProviderCase{
						fixture.Select("SELECT "+expr+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{nil}}), false, true,
					})
				}
				if tc.name == "array_sample" {
					cases = append(cases,
						// Count zero is a real selected invocation, not a permission bypass.
						objectGrantProviderCase{fixture.Select("SELECT array_sample(ARRAY[7],0) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT array_sample($1::INT[],$2::INTEGER) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}}, "{7}", int64(0)), true, true},
						objectGrantProviderCase{fixture.Select("SELECT array_sample('{}'::INT[],0) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}}), true, true},
						objectGrantProviderCase{fixture.Select("SELECT array_sample(a,n) AS v FROM og_array_random_provider WHERE id=3", []string{"v"}, []string{"_INT4"}, [][]any{{nil}}), true, true},
					)
				} else {
					cases = append(cases, objectGrantProviderCase{
						fixture.Select("SELECT array_shuffle('{}'::INT[]) AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{}"}}), true, true,
					})
				}
				// Exactly one sibling control: necessary to distinguish target-specific
				// authority from an accidental deny of the entire shared provider family.
				control := fixture.Select("SELECT "+tc.sibling+" AS v", []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}})
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, tc.target, "function", tc.name, cases, []fixture.Step{control})
			})
		})
	}
}
