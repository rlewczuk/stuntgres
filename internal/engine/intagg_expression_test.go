package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func intaggUnitAggregate(t *testing.T, e *Engine) (*expressionBinder, aggregateCall) {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	raw := parser.AggregateExpr{Name: "int_array_aggregate", Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 2}}}}
	if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err != nil {
		t.Fatal(err)
	}
	if len(b.aggregates) != 1 {
		t.Fatal("aggregate state slots", len(b.aggregates))
	}
	return b, b.aggregates[0]
}

func intaggUnitEnum(t *testing.T, e *Engine, input string) unnestExpression {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "int_array_enum"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: input}}}}
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(unnestExpression)
}

func TestIntaggAggregateIdentity(t *testing.T) {
	t.Run("VALUES scope", intaggUnitValuesScope)
	e, ext := intaggUnitInstalled(t)
	b, call := intaggUnitAggregate(t, e)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	array, _ := e.CatalogRead().LookupArray(integer)
	provider := call.provider.(*functions.ArrayAggregateProvider)
	if call.procedure != e.catalog.state.Get(ext.Members[0]) || call.ref.Address != ext.Members[0] || !call.includeNulls || call.operation != functions.AggregateArrayScalar || provider.ArgumentType() != integer || provider.ResultUsage().Type != array {
		t.Fatal("fixed scalar wrapper authority", call)
	}
	if !b.sameAggregateCall(call, call) {
		t.Fatal("same installed aggregate unequal")
	}
	builtin := call
	builtin.procedure, builtin.ref = nil, ids.ObjectRef{}
	if b.sameAggregateCall(call, builtin) {
		t.Fatal("wrapper/builtin identity collapsed")
	}
	changed := call
	changed.ref.Epoch++
	if b.sameAggregateCall(call, changed) {
		t.Fatal("aggregate epoch collapsed")
	}
	proc := *call.procedure
	proc.ObjectRevision++
	changed = call
	changed.procedure = &proc
	if b.sameAggregateCall(call, changed) {
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
	root := &aggregateNode{child: &valuesNode{}, calls: []aggregateCall{call}}
	refs := pgDependRefs{ctx: ctx, catalog: e.CatalogRead()}
	refs.query(root)
	if refs.err != nil {
		t.Fatal(refs.err)
	}
	if _, ok := refs.addresses[call.ref.Address]; !ok {
		t.Fatal("aggregateNode lost procedure dependency")
	}
}

func TestIntaggEnumerationIdentity(t *testing.T) {
	e, ext := intaggUnitInstalled(t)
	n := intaggUnitEnum(t, e, "{2,2,NULL}")
	if n.procedure != e.catalog.state.Get(ext.Members[1]) || n.ref.Address != ext.Members[1] || n.Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("fixed enum identity", n)
	}
	builtin := n
	builtin.procedure, builtin.ref = nil, ids.ObjectRef{}
	if sameGroupExpression(n, builtin) || sameQuerySetOccurrence(n, builtin) {
		t.Fatal("enum/builtin identity collapsed")
	}
	refs, err := pgDependExpressionRefs(context.Background(), e.CatalogRead(), n)
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectAddress{n.ref.Address}) {
		t.Fatal("enum dependency", refs, err)
	}
	c := &EvalContext{Context: context.Background(), catalog: e.CatalogRead()}
	input, err := n.args[0].Eval(c)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	n.args = []expr.Expr{queryProbe(n.args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { count++; return input, nil })}
	first, err := openFunctionCall(c, n)
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openFunctionCall(c, n)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	if count != 2 || first.array != input || second.array != input {
		t.Fatal("once per open/shared payload", count)
	}
	for _, want := range []any{int64(2), int64(2), nil} {
		value, present, err := first.next(c.Context, n.Usage.Type)
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
	if _, present, err := first.next(c.Context, n.Usage.Type); err != nil || present {
		t.Fatal("extra element", present, err)
	}
	if _, present, err := second.next(c.Context, n.Usage.Type); err != nil || !present {
		t.Fatal("cursor state shared", present, err)
	}
	for _, wholeNull := range []bool{false, true} {
		empty := intaggUnitEnum(t, e, "{}")
		if wholeNull {
			empty.args = []expr.Expr{literalExpression{Info: expr.InfoOf(empty.args[0]), value: sqltype.Null(empty.args[0].ResultUsage().Type)}}
		}
		cursor, err := openFunctionCall(c, empty)
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := cursor.next(c.Context, empty.Usage.Type)
		cursor.close()
		if err != nil || present {
			t.Fatal("whole NULL/empty yielded row", present, err)
		}
	}
	mapped, changed, err := n.mapChildren(func(arg expr.Expr) (expr.Expr, bool, error) {
		return literalExpression{Info: expr.InfoOf(arg), value: input}, true, nil
	})
	if err != nil || !changed || mapped.procedure != n.procedure || mapped.ref != n.ref {
		t.Fatal("child rewrite lost installation", err)
	}
	for _, foreign := range []bool{false, true} {
		bad := n.ref
		if foreign {
			bad = New().catalog.Domain.Ref(bad.Epoch, bad.Address)
		} else {
			bad.Epoch++
		}
		if err := validateIntaggIdentity(e.CatalogRead(), n.procedure, bad, "int_array_enum"); err == nil {
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
	next := cloneCatalog(before.State())
	for address := range before.Descriptors() {
		if _, moved := mapping[address]; !moved {
			continue
		}
		next.UnbindName(descriptorNameKey(before.Get(address)))
		next.RemoveDescriptor(address)
	}
	for address, original := range before.Descriptors() {
		destination, moved := mapping[address]
		if !moved {
			continue
		}
		descriptor := remapExtensionDescriptor(original, mapping)
		next.SetDescriptor(destination, descriptor)
		next.BindName(descriptorNameKey(descriptor), destination)
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
	root := &aggregateNode{querySchema: querySchema{{Name: "a", Usage: call.provider.ResultUsage()}}, child: &valuesNode{}, calls: []aggregateCall{call}}
	mapped, err := remapViewQuery(view, root, ref, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	remapped := mapped.(*aggregateNode).calls[0]
	if remapped.procedure != next.Get(mapping[call.ref.Address]) || remapped.ref != ref(call.ref) || remapped.provider == call.provider {
		t.Fatal("aggregate remap authority", remapped)
	}
	node, err := remapBoundExpression(enum, ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	node, _, err = retagBoundTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil {
		t.Fatal(err)
	}
	n := node.(unnestExpression)
	if n.ref != ref(enum.ref) || n.procedure != next.Get(n.ref.Address) {
		t.Fatal("enum remap authority", n.ref)
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), n, pgRenderParent{})
	if err != nil || !strings.Contains(text, "int_array_enum") || strings.Contains(text, "unnest") {
		t.Fatal("wrapper renderer", text, err)
	}
	if call.procedure != before.Get(call.ref.Address) || enum.ref.Epoch != before.Epoch() {
		t.Fatal("source authority mutated")
	}
	next.RemoveDescriptor(n.ref.Address)
	if err := validateIntaggIdentity(view, n.procedure, n.ref, "int_array_enum"); err == nil {
		t.Fatal("missing destination procedure accepted")
	}
}
