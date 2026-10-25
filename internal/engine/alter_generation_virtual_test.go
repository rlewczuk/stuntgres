package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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
	old := e.Catalog.State.MustRelation(ref.Address)
	capture := func() *exec.Source {
		t.Helper()
		scope, err := e.StartStatement(admin)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(errors.New("capture cleanup"))
		if err := scope.Gate(&parser.Select{}); err != nil {
			t.Fatal(err)
		}
		view, err := scope.AcquireDataView(ref)
		if err != nil {
			t.Fatal(err)
		}
		source, err := exec.CapturePrefix(view, ref)
		if err != nil {
			t.Fatal(err)
		}
		source.Occurrence = 0
		if err := scope.Finish(nil); err != nil {
			source.Close()
			t.Fatal(err)
		}
		return source
	}
	previous := capture()
	defer previous.Close()
	origin := ref
	origin.Address.SubID = 2
	frozen := plan.ColumnExpr{Info: expr.Info{Usage: old.Columns[1].Usage}, Origin: origin, Scope: 1, Frame: 1, Slot: 1, QueryOccurrence: 1}.WithGeneration(old, 1, 0)
	add := parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: "c", TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Default: alterGenerationLiteral(5)}}}}
	set := alterGenerationAction(alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationColumn("c")))
	if _, err := e.Execute(admin, &parser.AlterTable{Name: alterGenerationName("g"), Actions: []parser.AlterTableAction{add, set}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	current := capture()
	defer current.Close()
	// The persisted view exposes its original two slots even though the current
	// recipe needs the third physical input. Its captured raw source owns that input.
	bridge := &exec.Source{Input: current, Layout: []int{0, 1}, Scratch: make([]sqltype.Value, 2), Occurrence: 0}
	evaluate := func(source *exec.Source, want int64, latest [][]sqltype.Value) {
		t.Helper()
		state := &exec.State{Source: source, IdentityReads: true}
		frame := state.Evaluation(context.Background())
		frame.BindQueryRow(source, 0)
		frame.QueryCurrentRows = latest
		outer := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 2, Frame: 2, Outer: &frame, QueryCurrentRows: [][]sqltype.Value{}}}
		value, err := frozen.Eval(&outer)
		n, numberErr := value.Integer()
		if err != nil || numberErr != nil || n != want {
			t.Fatal("captured correlated recipe/frame", n, err, numberErr)
		}
	}
	evaluate(previous, 3, nil)
	evaluate(bridge, 7, nil)
	three, err := sqltype.Integer(old.Columns[0].Usage.Type, 3)
	if err != nil {
		t.Fatal(err)
	}
	five, err := sqltype.Integer(old.Columns[0].Usage.Type, 5)
	if err != nil {
		t.Fatal(err)
	}
	latest := [][]sqltype.Value{{three, sqltype.Null(old.Columns[1].Usage.Type), five}}
	evaluate(bridge, 8, latest)
	evaluate(bridge, 7, nil)
	if len(old.Columns) != 2 || previous.View.Load().Catalog.Get(ref.Address) != old || !reflect.DeepEqual(frozen.Virtual, old.ColumnMeta.Generations[2].Planned) {
		t.Fatal("frozen source recipe changed")
	}
	recipeContext := exec.Context{}
	if allocations := testing.AllocsPerRun(10, func() {
		recipe, row, err := recipeContext.ColumnCapturedGeneration(frozen, current, 0, nil)
		if err != nil || recipe == nil || len(row) != 3 {
			t.Fatal(recipe, row, err)
		}
	}); allocations != 0 {
		t.Fatal("captured recipe resolution allocated", allocations)
	}
	capturedOwner := current.View.Load().Catalog.MustRelation(ref.Address)
	if recipeContext.RoutineConsumer != capturedOwner || !recipeContext.RoutineRecipe || recipeContext.RoutineGraft ||
		!reflect.DeepEqual(recipeContext.RoutineReferences, capturedOwner.ColumnMeta.Generations[2].Dependencies) {
		t.Fatal("captured generation borrowed frozen or enclosing provenance")
	}
	if err := Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	evaluate(previous, 3, nil)
	evaluate(bridge, 7, nil)
}

func TestVirtualGenerationOriginalAdmission(t *testing.T) {
	e := New()
	op, cleanup := roleOperation(t, e, "postgres")
	defer cleanup()
	roleExecute(t, e, op, "CREATE TABLE strict_null_rows(v integer,j json,h integer GENERATED ALWAYS AS (json_array_length(j)) VIRTUAL)")
	roleExecute(t, e, op, "CREATE ROLE virtual_admission_reader LOGIN")
	roleExecute(t, e, op, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC")
	reader, done := roleOperation(t, e, "virtual_admission_reader")
	defer done()
	e.Mu.Lock()
	readerAuthority, err := e.AuthorizationLocked(reader)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Complete all setup before taking the statement's admission ownership.
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	result, err := scope.Execute(statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "h")
	column, ok := node.(plan.ColumnExpr)
	if !ok || column.VirtualOwner == nil {
		t.Fatal("bound virtual column lost owning generation", node)
	}
	authority := evaluation.RoutineAuthority
	if err := view.ValidateExpressionJoinExecution(&evaluation, column, evaluation.Catalog); err != nil {
		t.Fatal("original generation admission", err)
	}
	if evaluation.RoutineAuthority != authority {
		t.Fatal("generation admission changed current invoker")
	}
	owner := *column.VirtualOwner
	definition := owner.ColumnMeta.Generations[column.VirtualAttribute]
	owner.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{column.VirtualAttribute: definition}
	missing := column
	missing.VirtualOwner = &owner
	definition.Dependencies = nil
	owner.ColumnMeta.Generations[column.VirtualAttribute] = definition
	if err := view.ValidateExpressionJoinExecution(&evaluation, missing, evaluation.Catalog); err == nil {
		t.Fatal("generation recovered a missing original manifest from enclosing query or current OID")
	}
	missing.VirtualOwner = nil
	if err := view.ValidateExpressionJoinExecution(&evaluation, missing, evaluation.Catalog); err == nil {
		t.Fatal("SQL virtual column admitted without its original owner")
	}
	// Context selection is evaluator-local and cannot borrow a definer view's
	// references or replace the original current-invoker authority.
	local := evaluation
	local.RoutineGraft = true
	local.RoutineChecks = []catalog.ExpressionDependency{{}}
	exec.SelectVirtualGenerationRecipe(&local, column.VirtualOwner, column.VirtualOwner.ColumnMeta.Generations[column.VirtualAttribute])
	if local.RoutineAuthority != authority || local.RoutineConsumer != column.VirtualOwner ||
		local.RoutineGraft || !local.RoutineRecipe || local.RoutineChecks != nil ||
		!reflect.DeepEqual(local.RoutineReferences, column.VirtualOwner.ColumnMeta.Generations[column.VirtualAttribute].Dependencies) {
		t.Fatal("virtual generation retained enclosing recipe authority")
	}
	invoker := evaluation
	invoker.RoutineAuthority = &readerAuthority
	err = view.ValidateExpressionJoinExecution(&invoker, column, readerAuthority.Catalog)
	var denied interface{ SQLState() string }
	if !errors.As(err, &denied) || denied.SQLState() != "42501" {
		t.Fatal("generation did not enforce current invoker", err)
	}
}
