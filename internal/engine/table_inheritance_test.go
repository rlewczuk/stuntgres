package engine

import (
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceUnitRelation(e *Engine, name string, id ids.OID, names ...string) *relationDescriptor {
	d := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name: name, revision: 1, index: make(map[string]int)}
	for i, name := range names {
		d.index[name] = i
		d.attributes = append(d.attributes, int32(i+1))
		d.columns = append(d.columns, ColumnDescriptor{Name: name, Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.attributeHighWater = int32(len(names))
	return d
}
func inheritanceUnitView(e *Engine, relations ...*relationDescriptor) CatalogRead {
	state := cloneCatalog(e.CatalogRead().state)
	for _, d := range relations {
		state.setDescriptor(d.address, d)
		state.bindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.namespace, Name: d.name}, d.address)
	}
	return e.catalog.read(state)
}
func TestInheritanceMergeStableRemap(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "p", 22000, "b", "a")
	parent.attributes = []int32{2, 7}
	child := inheritanceUnitRelation(e, "c", 22001, "a", "b", "local")
	ref := e.catalog.domain.Ref(e.CatalogRead().Epoch(), parent.address)
	ref.Address.SubID = 7
	node := columnExpression{Info: expressionInfo{Usage: testUsage(e, sqltype.Int4)}, origin: ref, scope: 1, frame: 1, slot: 1}
	mapper := createLikeInput{source: parent, targetSlots: []int{1, 0}}
	mapped, err := mapper.remap(node, child, e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	got := mapped.(columnExpression)
	if got.slot != 0 || got.origin.Address.ObjectID != child.address.ObjectID || got.origin.Address.SubID != 1 || node.slot != 1 || node.origin != ref {
		t.Fatal(got, node)
	}
	sequence := sequenceCallExpression{ref: ref}
	mapped, err = mapper.remap(sequence, child, e.CatalogRead())
	if err != nil || mapped.(sequenceCallExpression).ref != ref {
		t.Fatal(mapped, err)
	}
}
func TestInheritanceMergeDefinitions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	p := inheritanceUnitRelation(e, "p", 22000, "a", "x")
	q := inheritanceUnitRelation(e, "q", 22001, "x", "a")
	p.notNullConstraints = []notNullConstraint{{OID: 23000, Name: "first", Attribute: 1}}
	q.notNullConstraints = []notNullConstraint{{OID: 23001, Name: "second", Attribute: 2}}
	usage := testUsage(e, sqltype.Int4)
	value, err := sqltype.Integer(usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	literal := literalExpression{Info: expressionInfo{Usage: usage}, value: value}
	p.defaults = map[int32]columnDefault{1: {OID: 23002, expression: literal}}
	q.defaults = map[int32]columnDefault{2: {OID: 23003, expression: literal}}
	child := inheritanceUnitRelation(e, "c", 22002, "a", "x", "z")
	view := inheritanceUnitView(e, p, q)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	stage := e.BeginCatalogStage(view)
	b := binding{createInheritance: []createInheritanceInput{{source: p}, {source: q}}, createLocalColumns: map[string]bool{"z": true}, createDeclarations: make([]createColumnDeclaration, 3)}
	if err := e.stageInheritance(op, auth, stage, child, b); err != nil {
		t.Fatal(err)
	}
	if len(child.notNullConstraints) != 1 || child.notNullConstraints[0].Name != "first" || child.columns[0].Nullable || len(child.inheritance.notNull[1].origins) != 2 || child.inheritance.notNull[1].local || child.inheritance.columns[1].local || !child.inheritance.columns[3].local {
		t.Fatal(child)
	}
	if !reflect.DeepEqual(child.inheritance.parents, []ids.ObjectAddress{p.address, q.address}) || !sameGroupExpression(child.defaults[1].expression, literal) || child.defaults[1].OID != 0 || p.defaults[1].OID != 23002 {
		t.Fatal(child.defaults, p.defaults)
	}
	if err := e.assignDefinitionOIDsLocked(child); err != nil {
		t.Fatal(err)
	}
	if child.notNullConstraints[0].OID == p.notNullConstraints[0].OID || child.defaults[1].OID == p.defaults[1].OID {
		t.Fatal("inherited identity copied")
	}
	combined := inheritanceUnitView(e, p, q, child)
	if err := validateInheritance(combined, child); err != nil {
		t.Fatal(err)
	}
}
func TestInheritanceGraphValidation(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	c := inheritanceUnitRelation(e, "c", 22001, "a")
	c.inheritance = &tableInheritance{parents: []ids.ObjectAddress{p.address}, columns: map[int32]inheritedColumn{1: {origins: []inheritanceOrigin{{parent: p.address, attribute: 1}}}}}
	if err := validateInheritance(inheritanceUnitView(e, p, c), c); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"dangling", "duplicate", "attribute", "cycle", "locality", "type"} {
		t.Run(kind, func(t *testing.T) {
			parent := *p
			child := *c
			graph := *c.inheritance
			child.inheritance = &graph
			graph.parents = append([]ids.ObjectAddress(nil), graph.parents...)
			graph.columns = map[int32]inheritedColumn{1: c.inheritance.columns[1]}
			switch kind {
			case "dangling":
				graph.parents[0].ObjectID++
			case "duplicate":
				graph.parents = append(graph.parents, p.address)
			case "attribute":
				graph.columns[1] = inheritedColumn{origins: []inheritanceOrigin{{parent: p.address, attribute: 0}}}
			case "cycle":
				parent.inheritance = &tableInheritance{parents: []ids.ObjectAddress{c.address}}
			case "locality":
				graph.columns[1] = inheritedColumn{}
			case "type":
				parent.columns = append([]ColumnDescriptor(nil), p.columns...)
				parent.columns[0].Usage = testUsage(e, sqltype.Int8)
			}
			if err := validateInheritance(inheritanceUnitView(e, &parent, &child), &child); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
	if p.inheritance != nil || c.inheritance.parents[0] != p.address {
		t.Fatal("source graph mutated")
	}
}
func TestInheritanceGraphDiamondChildren(t *testing.T) {
	e := New()
	base := inheritanceUnitRelation(e, "base", 22000)
	left := inheritanceUnitRelation(e, "left", 22001)
	right := inheritanceUnitRelation(e, "right", 22002)
	leaf := inheritanceUnitRelation(e, "leaf", 22003)
	left.inheritance = &tableInheritance{parents: []ids.ObjectAddress{base.address}}
	right.inheritance = &tableInheritance{parents: []ids.ObjectAddress{base.address}}
	leaf.inheritance = &tableInheritance{parents: []ids.ObjectAddress{left.address, right.address, base.address}}
	v := inheritanceUnitView(e, base, left, right, leaf)
	got := inheritanceChildren(v, base.address)
	if len(got) != 3 || got[0] != left || got[1] != right || got[2] != leaf || len(inheritanceChildren(v, leaf.address)) != 0 {
		t.Fatal(got)
	}
}

func TestInheritanceMergeDefaultOverride(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	p := inheritanceUnitRelation(e, "p", 22000, "a")
	q := inheritanceUnitRelation(e, "q", 22001, "a")
	usage := testUsage(e, sqltype.Int4)
	first, err := sqltype.Integer(usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sqltype.Integer(usage.Type, 8)
	if err != nil {
		t.Fatal(err)
	}
	p.defaults = map[int32]columnDefault{1: {OID: 23000, expression: literalExpression{Info: expressionInfo{Usage: usage}, value: first}}}
	q.defaults = map[int32]columnDefault{1: {OID: 23001, expression: literalExpression{Info: expressionInfo{Usage: usage}, value: second}}}
	view := inheritanceUnitView(e, p, q)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	for _, kind := range []string{"absent", "explicit", "copied"} {
		override := kind == "explicit"
		child := inheritanceUnitRelation(e, "c", 22002, "a")
		b := binding{createInheritance: []createInheritanceInput{{source: p}, {source: q}}, createDeclarations: make([]createColumnDeclaration, 1), createLocalColumns: map[string]bool{"a": kind != "absent"}}
		if override {
			b.createDeclarations[0].defaultExpr = parser.LiteralExpr{}
		}
		b.createDeclarations[0].copiedDefault = kind == "copied"
		stage := e.BeginCatalogStage(view)
		err := e.stageInheritance(op, auth, stage, child, b)
		if !override {
			diagnostic, ok := err.(interface{ SQLState() string })
			if !ok || diagnostic.SQLState() != "42611" {
				t.Fatal(err)
			}
			continue
		}
		if err != nil || len(child.defaults) != 0 || !child.inheritance.columns[1].explicitDefault || !child.inheritance.columns[1].local {
			t.Fatal(child, err)
		}
	}
	if p.defaults[1].OID != 23000 || q.defaults[1].OID != 23001 {
		t.Fatal("source default identity changed")
	}
}

func TestInheritanceMergeShapeDeclarations(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a", "x")
	p.columns[1].Usage = testUsage(e, sqltype.Text)
	q := inheritanceUnitRelation(e, "q", 22001, "y", "a")
	q.columns[0].Usage = testUsage(e, sqltype.Bool)
	e.catalog.state = inheritanceUnitView(e, p, q).state
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	name := func(text string) parser.ObjectName {
		return parser.ObjectName{Parts: []parser.Identifier{{Text: text}}}
	}
	syntax := &parser.CreateTable{Name: name("c"), Parents: []parser.ObjectName{name("p"), name("q")}, Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "z", TypeName: parser.TypeName{Name: "bigint"}}},
		{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "integer"}}},
	}}
	e.mu.Lock()
	bound, err := e.describe(op.Context(), syntax, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, column := range bound.createColumns {
		names = append(names, column.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "x", "y", "z"}) || len(bound.createDeclarations) != 4 || !bound.createLocalColumns["a"] || bound.createLocalColumns["x"] || !bound.createLocalColumns["z"] || len(bound.createInheritance) != 2 {
		t.Fatal(names, bound.createLocalColumns, bound.createDeclarations)
	}
	if bound.createColumns[1].Usage.Type.ID() != sqltype.Text || bound.createColumns[2].Usage.Type.ID() != sqltype.Bool || bound.createColumns[3].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(bound.createColumns)
	}
	if p.columns[1].Name != "x" || q.columns[0].Name != "y" {
		t.Fatal("source physical shape changed")
	}
}

func TestInheritanceRetainedRowAuthority(t *testing.T) {
	e := New()
	d := inheritanceUnitRelation(e, "retained", 22000, "a")
	v := e.CatalogRead()
	oldRef := e.catalog.domain.Ref(v.Epoch(), d.address)
	oldRef.Address.SubID = 1
	old := columnExpression{Info: expressionInfo{Usage: testUsage(e, sqltype.Int4)}, origin: oldRef, scope: 1, frame: 1, slot: 0}
	state := cloneCatalog(v.state)
	state.epoch++
	current := e.catalog.read(state)
	mapper := createLikeInput{source: d}
	mapped, err := mapper.remap(old, d, current)
	if err != nil || !sameInheritedExpression(mapped, old, d, current) || old.origin != oldRef {
		t.Fatal("retained row authority changed equality or source", mapped, old, err)
	}
	other := old
	other.Info.Usage = testUsage(e, sqltype.Int8)
	if sameInheritedExpression(mapped, other, d, current) {
		t.Fatal("row-authority normalization hid a different expression type")
	}
	external := sequenceCallExpression{ref: oldRef}
	newExternal := external
	newExternal.ref.Epoch++
	if sameInheritedExpression(newExternal, external, d, current) {
		t.Fatal("row-authority normalization retargeted an external provider")
	}
}

func TestInheritanceGenerationDeclarationProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	parent := inheritanceUnitRelation(e, "p", 22000, "g")
	usage := testUsage(e, sqltype.Int4)
	value, err := sqltype.Integer(usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	expression := literalExpression{Info: expressionInfo{Usage: usage}, value: value}
	parent.generations = map[int32]columnGeneration{1: {kind: generationStored, expression: expression, planned: expression}}
	view := inheritanceUnitView(e, parent)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = view
	auth.recaptureCatalog(auth.catalog)
	for _, explicit := range []bool{false, true} {
		child := inheritanceUnitRelation(e, "c", 22001, "g")
		b := binding{createInheritance: []createInheritanceInput{{source: parent}}, createLocalColumns: map[string]bool{"g": true}, createDeclarations: make([]createColumnDeclaration, 1)}
		if explicit {
			b.createDeclarations[0].generation = &parser.GenerationDefinition{Kind: "stored"}
		}
		if err := e.stageInheritance(op, auth, e.BeginCatalogStage(view), child, b); err != nil {
			t.Fatal(err)
		}
		provenance := child.inheritance.columns[1]
		if !provenance.local || provenance.explicitGeneration != explicit {
			t.Fatal("generation declaration provenance", provenance, explicit)
		}
		if !explicit && !sameGroupExpression(child.generations[1].expression, expression) {
			t.Fatal("plain local declaration lost inherited generation")
		}
	}
}
