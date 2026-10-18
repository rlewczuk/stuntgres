package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

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
	node, err := b.bindIntaggFunction(raw)
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
	if call.procedure != e.state.descriptors[ext.members[0]] || call.ref.Address != ext.members[0] || !call.includeNulls || call.operation != aggregateArrayScalar || provider.ArgumentType() != integer || provider.ResultUsage().Type != array {
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
	proc.revision++
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
	if n.procedure != e.state.descriptors[ext.members[1]] || n.ref.Address != ext.members[1] || n.usage.Type.ID() != sqltype.Int4 {
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
	n.args = []BoundExpr{queryProbe(n.args[0].ResultUsage(), func(*EvalContext) (sqltype.Value, error) { count++; return input, nil })}
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
		value, present, err := first.next(c.Context, n.usage.Type)
		if err != nil || !present || value.Type() != n.usage.Type {
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
	if _, present, err := first.next(c.Context, n.usage.Type); err != nil || present {
		t.Fatal("extra element", present, err)
	}
	if _, present, err := second.next(c.Context, n.usage.Type); err != nil || !present {
		t.Fatal("cursor state shared", present, err)
	}
	for _, wholeNull := range []bool{false, true} {
		empty := intaggUnitEnum(t, e, "{}")
		if wholeNull {
			empty.args = []BoundExpr{literalExpression{expressionInfo: boundExpressionInfo(empty.args[0]), value: sqltype.Null(empty.args[0].ResultUsage().Type)}}
		}
		cursor, err := openFunctionCall(c, empty)
		if err != nil {
			t.Fatal(err)
		}
		_, present, err := cursor.next(c.Context, empty.usage.Type)
		cursor.close()
		if err != nil || present {
			t.Fatal("whole NULL/empty yielded row", present, err)
		}
	}
	mapped, changed, err := n.mapChildren(func(arg BoundExpr) (BoundExpr, bool, error) {
		return literalExpression{expressionInfo: boundExpressionInfo(arg), value: input}, true, nil
	})
	if err != nil || !changed || mapped.procedure != n.procedure || mapped.ref != n.ref {
		t.Fatal("child rewrite lost installation", err)
	}
	for _, foreign := range []bool{false, true} {
		bad := n.ref
		if foreign {
			bad = New().domain.Ref(bad.Epoch, bad.Address)
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
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.address: {ClassID: ExtensionClassID, ObjectID: 900000}}
	for i, address := range ext.members {
		mapping[address] = ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: ids.OID(900001 + i)}
	}
	aggregate := before.state.descriptors[ext.members[0]].(*procedureDescriptor)
	for i, helper := range aggregate.aggregate.helpers {
		mapping[ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: helper.id}] = ids.ObjectAddress{ClassID: ProcedureClassID, ObjectID: ids.OID(900003 + i)}
	}
	next := cloneCatalog(before.state)
	for address := range before.state.descriptors {
		if _, moved := mapping[address]; !moved {
			continue
		}
		delete(next.names, descriptorNameKey(before.state.descriptors[address]))
		delete(next.descriptors, address)
	}
	for address, original := range before.state.descriptors {
		destination, moved := mapping[address]
		if !moved {
			continue
		}
		descriptor := remapExtensionDescriptor(original, mapping)
		next.descriptors[destination] = descriptor
		next.names[descriptorNameKey(descriptor)] = destination
	}
	next.epoch++
	view := CatalogRead{owner: e, state: next}
	ref := func(old ids.ObjectRef) ids.ObjectRef {
		old.Epoch = next.epoch
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
	if remapped.procedure != next.descriptors[mapping[call.ref.Address]] || remapped.ref != ref(call.ref) || remapped.provider == call.provider {
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
	if n.ref != ref(enum.ref) || n.procedure != next.descriptors[n.ref.Address] {
		t.Fatal("enum remap authority", n.ref)
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), n, pgRenderParent{})
	if err != nil || !strings.Contains(text, "int_array_enum") || strings.Contains(text, "unnest") {
		t.Fatal("wrapper renderer", text, err)
	}
	if call.procedure != before.state.descriptors[call.ref.Address] || enum.ref.Epoch != before.Epoch() {
		t.Fatal("source authority mutated")
	}
	delete(next.descriptors, n.ref.Address)
	if err := validateIntaggIdentity(view, n.procedure, n.ref, "int_array_enum"); err == nil {
		t.Fatal("missing destination procedure accepted")
	}
}
