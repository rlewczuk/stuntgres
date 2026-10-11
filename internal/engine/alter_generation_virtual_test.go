package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterGeneratedExpressionCapturedVirtual(t *testing.T) {
	e := New()
	admin, cleanup := roleOperation(t, e, "postgres")
	defer cleanup()
	table := &parser.CreateTable{Name: alterGenerationName("g"), Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "int4"}}},
		{Column: &parser.Column{Name: "b", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Generation: &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: "virtual", Expression: alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1))}}}}},
	}}
	if _, err := e.Execute(admin, table, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(admin, &parser.Insert{Name: alterGenerationName("g"), Targets: []parser.InsertTarget{{Name: "a"}}, Rows: [][]parser.Expr{{alterGenerationLiteral(2)}}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "g")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	capture := func() *querySource {
		t.Helper()
		scope, err := e.StartStatement(admin)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(errors.New("capture cleanup"))
		if err := scope.gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		view, err := scope.AcquireDataView(ref)
		if err != nil {
			t.Fatal(err)
		}
		source, err := captureQueryPrefix(view, ref)
		if err != nil {
			t.Fatal(err)
		}
		source.occurrence = 0
		if err := scope.Finish(nil); err != nil {
			source.close()
			t.Fatal(err)
		}
		return source
	}
	previous := capture()
	defer previous.close()
	origin := ref
	origin.Address.SubID = 2
	frozen := columnExpression{expressionInfo: expressionInfo{usage: old.columns[1].Usage}, origin: origin, scope: 1, frame: 1, slot: 1, queryOccurrence: 1}.withGeneration(old, 1, 0)
	add := parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: "c", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Default: alterGenerationLiteral(5)}}}}
	set := alterGenerationAction(alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationColumn("c")))
	if _, err := e.Execute(admin, &parser.AlterTable{Name: alterGenerationName("g"), Actions: []parser.AlterTableAction{add, set}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	current := capture()
	defer current.close()
	// The persisted view exposes its original two slots even though the current
	// recipe needs the third physical input. Its captured raw source owns that input.
	bridge := &querySource{input: current, layout: []int{0, 1}, scratch: make([]sqltype.Value, 2), occurrence: 0}
	evaluate := func(source *querySource, want int64, latest [][]sqltype.Value) {
		t.Helper()
		state := &queryState{source: source, virtualReads: true}
		frame := state.evaluation(context.Background())
		frame.bindQueryRow(source, 0)
		frame.queryCurrentRows = latest
		outer := EvalContext{Context: context.Background(), Scope: 2, Frame: 2, Outer: &frame, queryCurrentRows: [][]sqltype.Value{}}
		value, err := frozen.Eval(&outer)
		n, numberErr := value.Integer()
		if err != nil || numberErr != nil || n != want {
			t.Fatal("captured correlated recipe/frame", n, err, numberErr)
		}
	}
	evaluate(previous, 3, nil)
	evaluate(bridge, 7, nil)
	three, err := sqltype.Integer(old.columns[0].Usage.Type, 3)
	if err != nil {
		t.Fatal(err)
	}
	five, err := sqltype.Integer(old.columns[0].Usage.Type, 5)
	if err != nil {
		t.Fatal(err)
	}
	latest := [][]sqltype.Value{{three, sqltype.Null(old.columns[1].Usage.Type), five}}
	evaluate(bridge, 8, latest)
	evaluate(bridge, 7, nil)
	if len(old.columns) != 2 || previous.view.data.Load().catalog.state.descriptors[ref.Address] != old || !reflect.DeepEqual(frozen.virtual, old.generations[2].planned) {
		t.Fatal("frozen source recipe changed")
	}
	if allocations := testing.AllocsPerRun(10, func() {
		recipe, row, err := frozen.capturedGeneration(current, 0, nil)
		if err != nil || recipe == nil || len(row) != 3 {
			t.Fatal(recipe, row, err)
		}
	}); allocations != 0 {
		t.Fatal("captured recipe resolution allocated", allocations)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	evaluate(previous, 3, nil)
	evaluate(bridge, 7, nil)
}
