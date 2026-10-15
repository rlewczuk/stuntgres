package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayOrderQuantifiedArrayBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{
				"CREATE DOMAIN ao_qab_leaf AS int[]",
				"CREATE DOMAIN ao_qab_chain AS ao_qab_leaf",
				"CREATE DOMAIN ao_qab_outer AS ao_qab_leaf[]",
				"CREATE TABLE ao_qab_source(id int)",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ao_qab_source VALUES(1)", 1))
			// Expectations follow ordinary inner-array comparison and SQL quantified
			// reduction independently: SOME is ANY, empty ANY=false/ALL=true, whole
			// NULL RHS is UNKNOWN, and decisive comparisons dominate NULL elements.
			for _, tc := range []struct {
				name, left, rhs                      string
				args                                 []any
				equalAny, equalAll, lessAny, lessAll any
			}{
				{"constructor", "'{1}'::int[]", "ARRAY['{1}'::ao_qab_leaf]", nil, true, true, false, false},
				{"literal", "'{1}'::int[]", `'{"{1}"}'::ao_qab_leaf[]`, nil, true, true, false, false},
				{"all_null_elements", "'{1}'::int[]", "ARRAY[NULL::ao_qab_leaf]", nil, nil, nil, nil, nil},
				{"empty", "'{1}'::int[]", "'{}'::ao_qab_leaf[]", nil, false, true, false, true},
				{"null_rhs", "'{1}'::int[]", "NULL::ao_qab_leaf[]", nil, nil, nil, nil, nil},
				{"null_left_empty_rhs", "NULL::int[]", "'{}'::ao_qab_leaf[]", nil, false, true, false, true},
				{"chained_leaf", "'{1}'::int[]", "ARRAY['{1}'::ao_qab_chain]", nil, true, true, false, false},
				{"outer_domain", "'{1}'::int[]", `'{"{1}"}'::ao_qab_outer`, nil, true, true, false, false},
				{"empty_outer_domain", "'{1}'::int[]", "'{}'::ao_qab_outer", nil, false, true, false, true},
				{"null_outer_domain", "'{1}'::int[]", "NULL::ao_qab_outer", nil, nil, nil, nil, nil},
				{"typed_parameter", "'{1}'::int[]", "$1::ao_qab_leaf[]", []any{"{}"}, false, true, false, true},
				{"equal_and_null", "'{1}'::int[]", "ARRAY['{1}'::ao_qab_leaf,NULL::ao_qab_leaf]", nil, true, nil, nil, false},
				{"greater_and_null", "'{1}'::int[]", "ARRAY['{2}'::ao_qab_leaf,NULL::ao_qab_leaf]", nil, nil, false, true, nil},
				{"inner_prefix", "'{1}'::int[]", "ARRAY['{1,2}'::ao_qab_leaf]", nil, false, false, true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
						for _, operator := range []string{"=", "<"} {
							t.Run(operator+quantifier, func(t *testing.T) {
								want := tc.equalAny
								if quantifier == "ALL" {
									want = tc.equalAll
								}
								if operator == "<" {
									want = tc.lessAny
									if quantifier == "ALL" {
										want = tc.lessAll
									}
								}
								for _, source := range []struct {
									suffix string
									empty  bool
								}{{"", false}, {" FROM ao_qab_source", false}, {" FROM ao_qab_source WHERE FALSE", true}} {
									t.Run(source.suffix, func(t *testing.T) {
										q := "SELECT " + tc.left + " " + operator + " " + quantifier + "(" + tc.rhs + ") AS result" + source.suffix
										rows := [][]any{{want}}
										if source.empty {
											rows = nil
										}
										s := fixture.Select(q, []string{"result"}, []string{"BOOL"}, rows, tc.args...)
										s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
											cols, err := rows.ColumnTypes()
											if err != nil {
												t.Fatal(err)
											}
											arrayOrderMetadata(t, b, cols, []string{"result"})
										}
										arrayCompareBoth(t, p, b, c, s)
									})
								}
							})
						}
					}
				})
			}
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '{1}'::int[]='{1}'::int[],1=ANY(ARRAY[1]),NULL::int=ALL('{}'::int[])", []any{true, true, true}))
		})
	}
}
