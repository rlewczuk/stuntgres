package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func generatedUnitDescriptor(t *testing.T) (*Engine, catalog.Read, *catalog.Relation) {
	t.Helper()
	e := New()
	view := e.CatalogRead()
	integer, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	var namespace ids.ObjectAddress
	for address, descriptor := range view.Descriptors() {
		if descriptor.Kind() == ids.NamespaceObject && descriptor.Name() == "public" {
			namespace = address
			break
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: namespace, ObjectName: "g", ObjectRevision: 1, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: true}, {Name: "b", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Index: map[string]int{"a": 0, "b": 1}}
	return e, view, d
}

func TestGeneratedTypedRowAndCancellation(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	ref.Address.SubID = 1
	node := plan.ColumnExpr{Info: expr.Info{Usage: d.Columns[0].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: 0}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: node, Planned: node}}
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	row := []sqltype.Value{value, sqltype.Null(value.Type())}
	eval := EvalContext{}
	if err := evaluateGenerations(context.Background(), d, row, &eval); err != nil {
		t.Fatal(err)
	}
	if n, err := row[1].Integer(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	row[0] = sqltype.Null(value.Type())
	if err := evaluateGenerations(context.Background(), d, row, &eval); err != nil || !row[1].IsNull() {
		t.Fatal(row, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := evaluateGenerations(canceled, d, row, &eval); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	wrong := EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 2, Frame: 1, Row: row}}
	if _, err := node.Eval(&wrong); err == nil {
		t.Fatal("wrong frame accepted")
	}
}

func TestGeneratedOriginalReferencesAndPrivateReadManifest(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored}}
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	columns := []queryColumn{{descriptor: d.Columns[0], slot: 0, leaf: 0, physical: 0}, {descriptor: d.Columns[1], slot: 1, leaf: 0, physical: 1}}
	ns := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: "g", original: "g", schema: "public", ref: ref, columns: columns}}, frame: d.Columns}
	auth := AuthorizationView{View: access.View{Catalog: view}}
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.target.generatedOwner = d
	b.query.auth = &auth
	b.query.namespace = &ns
	b.query.sources = []queryLeaf{{Ref: ref, Relation: d}}
	raw := func(name string) parser.ColumnExpr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	if _, err := b.column(raw("a")); err != nil {
		t.Fatal(err)
	}
	if len(b.target.uses) != 0 {
		t.Fatal("generation added DML explicit read manifest", b.target.uses)
	}
	for _, tc := range []struct{ name, code string }{{"b", "42P17"}, {"g", "42P17"}, {"xmin", "42P10"}, {"missing", "42703"}} {
		_, err := b.column(raw(tc.name))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}

func TestGeneratedPhysicalVolatilityAndTableOIDRemap(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	oid, _ := view.LookupType(sqltype.Oid)
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	leaf := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ref}
	volatile := functions.Random{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	if generationImmutable(volatile) {
		t.Fatal("volatile accepted")
	}
	pruned := plan.CaseExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, Otherwise: leaf}
	if !generationImmutable(pruned) || !generationHasTableOID(pruned) {
		t.Fatal("surviving provider walk uses inherited volatility")
	}
	remapped, err := remapBoundExpression(pruned, func(old ids.ObjectRef) ids.ObjectRef { old.Address.ObjectID = 26000; return old }, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := remapped.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.OID(); err != nil || n != 26000 {
		t.Fatal(n, err)
	}
	if leaf.Owner.Address.ObjectID != 25000 {
		t.Fatal("source mutated")
	}
}

func TestGeneratedTableOIDStructuralEquality(t *testing.T) {
	e, view, parent := generatedUnitDescriptor(t)
	oid, _ := view.LookupType(sqltype.Oid)
	owner := e.catalog.Domain.Ref(view.Epoch(), parent.Address())
	original := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: owner}
	child := *parent
	child.ObjectAddress.ObjectID++
	mapper := createLikeInput{source: parent}
	left, err := mapper.remap(original, &child, view)
	if err != nil {
		t.Fatal(err)
	}
	right, err := mapper.remap(original, &child, view)
	if err != nil {
		t.Fatal(err)
	}
	if !sameGroupExpression(left, right) || sameGroupExpression(original, left) || original.Owner != owner {
		t.Fatal("mapped recipe equality lost exact row owner or mutated the source")
	}
	mapped := left.(plan.TableOIDExpr)
	for _, change := range []func(*plan.TableOIDExpr){
		func(n *plan.TableOIDExpr) { n.Owner = New().catalog.Domain.Ref(n.Owner.Epoch, n.Owner.Address) },
		func(n *plan.TableOIDExpr) { n.Owner.Epoch++ },
		func(n *plan.TableOIDExpr) { n.Owner.Address.ObjectID++ },
		func(n *plan.TableOIDExpr) { n.Owner.Address.SubID++ },
		func(n *plan.TableOIDExpr) { n.Usage = parent.Columns[0].Usage },
	} {
		different := mapped
		change(&different)
		if sameGroupExpression(mapped, different) || sameGroupExpression(different, mapped) {
			t.Fatal("distinct physical row authority or full Usage compared equal")
		}
	}
	column := plan.ColumnExpr{Info: mapped.Info, Origin: mapped.Owner}
	if sameGroupExpression(mapped, column) {
		t.Fatal("different expression kind compared equal")
	}
}

func TestGeneratedNullDefinitionValidationAndIdentity(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	op := &Operation{ctx: context.Background()}
	declarations := []createColumnDeclaration{{}, {generation: &parser.GenerationDefinition{Kind: "stored", Expression: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}}
	if err := e.bindCreateGenerations(op, AuthorizationView{View: access.View{Catalog: view}}, d, declarations, sqltype.InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err := e.assignDefinitionOIDsLocked(d)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := d.ColumnMeta.Generations[2]
	if definition.OID == 0 || !structuralNull(definition.Expression) || len(d.ColumnMeta.Defaults) != 0 {
		t.Fatal("generated NULL pruned/conflated", definition)
	}
	candidate := catalog.CloneState(view.State())
	candidate.SetDescriptor(d.Address(), d)
	candidate.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	read := e.catalog.read(candidate)
	if err := (relationProvider{}).Validate(read, d); err != nil {
		t.Fatal(err)
	}
	if !definitionOIDUsed(d, definition.OID) {
		t.Fatal("definition identity not reserved")
	}
	for _, mutate := range []func(*catalog.ColumnGeneration){
		func(g *catalog.ColumnGeneration) { g.Kind = 0 },
		func(g *catalog.ColumnGeneration) { g.Kind = catalog.GenerationKind(99) },
		func(g *catalog.ColumnGeneration) { g.OID = 0 },
		func(g *catalog.ColumnGeneration) { g.Dependencies = nil },
		func(g *catalog.ColumnGeneration) { g.Planned = nil },
	} {
		bad := *d
		replacement := definition
		mutate(&replacement)
		bad.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: replacement}
		if err := (relationProvider{}).Validate(read, &bad); err == nil {
			t.Fatal("invalid generated definition accepted")
		}
	}
}

func TestGeneratedReplacementPrivateRowsAndCancellation(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	oid, _ := view.LookupType(sqltype.Oid)
	oldAddress := d.Address()
	d.ObjectAddress.ObjectID = 26000
	d.Columns[1].Usage = oid.Usage()
	ref := e.catalog.Domain.Ref(view.Epoch()+1, d.Address())
	leaf := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ref}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: leaf, Planned: leaf}}
	base, _ := sqltype.Integer(d.Columns[0].Usage.Type, 2)
	oldOID, _ := sqltype.OIDValue(oid, uint32(oldAddress.ObjectID))
	source := []sqltype.Value{base, oldOID}
	rows := &storage.Relation{Versions: []storage.RowVersion{{Values: source}}}
	candidate := catalog.CloneState(view.State())
	candidate.SetDescriptor(d.Address(), d)
	relations := map[ids.ObjectAddress]*storage.Relation{d.Address(): rows}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{oldAddress: d.Address()}
	if err := recomputeRemappedGenerations(context.Background(), candidate, relations, mapping, EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := source[1].OID(); n != uint32(oldAddress.ObjectID) {
		t.Fatal("source row mutated", n)
	}
	if n, _ := rows.Versions[0].Values[1].OID(); n != uint32(d.Address().ObjectID) {
		t.Fatal("owner not recomputed", n)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recomputeRemappedGenerations(canceled, candidate, relations, mapping, EvalContext{EvalStatement: evalStatement{Context: canceled}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	d.Constraints.Check = []catalog.CheckConstraint{{Name: "blocked", Expression: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}}
	if err := recomputeRemappedGenerations(context.Background(), candidate, relations, mapping, EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, nil); err == nil {
		t.Fatal("replacement ignored completed-row CHECK")
	}
	if n, _ := source[1].OID(); n != uint32(oldAddress.ObjectID) {
		t.Fatal("constraint failure mutated source")
	}
}
