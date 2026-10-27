package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func routineTreeType(t *testing.T, read catalog.Read) *sqltype.Descriptor {
	t.Helper()
	typ, ok := read.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.PgNodeTree)})
	if !ok {
		t.Fatal("missing pg_node_tree")
	}
	return typ.SQLType
}

func routineTreeProcedure(t *testing.T, read catalog.Read, name string) *catalog.Procedure {
	t.Helper()
	addresses := read.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name)
	if len(addresses) != 1 {
		t.Fatalf("routine %s: %v", name, addresses)
	}
	d, ok := read.Procedure(addresses[0])
	if !ok {
		t.Fatal("missing routine")
	}
	return d
}

func TestRoutineDeclarationTrees(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE FUNCTION tree_owner(a integer DEFAULT NULL, b text DEFAULT 'x') RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT 17; SELECT a; END`)
	read := e.CatalogRead()
	d := routineTreeProcedure(t, read, "tree_owner")
	typ := routineTreeType(t, read)
	runtime := core.EngineCatalogRuntime{}
	defaults, body, err := runtime.RoutineTrees(context.Background(), read, d, typ)
	if err != nil {
		t.Fatal(err)
	}
	carrier, err := defaults.Tree()
	if err != nil {
		t.Fatal(err)
	}
	list, ok := carrier.(plan.ExpressionListTree)
	if !ok || list.Len() != 2 {
		t.Fatalf("not an ordered default list: %T", carrier)
	}
	if list.At(0).ResultUsage().Type.ID() != sqltype.Int4 || list.At(1).ResultUsage().Type.ID() != sqltype.Text {
		t.Fatal("lost declared default types")
	}
	if !plan.StructuralNull(list.At(0)) {
		t.Fatalf("NULL default not a present checked literal: %T", list.At(0))
	}
	checkedSecond, _ := d.SQLDefaults.Default(1)
	if !reflect.DeepEqual(list.At(1), checkedSecond) {
		t.Fatal("default capture copied or replaced immutable checked node")
	}
	program, err := body.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if program != d.SQLBody {
		t.Fatal("body did not retain checked program owner")
	}
	text, err := typ.Output(context.Background(), body)
	if err != nil || strings.Count(text, "{parser.Select ") != 2 || !strings.HasPrefix(text, "{RoutineProgram {parser.Select") || !strings.Contains(text, "Targets [{parser.LiteralExpr Value {parser.Value Kind 1 Integer 17 ") || !strings.Contains(text, "Targets [{parser.ParameterExpr Number 1}]") {
		t.Fatalf("lost actual ordered program syntax: %s, %v", text, err)
	}
	if strings.Index(text, "Integer 17") > strings.Index(text, "Targets [{parser.ParameterExpr Number 1}]") {
		t.Fatal("routine statement order changed", text)
	}
	oldDefault, err := typ.Output(context.Background(), defaults)
	if err != nil || !strings.Contains(oldDefault, "Const null") || !strings.Contains(oldDefault, `Const value "x"`) {
		t.Fatal(oldDefault, err)
	}
	remapped, err := d.SQLBody.Rebind(read, nil, stored.NewRoutineRemap(read, nil, nil, nil, false))
	if err != nil {
		t.Fatal(err)
	}
	remappedText, err := remapped.StructuralOutput(context.Background())
	if err != nil || remappedText != text {
		t.Fatal("remap changed program structure", remappedText, err)
	}
	enginetest.Run(t, e, `CREATE OR REPLACE FUNCTION tree_owner(a integer DEFAULT 9, b text DEFAULT 'new') RETURNS integer LANGUAGE SQL RETURN a`)
	current := e.CatalogRead()
	newDefaults, newBody, err := runtime.RoutineTrees(context.Background(), current, routineTreeProcedure(t, current, "tree_owner"), typ)
	if err != nil {
		t.Fatal(err)
	}
	newDefaultText, err := typ.Output(context.Background(), newDefaults)
	if err != nil || newDefaultText == oldDefault {
		t.Fatal("replacement retained stale defaults", err)
	}
	newBodyText, err := typ.Output(context.Background(), newBody)
	if err != nil || newBodyText == text {
		t.Fatal("replacement retained stale body", err)
	}
	badDefaults, badBody, err := runtime.RoutineTrees(context.Background(), current, d, typ)
	if !errors.Is(err, ids.ErrCatalogReplaced) || badDefaults.Type() != nil || badBody.Type() != nil {
		t.Fatal("failed capture published values", err)
	}
	enginetest.Run(t, e, `DROP FUNCTION tree_owner(integer,text)`)
	if got, err := typ.Output(context.Background(), defaults); err != nil || got != oldDefault {
		t.Fatal("retained defaults changed", got, err)
	}
	if got, err := typ.Output(context.Background(), body); err != nil || got != text {
		t.Fatal("retained body changed", got, err)
	}
	builtin, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 5023})
	if !ok || builtin.DeclarationBody == nil || builtin.SQLBody != nil {
		t.Fatal("builtin recipe falsely claims executable program or is absent")
	}
	_, releasedBody, err := runtime.RoutineTrees(context.Background(), read, builtin, typ)
	if err != nil {
		t.Fatal(err)
	}
	released, err := typ.Output(context.Background(), releasedBody)
	if err != nil || !strings.Contains(released, "RoutineDeclarationBody") || !strings.Contains(released, "parser.OperatorExpr") {
		t.Fatal(released, err)
	}
	mappedBuiltin, err := stored.NewRoutineRemap(read, nil, nil, nil, false).Procedure(builtin)
	if err != nil || mappedBuiltin.DeclarationBody != builtin.DeclarationBody || mappedBuiltin.SQLBody != nil {
		t.Fatal("readonly syntax remap lost immutable owner or acquired execution authority", err)
	}
}

func TestRoutineTreeCancellation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE FUNCTION tree_cancel(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL RETURN a`)
	read := e.CatalogRead()
	typ := routineTreeType(t, read)
	defaults, body, err := (core.EngineCatalogRuntime{}).RoutineTrees(context.Background(), read, routineTreeProcedure(t, read, "tree_cancel"), typ)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failedDefaults, failedBody, err := (core.EngineCatalogRuntime{}).RoutineTrees(ctx, read, routineTreeProcedure(t, read, "tree_cancel"), typ)
	if !errors.Is(err, context.Canceled) || failedDefaults.Type() != nil || failedBody.Type() != nil {
		t.Fatal("canceled capture published values", err)
	}
	for _, value := range []sqltype.Value{defaults, body} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := typ.Output(ctx, value); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled output succeeded", err)
		}
		text, err := typ.Output(context.Background(), value)
		if err != nil || text == "" {
			t.Fatal("cancellation poisoned lazy output", text, err)
		}
		if again, err := typ.Output(context.Background(), value); err != nil || again != text {
			t.Fatal("unstable cached output", again, err)
		}
	}
}
