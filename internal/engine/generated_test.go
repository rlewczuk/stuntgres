package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
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
	eval := exec.Context{}
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
	wrong := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 2, Frame: 1, Row: row}}
	if _, err := node.Eval(&wrong); err == nil {
		t.Fatal("wrong frame accepted")
	}
}

func TestGeneratedOriginalReferencesAndPrivateReadManifest(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored}}
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	columns := []analyze.QueryColumn{{Descriptor: d.Columns[0], Slot: 0, Leaf: 0, Physical: 0}, {Descriptor: d.Columns[1], Slot: 1, Leaf: 0, Physical: 1}}
	ns := analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{{Name: "g", Original: "g", Schema: "public", Ref: ref, Columns: columns}}, Frame: d.Columns}
	auth := AuthorizationView{View: access.View{Catalog: view}}
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.Target.GeneratedOwner = d
	b.Query.Auth = &auth
	b.Query.Namespace = &ns
	b.Query.Sources = []exec.Leaf{{Ref: ref, Relation: d}}
	raw := func(name string) parser.ColumnExpr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	if _, err := b.Column(raw("a")); err != nil {
		t.Fatal(err)
	}
	if len(b.Target.Uses) != 0 {
		t.Fatal("generation added DML explicit read manifest", b.Target.Uses)
	}
	for _, tc := range []struct{ name, code string }{{"b", "42P17"}, {"g", "42P17"}, {"xmin", "42P10"}, {"missing", "42703"}} {
		_, err := b.Column(raw(tc.name))
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
	if plan.GenerationImmutable(volatile) {
		t.Fatal("volatile accepted")
	}
	pruned := plan.CaseExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, Otherwise: leaf}
	if !plan.GenerationImmutable(pruned) || !plan.GenerationHasTableOID(pruned) {
		t.Fatal("surviving provider walk uses inherited volatility")
	}
	remapped, err := RemapExpression(pruned, func(old ids.ObjectRef) ids.ObjectRef { old.Address.ObjectID = 26000; return old }, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := remapped.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}})
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
	mapper := analyze.LikeInput{Source: parent}
	left, err := RemapLikeExpression(&mapper, original, &child, view)
	if err != nil {
		t.Fatal(err)
	}
	right, err := RemapLikeExpression(&mapper, original, &child, view)
	if err != nil {
		t.Fatal(err)
	}
	if !analyze.SameGroupExpression(left, right) || analyze.SameGroupExpression(original, left) || original.Owner != owner {
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
		if analyze.SameGroupExpression(mapped, different) || analyze.SameGroupExpression(different, mapped) {
			t.Fatal("distinct physical row authority or full Usage compared equal")
		}
	}
	column := plan.ColumnExpr{Info: mapped.Info, Origin: mapped.Owner}
	if analyze.SameGroupExpression(mapped, column) {
		t.Fatal("different expression kind compared equal")
	}
}

func TestGeneratedNullDefinitionValidationAndIdentity(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	op := &session.Operation{Ctx: context.Background()}
	declarations := []analyze.ColumnDeclaration{{}, {Generation: &parser.GenerationDefinition{Kind: "stored", Expression: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}}
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
	if definition.OID == 0 || !plan.StructuralNull(definition.Expression) || len(d.ColumnMeta.Defaults) != 0 {
		t.Fatal("generated NULL pruned/conflated", definition)
	}
	candidate := catalog.CloneState(view.State())
	candidate.SetDescriptor(d.Address(), d)
	candidate.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	read := e.catalog.read(candidate)
	if err := (relationProvider{}).Validate(read, d); err != nil {
		t.Fatal(err)
	}
	if !catalog.DefinitionOIDUsed(d, definition.OID) {
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
	if err := recomputeRemappedGenerations(context.Background(), candidate, relations, mapping, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, nil); err != nil {
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
	if err := recomputeRemappedGenerations(canceled, candidate, relations, mapping, exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	d.Constraints.Check = []catalog.CheckConstraint{{Name: "blocked", Expression: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}}
	if err := recomputeRemappedGenerations(context.Background(), candidate, relations, mapping, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, nil); err == nil {
		t.Fatal("replacement ignored completed-row CHECK")
	}
	if n, _ := source[1].OID(); n != uint32(oldAddress.ObjectID) {
		t.Fatal("constraint failure mutated source")
	}
}
