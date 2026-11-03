package enginetests

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

var _ expr.StatementJumbler = functions.GinMaintenanceCall{}

func TestGINMaintenanceStatementJumble(t *testing.T) {
	e := engine.New()
	for _, source := range []string{
		"CREATE TABLE jumble_gin(a integer[])",
		"CREATE INDEX jumble_gin_a ON jumble_gin USING gin(a)",
		"CREATE INDEX jumble_gin_b ON jumble_gin USING gin(a)",
	} {
		enginetest.Run(t, e, source)
	}
	var constantID int64
	for _, source := range []string{
		"SELECT gin_clean_pending_list('jumble_gin_a'::regclass)",
		"SELECT gin_clean_pending_list('jumble_gin_b'::regclass)",
	} {
		ast, binding := pgssAnalyzedBinding(t, e, source)
		project, ok := binding.Query.Root.(*plan.ProjectNode)
		if !ok || len(project.Expressions) != 1 {
			t.Fatal("missing genuine cleanup projection")
		}
		call, ok := project.Expressions[0].(functions.GinMaintenanceCall)
		if !ok || call.CallRoutineReference() == nil || call.CallRoutineReference().RoutineOID() != 3789 {
			t.Fatal("missing typed cleanup routine provenance")
		}
		rebuilt, err := call.Rebuild(expr.InfoOf(call), call.Children())
		if err != nil {
			t.Fatal(err)
		}
		before := analyze.JumbleStatement(ast, binding)
		project.Expressions[0] = rebuilt
		after := analyze.JumbleStatement(ast, binding)
		if before.QueryID() != after.QueryID() || constantID != 0 && before.QueryID() != constantID {
			t.Fatal("rebuild or constant target changed cleanup identity", before.QueryID(), after.QueryID(), constantID)
		}
		constantID = before.QueryID()
		for _, jumble := range []analyze.StatementJumble{before, after} {
			text, raw := parser.NormalizeStatementText(source, 0, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if text != "SELECT gin_clean_pending_list($1::regclass)" || raw != source {
				t.Fatal("cleanup lexical child did not normalize", text, raw)
			}
		}
	}
	parameter := pgssAnalyzedJumble(t, e, "SELECT gin_clean_pending_list($1::regclass)")
	if parameter.QueryID() == constantID || parameter.HighestParameter != 1 || len(parameter.Locations) != 0 {
		t.Fatal("cleanup argument lost parameter identity", parameter)
	}
}
