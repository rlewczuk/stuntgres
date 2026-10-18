package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
	e, catalog, integer := pgDependUnitCatalog(t)
	parent := pgDependUnitAddress(1259, 48001, 0)
	column := parent
	column.SubID = 3
	catalog.State().SetDescriptor(parent, &relationDescriptor{ObjectAddress: parent, RelationKind: tableRelation, Attributes: []int32{3}, Columns: []ColumnDescriptor{{Usage: integer.Usage()}}})
	provider := pgDependUnitAddress(1259, 48002, 0)
	catalog.State().SetDescriptor(provider, &relationDescriptor{ObjectAddress: provider, RelationKind: tableRelation})
	domain, err := sqltype.Domain(48003, "d", "public.d", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	catalog.State().SetDescriptor(pgDependUnitAddress(1247, 48003, 0), &typeDescriptor{SQLType: domain})
	regclass, ok := catalog.LookupType(sqltype.Regclass)
	if !ok {
		t.Fatal("regclass")
	}
	array, ok := catalog.LookupArray(regclass)
	if !ok {
		t.Fatal("regclass array")
	}
	ref := func(a ids.ObjectAddress) ids.ObjectRef {
		return e.catalog.Domain.Ref(catalog.State().Epoch(), a)
	}
	variable := columnExpression{Info: expr.Info{Usage: domain.Usage()}, origin: ref(column)}
	scalar := literalExpression{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref(provider)}}
	cases := []struct {
		name string
		expr expr.Expr
		want []ids.ObjectAddress
	}{
		{"domain_var", variable, []ids.ObjectAddress{column}},
		{"domain_const", literalExpression{Info: expr.Info{Usage: domain.Usage()}}, []ids.ObjectAddress{pgDependUnitAddress(1247, 48003, 0)}},
		{"domain_coercion", coercionExpression{Info: expr.Info{Usage: domain.Usage()}, child: variable}, []ids.ObjectAddress{pgDependUnitAddress(1247, 48003, 0), column}},
		{"scalar_regclass", scalar, []ids.ObjectAddress{provider}},
		{"null_regclass", literalExpression{Info: expr.Info{Usage: regclass.Usage()}}, nil},
		{"absent_regclass", literalExpression{Info: expr.Info{Usage: regclass.Usage()}}, nil},
		{"regclass_array", literalExpression{Info: expr.Info{Usage: array.Usage()}}, nil},
		{"runtime_numeric_cast", coercionExpression{Info: expr.Info{Usage: regclass.Usage()}, child: literalExpression{Info: expr.Info{Usage: integer.Usage()}}}, nil},
		{"repeated_var", functions.Binary{Info: expr.Info{Usage: domain.Usage()}, Args: []expr.Expr{variable, variable}}, []ids.ObjectAddress{column}},
		{"dead_case", caseExpression{arms: []caseBranch{{condition: literalExpression{Info: expr.Info{Usage: integer.Usage()}}, result: variable}}, otherwise: scalar}, []ids.ObjectAddress{column, provider}},
		{"tableoid", tableOIDExpression{owner: ref(parent)}, []ids.ObjectAddress{pgDependUnitAddress(1259, 48001, -6)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pgDependExpressionRefs(context.Background(), catalog, tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			pgDependUnitRefs(t, got, tc.want)
		})
	}
	collection := pgDependRefs{ctx: context.Background(), catalog: catalog}
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
		r := pgDependRefs{ctx: context.Background(), catalog: catalog}
		r.add(bad)
		if _, err := r.finish(); err == nil {
			t.Fatal("missing authority accepted", bad)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := pgDependExpressionRefs(canceled, catalog, scalar); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal(got, err)
	}
}
func TestPGDependQuerySourceSuppression(t *testing.T) {
	e, catalog, integer := pgDependUnitCatalog(t)
	table := pgDependUnitAddress(1259, 49001, 0)
	column := table
	column.SubID = 4
	catalog.State().SetDescriptor(table, &relationDescriptor{ObjectAddress: table, RelationKind: tableRelation, Attributes: []int32{4}, Columns: []ColumnDescriptor{{Usage: integer.Usage()}}})
	ref := e.catalog.Domain.Ref(catalog.State().Epoch(), table)
	source := &sourceNode{ref: ref}
	relationOnly := &BoundQuery{Root: source}
	got, err := pgDependQueryRefs(context.Background(), catalog, relationOnly)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{table})
	ref.Address = column
	variable := columnExpression{Info: expr.Info{Usage: integer.Usage()}, origin: ref}
	query := &BoundQuery{Root: &projectNode{child: source, expressions: []expr.Expr{variable, variable}}}
	got, err = pgDependQueryRefs(context.Background(), catalog, query)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
	nested := subqueryExpression{queryPlan: query}
	got, err = pgDependExpressionRefs(context.Background(), catalog, nested)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
	aggregate := &BoundQuery{Root: &projectNode{child: &aggregateNode{child: source, keys: []expr.Expr{variable}, calls: []aggregateCall{{argument: variable, order: []sortKey{{expression: variable}}}}}, expressions: []expr.Expr{groupExpression{}, aggregateExpression{}}}}
	got, err = pgDependQueryRefs(context.Background(), catalog, aggregate)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{column})
}
