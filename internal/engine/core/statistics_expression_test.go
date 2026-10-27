package core_test

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStatisticsExpressionBindingOwnership(t *testing.T) {
	e, rel := statisticsUnitEngine(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	parsed, err := parser.Parse("CREATE STATISTICS expression_owned ON (a+1) FROM statistics_input")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := statisticsUnitStage(t, e, op, parsed[0].(*parser.CreateStatistics))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	d := statisticsUnitDescriptor(t, stage)
	if d.Selection.Len() != 0 || d.Selection.ExpressionLen() != 1 || d.Selection.Dimension(0) != -1 || d.Selection.Kinds() != "e" || d.Expressions.Len() != 1 {
		t.Fatal("expression dimension authority", d)
	}
	recipe := d.Expressions.At(0)
	if recipe.InputLen() != 1 || recipe.Input(0).Address.SubID != rel.Attributes[0] || recipe.Definition() == nil || recipe.Execution() == nil {
		t.Fatal("retained binding omitted input")
	}
	attributes := make([]int32, recipe.AttributeLen())
	for i := range attributes {
		attributes[i] = recipe.Attribute(i)
	}
	inputs := make([]ids.ObjectRef, recipe.InputLen())
	for i := range inputs {
		inputs[i] = recipe.Input(i)
	}
	dependencies := make([]ids.ObjectRef, recipe.DependencyLen())
	for i := range dependencies {
		dependencies[i] = recipe.Dependency(i)
	}
	owned := catalog.NewStatisticsExpression(recipe.Definition(), recipe.Execution(), attributes, inputs, dependencies, recipe.Usage())
	values := []catalog.StatisticsExpression{owned}
	collection := catalog.NewStatisticsExpressions(values)
	attributes[0] = 999
	inputs[0] = ids.ObjectRef{}
	if len(dependencies) > 0 {
		dependencies[0] = ids.ObjectRef{}
	}
	values[0] = catalog.StatisticsExpression{}
	if collection.At(0).Attribute(0) != rel.Attributes[0] || collection.At(0).Input(0) != recipe.Input(0) || collection.At(0).Definition() == nil {
		t.Fatal("constructor retained caller buffers")
	}
	copy := *d
	copy.TargetSet = true
	copy.Target = 0
	copy.ObjectRevision++
	if copy.Expressions != d.Expressions {
		t.Fatal("metadata COW changed recipe identity")
	}
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	for _, mode := range []string{"foreign_input", "stale_epoch", "duplicate_input", "missing_input", "bad_layout", "duplicate_layout", "missing_execution", "foreign_dependency", "dangling_dependency"} {
		attrs := make([]int32, recipe.AttributeLen())
		for i := range attrs {
			attrs[i] = recipe.Attribute(i)
		}
		in := []ids.ObjectRef{recipe.Input(0)}
		deps := make([]ids.ObjectRef, recipe.DependencyLen())
		for i := range deps {
			deps[i] = recipe.Dependency(i)
		}
		execution := recipe.Execution()
		switch mode {
		case "foreign_input":
			in[0] = (&ids.Domain{}).Ref(view.Epoch(), in[0].Address)
		case "stale_epoch":
			in[0].Epoch++
		case "duplicate_input":
			in = append(in, in[0])
		case "missing_input":
			a := in[0].Address
			a.SubID = 999
			in[0] = view.Domain().Ref(view.Epoch(), a)
		case "bad_layout":
			attrs[0] = 999
		case "duplicate_layout":
			attrs[1] = attrs[0]
		case "missing_execution":
			execution = nil
		case "foreign_dependency":
			deps = append(deps, (&ids.Domain{}).Ref(view.Epoch(), rel.Address()))
		case "dangling_dependency":
			deps = append(deps, view.Domain().Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 999999}))
		}
		invalid := *d
		invalid.Expressions = catalog.NewStatisticsExpressions([]catalog.StatisticsExpression{catalog.NewStatisticsExpression(recipe.Definition(), execution, attrs, in, deps, recipe.Usage())})
		state := catalog.CloneState(view.State())
		state.SetDescriptor(invalid.Address(), &invalid)
		if err := (catalog.StatisticsProvider{}).Validate(view.WithState(state), &invalid); err == nil {
			t.Fatal("accepted invalid recipe", mode)
		}
	}
}

func TestStatisticsExpressionBindingSignedSelection(t *testing.T) {
	keys := []int32{4, 2}
	s, err := catalog.NewStatisticsSelection(keys, 2, "ed")
	if err != nil {
		t.Fatal(err)
	}
	keys[0] = 99
	if s.DimensionLen() != 4 || s.Dimension(0) != 2 || s.Dimension(1) != 4 || s.Dimension(2) != -1 || s.Dimension(3) != -2 || s.Kinds() != "de" {
		t.Fatal(s)
	}
	for _, tc := range []struct {
		keys  []int32
		n     int
		kinds string
	}{{nil, 0, "d"}, {[]int32{1}, 0, "d"}, {nil, 1, "d"}, {nil, 1, "de"}, {[]int32{1, 1}, 1, "de"}, {[]int32{-1, 2}, 0, "d"}, {nil, 9, "e"}, {[]int32{1, 2}, 0, "de"}} {
		if _, err := catalog.NewStatisticsSelection(tc.keys, tc.n, tc.kinds); err == nil {
			t.Fatal("invalid selection", tc)
		}
	}
}

func TestStatisticsExpressionBindingDefinitionEquality(t *testing.T) {
	for _, tc := range []struct {
		sql       string
		duplicate bool
	}{
		{"CREATE STATISTICS s ON (a+1),(a+1) FROM statistics_input", true},
		{"CREATE STATISTICS s ON (a+1),(a+2) FROM statistics_input", false},
		{"CREATE STATISTICS s ON random() FROM statistics_input", false},
	} {
		e, _ := statisticsUnitEngine(t)
		op, done := enginetest.RoleOperation(t, e, "postgres")
		parsed, err := parser.Parse(tc.sql)
		if err != nil {
			done()
			t.Fatal(err)
		}
		stage, err := statisticsUnitStage(t, e, op, parsed[0].(*parser.CreateStatistics))
		if stage != nil {
			e.DiscardCatalogStage(stage)
		}
		done()
		if tc.duplicate {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42701" || diagnostic.Message != "duplicate expression in statistics definition" {
				t.Fatal("structural duplicate ignored source-independent definition", err)
			}
		} else if err != nil {
			t.Fatal("distinct or nonimmutable definition rejected", tc.sql, err)
		}
	}
}
