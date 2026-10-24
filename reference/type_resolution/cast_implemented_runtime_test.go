package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// One fixture per fixed prerequisite group; source/seed rows are preloaded once.
// Successful projections sharing a row are batched, errors remain independent.
func castRunCases(t *testing.T, cells []castMatrixCell, function bool) {
	t.Helper()
	for _, group := range []string{"Builtin", "UserShapes", "Installed"} {
		var selected []castMatrixCell
		for _, cell := range cells {
			if castPrerequisiteGroup(cell) == group {
				selected = append(selected, cell)
			}
		}
		if len(selected) == 0 {
			continue
		}
		t.Run(group, func(t *testing.T) {
			grantor := false
			for _, cell := range selected {
				if strings.Contains(cell.source.name, "aclitem") || strings.Contains(cell.target.name, "aclitem") {
					grantor = true
				}
			}
			if grantor {
				fixture.RoleReferenceCleanup(t, []string{"cast_acl_grantor"})
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					castBuiltinSetup(t, p.Ctx, b)
					if group == "UserShapes" {
						castUserShapesSetup(t, p.Ctx, b)
					}
					if group == "Installed" {
						castInstalledSetup(t, p.Ctx, b)
					}
					if grantor {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE cast_acl_grantor", 0))
					}
					type sourceRows struct {
						table string
						ids   map[string]int64
					}
					sources := map[string]*sourceRows{}
					type batch struct {
						from                        string
						expressions, columns, types []string
						values                      []any
					}
					var batches []batch
					for _, original := range selected {
						cell := original
						value := castScalarMatrixValue(cell)
						var analysis *fixture.Diagnostic
						if function {
							cell, value, analysis = castScalarFunctionCell(cell)
						}
						expr, from := cell.source.source, ""
						if cell.source.storage {
							rows := sources[cell.source.name]
							if rows == nil {
								rows = &sourceRows{table: fmt.Sprintf("cast_input_%d", len(sources)), ids: map[string]int64{}}
								sources[cell.source.name] = rows
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+rows.table+"(id integer,v "+cell.source.name+")", 0))
							}
							id, exists := rows.ids[value.input]
							if !exists {
								id = int64(len(rows.ids) + 1)
								rows.ids[value.input] = id
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%d,$1)", rows.table, id), 1, castInputArgument(t, cell.source, value.input)))
							}
							expr = "v"
							from = fmt.Sprintf(" FROM %s WHERE id=%d", rows.table, id)
						}
						value = castProviderMatrixValue(t, p, b, cell, value)
						form := "CAST(" + expr + " AS " + castInventoryIdentity(cell.target) + ")"
						if function {
							form = castFunctionName(cell.target) + "(" + expr + ")"
						}
						q := "SELECT " + form + " AS v" + from
						diagnostic := castTargetBoundary(cell, q, b.PG)
						if function {
							diagnostic = castFunctionTargetBoundary(cell, q, b.PG)
						}
						if diagnostic == nil && !cell.contexts[2].admitted {
							if analysis != nil {
								diagnostic = analysis
							} else {
								d := castMatrixForbidden(cell.source, cell.target, q)
								diagnostic = &d
							}
						}
						if diagnostic == nil {
							diagnostic = value.diagnostic
						}
						if diagnostic != nil {
							s := fixture.ImplicitError(q, *diagnostic)
							s.SelectRows = true
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
							continue
						}
						if value.polygon {
							castCirclePolygonValue(t, p, b, q, cell.target, value.output, nil)
							continue
						}
						var expected any
						if cell.source.name != "cast_deferred_record" {
							expected = castExpectedValue(t, castMatrixResultFact(cell), value.output, b.PG)
						}
						index := -1
						for i := range batches {
							if batches[i].from == from && len(batches[i].expressions) < 32 {
								index = i
								break
							}
						}
						if index < 0 {
							index = len(batches)
							batches = append(batches, batch{from: from})
						}
						item := &batches[index]
						name := fmt.Sprintf("v%d", len(item.expressions))
						item.expressions = append(item.expressions, form+" AS "+name)
						item.columns = append(item.columns, name)
						item.types = append(item.types, castResultType(castMatrixResultFact(cell), b.PG))
						item.values = append(item.values, expected)
					}
					for _, item := range batches {
						q := "SELECT " + strings.Join(item.expressions, ",") + item.from
						step := fixture.Select(q, item.columns, item.types, [][]any{item.values})
						step.CheckRaw = castResultStep(t, "", castInventoryType("text"), nil, b.PG).CheckRaw
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
					}
				})
			}
		})
	}
}
func castScalarMatrix(t *testing.T, family string, function bool) {
	t.Helper()
	var cells []castMatrixCell
	for _, c := range castRuntimeCases(function) {
		if c.family == family {
			cells = append(cells, castCaseCell(c.source, c.target))
		}
	}
	castRunCases(t, cells, function)
}
