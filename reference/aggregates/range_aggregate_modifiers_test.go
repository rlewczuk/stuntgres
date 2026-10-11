package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateModifierBoundaries(t *testing.T) {
	for _, family := range []struct{ r, m string }{{"INT4RANGE", "INT4MULTIRANGE"}, {"INT8RANGE", "INT8MULTIRANGE"}, {"NUMRANGE", "NUMMULTIRANGE"}} {
		for _, input := range []string{family.r, family.m} {
			t.Run(input, func(t *testing.T) {
				rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v "+input+")", 0))
					value := "[1,4)"
					if input == family.m {
						value = "{[1,4)}"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES('"+value+"'),('"+value+"')", 2))
					for _, name := range []string{"range_agg", "range_intersect_agg"} {
						result, datum := family.m, "{[1,4)}"
						if name == "range_intersect_agg" && input == family.r {
							result, datum = family.r, "[1,4)"
						}
						for _, form := range []string{"DISTINCT v", "v ORDER BY v", "v) OVER("} {
							q := "SELECT " + name + "(" + form + ") FROM r"
							if b.PG {
								want := [][]any{{datum}}
								if form == "v) OVER(" {
									want = [][]any{{datum}, {datum}}
								}
								rangeAggregateRoutes(t, p, b, rangeAggregateStep(true, q, []string{name}, []string{result}, want))
							} else {
								d := fixture.ControlsReferenceDiagnostic("42883", "could not identify an equality operator for type "+strings.ToLower(input))
								position := strings.Index(q, "DISTINCT ") + len("DISTINCT ") + 1
								if form == "v ORDER BY v" {
									d = fixture.ControlsReferenceDiagnostic("0A000", "aggregate ordering is unsupported for type "+strings.ToLower(input))
									position = strings.LastIndex(q, "v)") + 1
								}
								if form == "v) OVER(" {
									d = fixture.ControlsReferenceDiagnostic("0A000", "window function provider is unsupported")
									position = 8
								}
								d.Position = strconv.Itoa(position)
								for _, prepared := range []bool{false, true} {
									s := fixture.ErrorDiagnostic(q, d)
									s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
									if prepared {
										s.ErrorStage = "prepare"
									}
									fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
								}
							}
						}
					}
				})
			})
		}
	}
}
