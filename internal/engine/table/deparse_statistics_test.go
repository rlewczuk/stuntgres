package table

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func statisticsDeparseFixture(t *testing.T) (DeparseInputs, *catalog.Statistics, *catalog.Relation) {
	t.Helper()
	domain, principals := &ids.Domain{}, &authorization.Domain{}
	state := catalog.NewState(3)
	owner := &catalog.Owner{Domain: domain, Principals: principals}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	namespace := &catalog.Namespace{ObjectAddress: ns, ObjectName: "public"}
	state.SetDescriptor(ns, namespace)
	state.BindName(catalog.DescriptorNameKey(namespace), ns)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	view := catalog.NewRead(owner, state)
	integer, _ := view.LookupType(sqltype.Int4)
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 41000}, ObjectNamespace: ns, ObjectName: "stats_t", RelationKind: catalog.TableRelation,
		Columns: []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: integer.Usage()}}, Attributes: []int32{1, 3}}
	state.SetDescriptor(relation.Address(), relation)
	state.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	input := relation.Address()
	input.SubID = 1
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: domain.Ref(view.Epoch(), input), Scope: 1, Frame: 1}
	oneValue, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	one := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: oneValue}
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, integer, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	definition := plan.ArithmeticExpr{Info: expr.Info{Usage: integer.Usage(), Property: column.Properties()}, Left: column, Right: one, Operator: sqltype.ArithmeticAdd, Provider: provider, Namespace: "pg_catalog"}
	recipe := catalog.NewStatisticsExpression(definition, definition, []int32{1}, []ids.ObjectRef{domain.Ref(view.Epoch(), input)}, nil, integer.Usage())
	// A constant expression has a genuinely folded execution tree. Presentation
	// must preserve its original operation while also never evaluating row Vars.
	constant := plan.ArithmeticExpr{Info: expr.Info{Usage: integer.Usage()}, Left: one, Right: one, Operator: sqltype.ArithmeticAdd, Provider: provider, Namespace: "pg_catalog"}
	executionValue, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	execution := &plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: executionValue}
	folded := catalog.NewStatisticsExpression(constant, *execution, nil, nil, nil, integer.Usage())
	selection, err := catalog.NewStatisticsSelection([]int32{3}, 2, "fme")
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Statistics{ObjectAddress: ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: 41001}, ObjectNamespace: ns, ObjectName: "stats_s", ObjectRevision: 1,
		Owner: principals.Principal(10), Relation: domain.Ref(view.Epoch(), relation.Address()), Selection: selection, Expressions: catalog.NewStatisticsExpressions([]catalog.StatisticsExpression{recipe, folded})}
	state.SetDescriptor(d.Address(), d)
	state.BindName(catalog.DescriptorNameKey(d), d.Address())
	if err := (catalog.StatisticsProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	return DeparseInputs{Catalog: view, Path: []ids.ObjectAddress{ns}}, d, relation
}

func TestDeparseStatisticsAuthority(t *testing.T) {
	in, definition, relation := statisticsDeparseFixture(t)
	view := in.Catalog
	text, _ := view.LookupType(sqltype.Text)
	oid, _ := view.LookupType(sqltype.Oid)
	value, err := sqltype.OIDValue(oid, uint32(definition.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context, in DeparseInputs, value sqltype.Value) (sqltype.Value, error) {
		return Deparse(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, in, functions.DeparseStatistics, [3]sqltype.Value{value}, text)
	}
	check := func(in DeparseInputs, want string) {
		got, err := call(t.Context(), in, value)
		if err != nil {
			t.Fatal(err)
		}
		if actual, err := got.Text(); err != nil || actual != want {
			t.Fatal(actual, want, err)
		}
	}
	check(in, "CREATE STATISTICS public.stats_s (dependencies, mcv) ON b, (a + 1), (1 + 1) FROM stats_t")
	backing := definition.Expressions
	input := backing.At(0).Input(0)
	if input.Domain() != view.Domain() || input.Epoch != view.Epoch() || input.Address.SubID != 1 || definition.Selection.Key(0) != 3 {
		t.Fatal("lost stable authority")
	}
	if view.Get(definition.Address()) != definition || view.Get(relation.Address()) != relation {
		t.Fatal("descriptor identity")
	}
	state := catalog.CloneState(view.State())
	renamed := *relation
	renamed.Columns = append([]catalog.Column(nil), relation.Columns...)
	renamed.Columns[0].Name = "New A"
	state.SetDescriptor(renamed.Address(), &renamed)
	current := in
	current.Catalog = view.WithState(state)
	current.Output.QuoteAllIdentifiers = true
	check(current, `CREATE STATISTICS "public"."stats_s" (dependencies, mcv) ON "b", ("New A" + 1), (1 + 1) FROM "stats_t"`)
	check(in, "CREATE STATISTICS public.stats_s (dependencies, mcv) ON b, (a + 1), (1 + 1) FROM stats_t")
	if definition.Expressions != backing || definition.Expressions.At(0).Input(0) != input || relation.Columns[0].Name != "a" {
		t.Fatal("presentation mutated retained backing")
	}
	for _, change := range []func(*catalog.Statistics){
		func(d *catalog.Statistics) { d.Relation.Epoch++ },
		func(d *catalog.Statistics) { d.Relation = (&ids.Domain{}).Ref(view.Epoch(), relation.Address()) },
		func(d *catalog.Statistics) { d.Owner = (&authorization.Domain{}).Principal(10) },
		func(d *catalog.Statistics) { d.Relation.Address.ObjectID++ },
	} {
		bad := *definition
		change(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		broken := in
		broken.Catalog = view.WithState(state)
		if got, err := call(t.Context(), broken, value); err == nil || got.Type() != nil {
			t.Fatal("foreign/stale authority became NULL or text", got, err)
		}
	}
	replaced := *definition
	replaced.ObjectRevision++
	state = catalog.CloneState(view.State())
	state.SetDescriptor(replaced.Address(), &replaced)
	if (catalog.StatisticsProvider{}).Validate(view.WithState(state), definition) == nil {
		t.Fatal("replaced descriptor retained current authority")
	}
	if (catalog.StatisticsProvider{}).Validate(view, &replaced) == nil {
		t.Fatal("uninstalled descriptor admitted")
	}
	for _, id := range []uint32{0, uint32(relation.Address().ObjectID), 3415} {
		missing, err := sqltype.OIDValue(oid, id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := call(t.Context(), in, missing)
		if err != nil || !got.IsNull() || got.Type() != text {
			t.Fatal("class-specific missing typed NULL", got, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := call(ctx, in, value); !errors.Is(err, context.Canceled) || got.Type() != nil {
		t.Fatal("cancellation published text", got, err)
	}
	if got, err := call(t.Context(), DeparseInputs{}, value); err == nil || got.Type() != nil {
		t.Fatal("missing authority became NULL", got, err)
	}
	for _, at := range []int{2, 3, 4} {
		ctx, cancel := context.WithCancel(t.Context())
		bounded := &statisticsDeparseCancelContext{Context: ctx, cancel: cancel, remaining: at}
		got, err := call(bounded, in, value)
		cancel()
		if !errors.Is(err, context.Canceled) || got.Type() != nil {
			t.Fatal("mid-render cancellation published partial text", at, got, err)
		}
	}
}

type statisticsDeparseCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *statisticsDeparseCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
