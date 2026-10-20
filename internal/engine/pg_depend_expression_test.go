package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgDependUnitRefs(t *testing.T, got, want []ids.ObjectAddress) {
	t.Helper()
	bag := func(rows []ids.ObjectAddress) map[ids.ObjectAddress]int {
		out := map[ids.ObjectAddress]int{}
		for _, row := range rows {
			out[row]++
		}
		return out
	}
	if !reflect.DeepEqual(bag(got), bag(want)) {
		t.Fatalf("reference bag got %#v want %#v", got, want)
	}
}
func TestPGDependExpressionReferences(t *testing.T) {
	e, view, integer := pgDependUnitCatalog(t)
	parent := pgDependUnitAddress(1259, 48001, 0)
	column := parent
	column.SubID = 3
	view.State().SetDescriptor(parent, &catalog.Relation{ObjectAddress: parent, RelationKind: catalog.TableRelation, Attributes: []int32{3}, Columns: []catalog.Column{{Usage: integer.Usage()}}})
	provider := pgDependUnitAddress(1259, 48002, 0)
	view.State().SetDescriptor(provider, &catalog.Relation{ObjectAddress: provider, RelationKind: catalog.TableRelation})
	domain, err := sqltype.Domain(48003, "d", "public.d", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	view.State().SetDescriptor(pgDependUnitAddress(1247, 48003, 0), &catalog.Type{SQLType: domain})
	regclass, ok := view.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("regclass")
	}
	array, ok := view.LookupArray(regclass)
	if !ok {
		t.Fatal("regclass array")
	}
	ref := func(a ids.ObjectAddress) ids.ObjectRef {
		return e.catalog.Domain.Ref(view.State().Epoch(), a)
	}
	variable := plan.ColumnExpr{Info: expr.Info{Usage: domain.Usage()}, Origin: ref(column)}
	scalar := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref(provider)}}
	cases := []struct {
		name string
		expr expr.Expr
		want []ids.ObjectAddress
	}{
		{"domain_var", variable, []ids.ObjectAddress{column}},
		{"domain_const", plan.LiteralExpr{Info: expr.Info{Usage: domain.Usage()}}, []ids.ObjectAddress{pgDependUnitAddress(1247, 48003, 0)}},
		{"domain_coercion", plan.CoercionExpr{Info: expr.Info{Usage: domain.Usage()}, Child: variable}, []ids.ObjectAddress{pgDependUnitAddress(1247, 48003, 0), column}},
		{"scalar_regclass", scalar, []ids.ObjectAddress{provider}},
		{"null_regclass", plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}}, nil},
		{"absent_regclass", plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}}, nil},
		{"regclass_array", plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}}, nil},
		{"runtime_numeric_cast", plan.CoercionExpr{Info: expr.Info{Usage: regclass.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}}}, nil},
		{"repeated_var", functions.Binary{Info: expr.Info{Usage: domain.Usage()}, Args: []expr.Expr{variable, variable}}, []ids.ObjectAddress{column}},
		{"dead_case", plan.CaseExpr{Arms: []plan.CaseBranch{{Condition: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}}, Result: variable}}, Otherwise: scalar}, []ids.ObjectAddress{column, provider}},
		{"tableoid", plan.TableOIDExpr{Owner: ref(parent)}, []ids.ObjectAddress{pgDependUnitAddress(1259, 48001, -6)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pgDependExpressionRefs(context.Background(), view, tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			pgDependUnitRefs(t, got, tc.want)
		})
	}
	collection := pgDependRefs{ctx: context.Background(), catalog: view}
	collection.add(parent)
	collection.add(column)
	collection.add(column)
	collection.add(provider)
	got, err := collection.finish()
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column, provider})
	for _, bad := range []ids.ObjectAddress{pgDependUnitAddress(1259, 49999, 0), pgDependUnitAddress(1259, 48001, 2)} {
		r := pgDependRefs{ctx: context.Background(), catalog: view}
		r.add(bad)
		if _, err := r.finish(); err == nil {
			t.Fatal("missing authority accepted", bad)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := pgDependExpressionRefs(canceled, view, scalar); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal(got, err)
	}
}
func TestPGDependQuerySourceSuppression(t *testing.T) {
	e, view, integer := pgDependUnitCatalog(t)
	table := pgDependUnitAddress(1259, 49001, 0)
	column := table
	column.SubID = 4
	view.State().SetDescriptor(table, &catalog.Relation{ObjectAddress: table, RelationKind: catalog.TableRelation, Attributes: []int32{4}, Columns: []catalog.Column{{Usage: integer.Usage()}}})
	ref := e.catalog.Domain.Ref(view.State().Epoch(), table)
	source := &plan.SourceNode{Ref: ref}
	relationOnly := &plan.Query{Root: source}
	got, err := pgDependQueryRefs(context.Background(), view, nil, relationOnly)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{table})
	ref.Address = column
	variable := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Origin: ref}
	query := &plan.Query{Root: &plan.ProjectNode{Child: source, Expressions: []expr.Expr{variable, variable}}}
	got, err = pgDependQueryRefs(context.Background(), view, nil, query)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
	nested := plan.SubqueryExpr{QueryPlan: query}
	got, err = pgDependExpressionRefs(context.Background(), view, nested)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
	aggregate := &plan.Query{Root: &plan.ProjectNode{Child: &plan.AggregateNode{Child: source, Keys: []expr.Expr{variable}, Calls: []plan.AggregateCall{{Argument: variable, Order: []plan.SortKey{{Expression: variable}}}}}, Expressions: []expr.Expr{plan.GroupExpr{}, plan.AggregateExpr{}}}}
	got, err = pgDependQueryRefs(context.Background(), view, nil, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
}
