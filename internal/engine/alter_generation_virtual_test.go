package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
	old := e.catalog.state.MustRelation(ref.Address)
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
	frozen := columnExpression{Info: expr.Info{Usage: old.Columns[1].Usage}, origin: origin, scope: 1, frame: 1, slot: 1, queryOccurrence: 1}.withGeneration(old, 1, 0)
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
		outer := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 2, Frame: 2, Outer: &frame, queryCurrentRows: [][]sqltype.Value{}}}
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
	if len(old.Columns) != 2 || previous.view.Load().Catalog.Get(ref.Address) != old || !reflect.DeepEqual(frozen.virtual, old.ColumnMeta.Generations[2].Planned) {
		t.Fatal("frozen source recipe changed")
	}
	recipeContext := EvalContext{}
	if allocations := testing.AllocsPerRun(10, func() {
		recipe, row, err := recipeContext.columnCapturedGeneration(frozen, current, 0, nil)
		if err != nil || recipe == nil || len(row) != 3 {
			t.Fatal(recipe, row, err)
		}
	}); allocations != 0 {
		t.Fatal("captured recipe resolution allocated", allocations)
	}
	capturedOwner := current.view.Load().Catalog.MustRelation(ref.Address)
	if recipeContext.routineConsumer != capturedOwner || !recipeContext.routineRecipe || recipeContext.routineGraft ||
		!reflect.DeepEqual(recipeContext.routineReferences, capturedOwner.ColumnMeta.Generations[2].Dependencies) {
		t.Fatal("captured generation borrowed frozen or enclosing provenance")
	}
	if err := e.Reset(context.Background()); err != nil {
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
	e.mu.Lock()
	readerAuthority, err := e.authorizationLocked(reader)
	e.mu.Unlock()
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
	column, ok := node.(columnExpression)
	if !ok || column.virtualOwner == nil {
		t.Fatal("bound virtual column lost owning generation", node)
	}
	authority := evaluation.routineAuthority
	if err := validateExpressionJoinExecution(&evaluation, column, evaluation.catalog); err != nil {
		t.Fatal("original generation admission", err)
	}
	if evaluation.routineAuthority != authority {
		t.Fatal("generation admission changed current invoker")
	}
	owner := *column.virtualOwner
	definition := owner.ColumnMeta.Generations[column.virtualAttribute]
	owner.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{column.virtualAttribute: definition}
	missing := column
	missing.virtualOwner = &owner
	definition.Dependencies = nil
	owner.ColumnMeta.Generations[column.virtualAttribute] = definition
	if err := validateExpressionJoinExecution(&evaluation, missing, evaluation.catalog); err == nil {
		t.Fatal("generation recovered a missing original manifest from enclosing query or current OID")
	}
	missing.virtualOwner = nil
	if err := validateExpressionJoinExecution(&evaluation, missing, evaluation.catalog); err == nil {
		t.Fatal("SQL virtual column admitted without its original owner")
	}
	// Context selection is evaluator-local and cannot borrow a definer view's
	// references or replace the original current-invoker authority.
	local := evaluation
	local.routineGraft = true
	local.routineChecks = []catalog.ExpressionDependency{{}}
	selectVirtualGenerationRecipe(&local, column.virtualOwner, column.virtualOwner.ColumnMeta.Generations[column.virtualAttribute])
	if local.routineAuthority != authority || local.routineConsumer != column.virtualOwner ||
		local.routineGraft || !local.routineRecipe || local.routineChecks != nil ||
		!reflect.DeepEqual(local.routineReferences, column.virtualOwner.ColumnMeta.Generations[column.virtualAttribute].Dependencies) {
		t.Fatal("virtual generation retained enclosing recipe authority")
	}
	invoker := evaluation
	invoker.routineAuthority = &readerAuthority
	err = validateExpressionJoinExecution(&invoker, column, readerAuthority.Catalog)
	var denied interface{ SQLState() string }
	if !errors.As(err, &denied) || denied.SQLState() != "42501" {
		t.Fatal("generation did not enforce current invoker", err)
	}
}
