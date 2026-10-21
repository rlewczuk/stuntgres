package engine

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func intaggUnitAggregate(t *testing.T, e *Engine) (*analyze.Binder, plan.AggregateCall) {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.AggregateExpr{Name: "int_array_aggregate", Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}}}
	if _, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext}); err != nil {
		t.Fatal(err)
	}
	if len(b.Grouping.Aggregates) != 1 {
		t.Fatal("aggregate state slots", len(b.Grouping.Aggregates))
	}
	return b, b.Grouping.Aggregates[0]
}

func intaggUnitEnum(t *testing.T, e *Engine, input string) plan.UnnestExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "int_array_enum"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.UnnestExpr)
}

func TestIntaggAggregateIdentity(t *testing.T) {
	t.Run("VALUES scope", intaggUnitValuesScope)
	e, ext := intaggUnitInstalled(t)
	b, call := intaggUnitAggregate(t, e)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	array, _ := e.CatalogRead().LookupArray(integer)
	provider := call.Provider.(*functions.ArrayAggregateProvider)
	if call.Procedure != e.catalog.state.Get(ext.Members[0]) || call.Ref.Address != ext.Members[0] || !call.IncludeNulls || call.Operation != functions.AggregateArrayScalar || provider.ArgumentType() != integer || provider.ResultUsage().Type != array {
		t.Fatal("fixed scalar wrapper authority", call)
	}
	if !b.SameAggregateCall(call, call) {
		t.Fatal("same installed aggregate unequal")
	}
	builtin := call
	builtin.Procedure, builtin.Ref = nil, ids.ObjectRef{}
	if b.SameAggregateCall(call, builtin) {
		t.Fatal("wrapper/builtin identity collapsed")
	}
	changed := call
	changed.Ref.Epoch++
	if b.SameAggregateCall(call, changed) {
		t.Fatal("aggregate epoch collapsed")
	}
	proc := *call.Procedure
	proc.ObjectRevision++
	changed = call
	changed.Procedure = &proc
	if b.SameAggregateCall(call, changed) {
		t.Fatal("aggregate revision collapsed")
	}
	ctx := context.Background()
	state, err := provider.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []sqltype.Value{two, sqltype.Null(integer), two} {
		if err := provider.Advance(ctx, state, value, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := provider.Finalize(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	text, err := array.Output(ctx, result)
	if err != nil || text != "{2,NULL,2}" || result.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 3}) {
		t.Fatal("NULL transition/shape", text, err)
	}
	empty, err := provider.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	value, err := provider.Finalize(ctx, empty)
	if err != nil || !value.IsNull() || value.Type() != array {
		t.Fatal("empty group", value, err)
	}
	root := &plan.AggregateNode{Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{call}}
	refs := pgDependRefs{Context: ctx, Catalog: e.CatalogRead()}
	refs.Query(root)
	if refs.Err != nil {
		t.Fatal(refs.Err)
	}
	if _, ok := refs.Addresses[call.Ref.Address]; !ok {
		t.Fatal("plan.AggregateNode lost procedure dependency")
	}
}

func TestIntaggEnumerationIdentity(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	n := intaggUnitEnum(t, e, "{2,2,NULL}")
	if n.Procedure != e.catalog.state.Get(ext.Members[1]) || n.Ref.Address != ext.Members[1] || n.Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("fixed enum identity", n)
	}
	builtin := n
	builtin.Procedure, builtin.Ref = nil, ids.ObjectRef{}
	if analyze.SameGroupExpression(n, builtin) || analyze.SameQuerySetOccurrence(n, builtin) {
		t.Fatal("enum/builtin identity collapsed")
	}
	refs, err := stored.ExpressionDependRefs(context.Background(), e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.Ref.Address}) {
		t.Fatal("enum dependency", refs, err)
	}
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead()}}
	input, err := n.Args[0].Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	n.Args = []expr.Expr{queryProbe(n.Args[0].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { count++; return input, nil })}
	first, err := exec.OpenFunctionSource(c, n)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := exec.OpenFunctionSource(c, n)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if count != 2 || first.Value != input || second.Value != input {
		t.Fatal("once per open/shared payload", count)
	}
	for _, want := range []any{int64(2), int64(2), nil} {
		value, present, err := first.Next(c.Context)
		if err != nil || !present || value.Type() != n.Usage.Type {
			t.Fatal("stored element", value, present, err)
		}
		if want == nil {
			if !value.IsNull() {
				t.Fatal("NULL element disappeared")
			}
		} else if v, err := value.Integer(); err != nil || v != want {
			t.Fatal(v, err)
		}
	}
	if _, present, err := first.Next(c.Context); err != nil || present {
		t.Fatal("extra element", present, err)
	}
	if _, present, err := second.Next(c.Context); err != nil || !present {
		t.Fatal("cursor state shared", present, err)
	}
	for _, wholeNull := range []bool{false, true} {
		empty := intaggUnitEnum(t, e, "{}")
		if wholeNull {
			empty.Args = []expr.Expr{plan.LiteralExpr{Info: expr.InfoOf(empty.Args[0]), Value: sqltype.Null(empty.Args[0].ResultUsage().Type)}}
		}
		cursor, err := exec.OpenFunctionSource(c, empty)
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := cursor.Next(c.Context)
		cursor.Close()
		if err != nil || present {
			t.Fatal("whole NULL/empty yielded row", present, err)
		}
	}
	rewritten, changed, err := expr.MapSubstituted(n, func(arg expr.Expr) (expr.Expr, bool, error) {
		return plan.LiteralExpr{Info: expr.InfoOf(arg), Value: input}, true, nil
	})
	mapped := rewritten.(plan.UnnestExpr)
	if err != nil || !changed || mapped.Procedure != n.Procedure || mapped.Ref != n.Ref {
		t.Fatal("child rewrite lost installation", err)
	}
	for _, foreign := range []bool{false, true} {
		bad := n.Ref
		if foreign {
			bad = New().catalog.Domain.Ref(bad.Epoch, bad.Address)
		} else {
			bad.Epoch++
		}
		if err := extension.ValidateIntaggIdentity(e.CatalogRead(), n.Procedure, bad, "int_array_enum"); err == nil {
			t.Fatal("foreign/stale enum accepted")
		}
	}
}

func TestIntaggIdentityRemap(t *testing.T) {
	t.Run("frozen recipes", intaggUnitRecipes)
	t.Run("retained TEMP dependencies", intaggUnitTemporaryDependencies)
	e, ext := intaggUnitInstalled(t)
	_, call := intaggUnitAggregate(t, e)
	enum := intaggUnitEnum(t, e, "{2,2,NULL}")
	before := e.CatalogRead()
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.Members {
		mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	aggregate := before.MustProcedure(ext.Members[0])
	for i, helper := range aggregate.Aggregate.Helpers {
		mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(900003 + i)}
	}
	next := catalog.CloneState(before.State())
	for address := range before.Descriptors() {
		if _, moved := mapping[address]; !moved {
			continue
		}
		next.UnbindName(catalog.DescriptorNameKey(before.Get(address)))
		next.RemoveDescriptor(address)
	}
	for address, original := range before.Descriptors() {
		destination, moved := mapping[address]
		if !moved {
			continue
		}
		descriptor := extension.RemapDescriptor(original, mapping)
		next.SetDescriptor(destination, descriptor)
		next.BindName(catalog.DescriptorNameKey(descriptor), destination)
	}
	next.SetEpoch(next.Epoch() + 1)
	view := e.catalog.read(next)
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		old.Epoch = next.Epoch()
		if address, ok := mapping[old.Address]; ok {
			old.Address = address
		}
		return old
	}
	root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "a", Usage: call.Provider.ResultUsage()}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{call}}
	mapped, err := stored.RemapViewQuery(view, root, ref, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*plan.AggregateNode).Calls[0]
	if remapped.Procedure != next.Get(mapping[call.Ref.Address]) || remapped.Ref != ref(call.Ref) || remapped.Provider == call.Provider {
		t.Fatal("aggregate remap authority", remapped)
	}
	node, err := stored.RemapExpression(enum, ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, _, err = stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	n := node.(plan.UnnestExpr)
	if n.Ref != ref(enum.Ref) || n.Procedure != next.Get(n.Ref.Address) {
		t.Fatal("enum remap authority", n.Ref)
	}
	text, err := (pgRenderer{View: view}).SQL(context.Background(), n, pgRenderParent{})
	if err != nil || !strings.Contains(text, "int_array_enum") || strings.Contains(text, "unnest") {
		t.Fatal("wrapper renderer", text, err)
	}
	if call.Procedure != before.Get(call.Ref.Address) || enum.Ref.Epoch != before.Epoch() {
		t.Fatal("source authority mutated")
	}
	next.RemoveDescriptor(n.Ref.Address)
	if err := extension.ValidateIntaggIdentity(view, n.Procedure, n.Ref, "int_array_enum"); err == nil {
		t.Fatal("missing destination procedure accepted")
	}
}
