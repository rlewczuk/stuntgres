package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func generatedUnitDescriptor(t *testing.T) (*Engine, CatalogRead, *relationDescriptor) {
	t.Helper()
	e := New()
	view := e.CatalogRead()
	integer, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	var namespace ObjectAddress
	for address, descriptor := range view.state.descriptors {
		if descriptor.Kind() == NamespaceObject && descriptor.Name() == "public" {
			namespace = address
			break
		}
	}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 25000}, namespace: namespace, name: "g", revision: 1, columns: []ColumnDescriptor{{Name: "a", Usage: integer.Usage(), Nullable: true}, {Name: "b", Usage: integer.Usage(), Nullable: true}}, attributes: []int32{1, 2}, attributeHighWater: 2, index: map[string]int{"a": 0, "b": 1}}
	return e, view, d
}

func TestGeneratedTypedRowAndCancellation(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: d.address}
	ref.Address.SubID = 1
	node := columnExpression{expressionInfo: expressionInfo{usage: d.columns[0].Usage, properties: ExprProperties{RowDependent: true}}, origin: ref, scope: 1, frame: 1, slot: 0}
	d.generations = map[int32]columnGeneration{2: {expression: node, planned: node}}
	value, err := sqltype.Integer(d.columns[0].Usage.Type, 2)
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
	wrong := EvalContext{Context: context.Background(), Scope: 2, Frame: 1, Row: row}
	if _, err := node.Eval(&wrong); err == nil {
		t.Fatal("wrong frame accepted")
	}
}

func TestGeneratedOriginalReferencesAndPrivateReadManifest(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	d.generations = map[int32]columnGeneration{2: {}}
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: d.address}
	columns := []queryColumn{{descriptor: d.columns[0], slot: 0, leaf: 0, physical: 0}, {descriptor: d.columns[1], slot: 1, leaf: 0, physical: 1}}
	ns := queryNamespace{columns: columns, qualifiers: []queryQualifier{{name: "g", original: "g", schema: "public", ref: ref, columns: columns}}, frame: d.columns}
	auth := AuthorizationView{catalog: view}
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.generatedOwner = d
	b.queryAuth = &auth
	b.queryNamespace = &ns
	b.querySources = []queryLeaf{{ref: ref, relation: d}}
	raw := func(name string) parser.ColumnExpr {
		return parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}}
	}
	if _, err := b.column(raw("a")); err != nil {
		t.Fatal(err)
	}
	if len(b.uses) != 0 {
		t.Fatal("generation added DML explicit read manifest", b.uses)
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
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: d.address}
	leaf := tableOIDExpression{expressionInfo{usage: oid.Usage(), properties: ExprProperties{RowDependent: true}}, ref}
	volatile := randomExpression{expressionInfo: expressionInfo{usage: oid.Usage(), properties: ExprProperties{Volatility: Volatile}}, name: "random"}
	if generationImmutable(volatile) {
		t.Fatal("volatile accepted")
	}
	pruned := caseExpression{expressionInfo: expressionInfo{usage: oid.Usage(), properties: ExprProperties{Volatility: Volatile, RowDependent: true}}, otherwise: leaf}
	if !generationImmutable(pruned) || !generationHasTableOID(pruned) {
		t.Fatal("surviving provider walk uses inherited volatility")
	}
	remapped, err := remapBoundExpression(pruned, func(old ObjectRef) ObjectRef { old.Address.ObjectID = 26000; return old }, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := remapped.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.OID(); err != nil || n != 26000 {
		t.Fatal(n, err)
	}
	if leaf.owner.Address.ObjectID != 25000 {
		t.Fatal("source mutated")
	}
}

func TestGeneratedNullDefinitionValidationAndIdentity(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	op := &Operation{ctx: context.Background()}
	declarations := []createColumnDeclaration{{}, {generation: &parser.GenerationDefinition{Expression: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}}
	if err := e.bindCreateGenerations(op, AuthorizationView{catalog: view}, d, declarations, sqltype.InputEnvironment{}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err := e.assignDefinitionOIDsLocked(d)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := d.generations[2]
	if definition.OID == 0 || !structuralNull(definition.expression) || len(d.defaults) != 0 {
		t.Fatal("generated NULL pruned/conflated", definition)
	}
	candidate := cloneCatalog(view.state)
	candidate.descriptors[d.address] = d
	candidate.names[NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: d.name}] = d.address
	read := CatalogRead{owner: e, state: candidate}
	if err := (relationProvider{}).Validate(read, d); err != nil {
		t.Fatal(err)
	}
	if !definitionOIDUsed(d, definition.OID) {
		t.Fatal("definition identity not reserved")
	}
	for _, mutate := range []func(*columnGeneration){
		func(g *columnGeneration) { g.OID = 0 },
		func(g *columnGeneration) { g.dependencies = nil },
		func(g *columnGeneration) { g.planned = nil },
	} {
		bad := *d
		replacement := definition
		mutate(&replacement)
		bad.generations = map[int32]columnGeneration{2: replacement}
		if err := (relationProvider{}).Validate(read, &bad); err == nil {
			t.Fatal("invalid generated definition accepted")
		}
	}
}

func TestGeneratedReplacementPrivateRowsAndCancellation(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	oid, _ := view.LookupType(sqltype.Oid)
	oldAddress := d.address
	d.address.ObjectID = 26000
	d.columns[1].Usage = oid.Usage()
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch() + 1, Address: d.address}
	leaf := tableOIDExpression{expressionInfo{usage: oid.Usage(), properties: ExprProperties{RowDependent: true}}, ref}
	d.generations = map[int32]columnGeneration{2: {expression: leaf, planned: leaf}}
	base, _ := sqltype.Integer(d.columns[0].Usage.Type, 2)
	oldOID, _ := sqltype.OIDValue(oid, uint32(oldAddress.ObjectID))
	source := []sqltype.Value{base, oldOID}
	rows := &relationStorage{versions: []rowVersion{{values: source}}}
	candidate := cloneCatalog(view.state)
	candidate.descriptors[d.address] = d
	storage := map[ObjectAddress]*relationStorage{d.address: rows}
	mapping := map[ObjectAddress]ObjectAddress{oldAddress: d.address}
	if err := recomputeRemappedGenerations(context.Background(), candidate, storage, mapping, EvalContext{Context: context.Background()}); err != nil {
		t.Fatal(err)
	}
	if n, _ := source[1].OID(); n != uint32(oldAddress.ObjectID) {
		t.Fatal("source row mutated", n)
	}
	if n, _ := rows.versions[0].values[1].OID(); n != uint32(d.address.ObjectID) {
		t.Fatal("owner not recomputed", n)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recomputeRemappedGenerations(canceled, candidate, storage, mapping, EvalContext{Context: canceled}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	d.checkConstraints = []checkConstraint{{Name: "blocked", expression: literalExpression{expressionInfo{usage: boolean.Usage()}, sqltype.Boolean(boolean, false)}}}
	if err := recomputeRemappedGenerations(context.Background(), candidate, storage, mapping, EvalContext{Context: context.Background()}); err == nil {
		t.Fatal("replacement ignored completed-row CHECK")
	}
	if n, _ := source[1].OID(); n != uint32(oldAddress.ObjectID) {
		t.Fatal("constraint failure mutated source")
	}
}
