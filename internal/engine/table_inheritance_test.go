package engine

import (
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func inheritanceUnitRelation(e *Engine, name string, id ids.OID, names ...string) *catalog.Relation {
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Index: make(map[string]int)}
	for i, name := range names {
		d.Index[name] = i
		d.Attributes = append(d.Attributes, int32(i+1))
		d.Columns = append(d.Columns, catalog.Column{Name: name, Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true, OriginTable: uint32(id), OriginAttribute: int16(i + 1)})
	}
	d.ColumnMeta.HighWater = int32(len(names))
	return d
}
func inheritanceUnitView(e *Engine, relations ...*catalog.Relation) catalog.Read {
	state := catalog.CloneState(e.CatalogRead().State())
	for _, d := range relations {
		state.SetDescriptor(d.Address(), d)
		state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	}
	return e.catalog.read(state)
}
func TestInheritanceMergeStableRemap(t *testing.T) {
	e := New()
	parent := inheritanceUnitRelation(e, "p", 22000, "b", "a")
	parent.Attributes = []int32{2, 7}
	child := inheritanceUnitRelation(e, "c", 22001, "a", "b", "local")
	ref := e.catalog.Domain.Ref(e.CatalogRead().Epoch(), parent.Address())
	ref.Address.SubID = 7
	node := plan.ColumnExpr{Info: expr.Info{Usage: testUsage(e, sqltype.Int4)}, Origin: ref, Scope: 1, Frame: 1, Slot: 1}
	mapper := createLikeInput{source: parent, targetSlots: []int{1, 0}}
	mapped, err := mapper.remap(node, child, e.CatalogRead())
	if err != nil {
		t.Fatal(err)
	}
	got := mapped.(plan.ColumnExpr)
	if got.Slot != 0 || got.Origin.Address.ObjectID != child.Address().ObjectID || got.Origin.Address.SubID != 1 || node.Slot != 1 || node.Origin != ref {
		t.Fatal(got, node)
	}
	sequence := functions.SequenceCall{Ref: ref}
	mapped, err = mapper.remap(sequence, child, e.CatalogRead())
	if err != nil || mapped.(functions.SequenceCall).Ref != ref {
		t.Fatal(mapped, err)
	}
}
func TestInheritanceMergeDefinitions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	p := inheritanceUnitRelation(e, "p", 22000, "a", "x")
	q := inheritanceUnitRelation(e, "q", 22001, "x", "a")
	p.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23000, Name: "first", Attribute: 1}}
	q.Constraints.NotNull = []catalog.NotNullConstraint{{OID: 23001, Name: "second", Attribute: 2}}
	usage := testUsage(e, sqltype.Int4)
	value, err := sqltype.Integer(usage.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}
	p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 23002, Expression: literal}}
	q.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{2: {OID: 23003, Expression: literal}}
	child := inheritanceUnitRelation(e, "c", 22002, "a", "x", "z")
	view := inheritanceUnitView(e, p, q)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	stage := e.BeginCatalogStage(view)
	b := binding{create: createTableBinding{inheritance: []createInheritanceInput{{source: p}, {source: q}}, localColumns: map[string]bool{"z": true}, declarations: make([]createColumnDeclaration, 3)}}
	if err := e.stageInheritance(op, auth, stage, child, b); err != nil {
		t.Fatal(err)
	}
	if len(child.Constraints.NotNull) != 1 || child.Constraints.NotNull[0].Name != "first" || child.Columns[0].Nullable || len(child.Inheritance.NotNull[1].Origins) != 2 || child.Inheritance.NotNull[1].Local || child.Inheritance.Columns[1].Local || !child.Inheritance.Columns[3].Local {
		t.Fatal(child)
	}
	if !reflect.DeepEqual(child.Inheritance.Parents, []ids.ObjectAddress{p.Address(), q.Address()}) || !sameGroupExpression(child.ColumnMeta.Defaults[1].Expression, literal) || child.ColumnMeta.Defaults[1].OID != 0 || p.ColumnMeta.Defaults[1].OID != 23002 {
		t.Fatal(child.ColumnMeta.Defaults, p.ColumnMeta.Defaults)
	}
	if err := e.assignDefinitionOIDsLocked(child); err != nil {
		t.Fatal(err)
	}
	if child.Constraints.NotNull[0].OID == p.Constraints.NotNull[0].OID || child.ColumnMeta.Defaults[1].OID == p.ColumnMeta.Defaults[1].OID {
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
	c.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{p.Address()}, Columns: map[int32]catalog.InheritedColumn{1: {Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 1}}}}}
	if err := validateInheritance(inheritanceUnitView(e, p, c), c); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"dangling", "duplicate", "attribute", "cycle", "locality", "type"} {
		t.Run(kind, func(t *testing.T) {
			parent := *p
			child := *c
			graph := *c.Inheritance
			child.Inheritance = &graph
			graph.Parents = append([]ids.ObjectAddress(nil), graph.Parents...)
			graph.Columns = map[int32]catalog.InheritedColumn{1: c.Inheritance.Columns[1]}
			switch kind {
			case "dangling":
				graph.Parents[0].ObjectID++
			case "duplicate":
				graph.Parents = append(graph.Parents, p.Address())
			case "attribute":
				graph.Columns[1] = catalog.InheritedColumn{Origins: []catalog.InheritanceOrigin{{Parent: p.Address(), Attribute: 0}}}
			case "cycle":
				parent.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{c.Address()}}
			case "locality":
				graph.Columns[1] = catalog.InheritedColumn{}
			case "type":
				parent.Columns = append([]catalog.Column(nil), p.Columns...)
				parent.Columns[0].Usage = testUsage(e, sqltype.Int8)
			}
			if err := validateInheritance(inheritanceUnitView(e, &parent, &child), &child); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
	if p.Inheritance != nil || c.Inheritance.Parents[0] != p.Address() {
		t.Fatal("source graph mutated")
	}
}
func TestInheritanceGraphDiamondChildren(t *testing.T) {
	e := New()
	base := inheritanceUnitRelation(e, "base", 22000)
	left := inheritanceUnitRelation(e, "left", 22001)
	right := inheritanceUnitRelation(e, "right", 22002)
	leaf := inheritanceUnitRelation(e, "leaf", 22003)
	left.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{base.Address()}}
	right.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{base.Address()}}
	leaf.Inheritance = &catalog.TableInheritance{Parents: []ids.ObjectAddress{left.Address(), right.Address(), base.Address()}}
	v := inheritanceUnitView(e, base, left, right, leaf)
	got := inheritanceChildren(v, base.Address())
	if len(got) != 3 || got[0] != left || got[1] != right || got[2] != leaf || len(inheritanceChildren(v, leaf.Address())) != 0 {
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
	p.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 23000, Expression: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: first}}}
	q.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {OID: 23001, Expression: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: second}}}
	view := inheritanceUnitView(e, p, q)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	for _, kind := range []string{"absent", "explicit", "copied"} {
		override := kind == "explicit"
		child := inheritanceUnitRelation(e, "c", 22002, "a")
		b := binding{create: createTableBinding{inheritance: []createInheritanceInput{{source: p}, {source: q}}, declarations: make([]createColumnDeclaration, 1), localColumns: map[string]bool{"a": kind != "absent"}}}
		if override {
			b.create.declarations[0].defaultExpr = parser.LiteralExpr{}
		}
		b.create.declarations[0].copiedDefault = kind == "copied"
		stage := e.BeginCatalogStage(view)
		err := e.stageInheritance(op, auth, stage, child, b)
		if !override {
			diagnostic, ok := err.(interface{ SQLState() string })
			if !ok || diagnostic.SQLState() != "42611" {
				t.Fatal(err)
			}
			continue
		}
		if err != nil || len(child.ColumnMeta.Defaults) != 0 || !child.Inheritance.Columns[1].ExplicitDefault || !child.Inheritance.Columns[1].Local {
			t.Fatal(child, err)
		}
	}
	if p.ColumnMeta.Defaults[1].OID != 23000 || q.ColumnMeta.Defaults[1].OID != 23001 {
		t.Fatal("source default identity changed")
	}
}

func TestInheritanceMergeShapeDeclarations(t *testing.T) {
	e := New()
	p := inheritanceUnitRelation(e, "p", 22000, "a", "x")
	p.Columns[1].Usage = testUsage(e, sqltype.Text)
	q := inheritanceUnitRelation(e, "q", 22001, "y", "a")
	q.Columns[0].Usage = testUsage(e, sqltype.Bool)
	e.catalog.state = inheritanceUnitView(e, p, q).State()
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
	for _, column := range bound.create.columns {
		names = append(names, column.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "x", "y", "z"}) || len(bound.create.declarations) != 4 || !bound.create.localColumns["a"] || bound.create.localColumns["x"] || !bound.create.localColumns["z"] || len(bound.create.inheritance) != 2 {
		t.Fatal(names, bound.create.localColumns, bound.create.declarations)
	}
	if bound.create.columns[1].Usage.Type.ID() != sqltype.Text || bound.create.columns[2].Usage.Type.ID() != sqltype.Bool || bound.create.columns[3].Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(bound.create.columns)
	}
	if p.Columns[1].Name != "x" || q.Columns[0].Name != "y" {
		t.Fatal("source physical shape changed")
	}
}

func TestInheritanceRetainedRowAuthority(t *testing.T) {
	e := New()
	d := inheritanceUnitRelation(e, "retained", 22000, "a")
	v := e.CatalogRead()
	oldRef := e.catalog.Domain.Ref(v.Epoch(), d.Address())
	oldRef.Address.SubID = 1
	old := plan.ColumnExpr{Info: expr.Info{Usage: testUsage(e, sqltype.Int4)}, Origin: oldRef, Scope: 1, Frame: 1, Slot: 0}
	state := catalog.CloneState(v.State())
	state.SetEpoch(state.Epoch() + 1)
	current := e.catalog.read(state)
	mapper := createLikeInput{source: d}
	mapped, err := mapper.remap(old, d, current)
	if err != nil || !sameInheritedExpression(mapped, old, d, current) || old.Origin != oldRef {
		t.Fatal("retained row authority changed equality or source", mapped, old, err)
	}
	other := old
	other.Info.Usage = testUsage(e, sqltype.Int8)
	if sameInheritedExpression(mapped, other, d, current) {
		t.Fatal("row-authority normalization hid a different expression type")
	}
	external := functions.SequenceCall{Ref: oldRef}
	newExternal := external
	newExternal.Ref.Epoch++
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
	expression := plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}
	parent.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{1: {Kind: catalog.GenerationStored, Expression: expression, Planned: expression}}
	view := inheritanceUnitView(e, parent)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	for _, explicit := range []bool{false, true} {
		child := inheritanceUnitRelation(e, "c", 22001, "g")
		b := binding{create: createTableBinding{inheritance: []createInheritanceInput{{source: parent}}, localColumns: map[string]bool{"g": true}, declarations: make([]createColumnDeclaration, 1)}}
		if explicit {
			b.create.declarations[0].generation = &parser.GenerationDefinition{Kind: "stored"}
		}
		if err := e.stageInheritance(op, auth, e.BeginCatalogStage(view), child, b); err != nil {
			t.Fatal(err)
		}
		provenance := child.Inheritance.Columns[1]
		if !provenance.Local || provenance.ExplicitGeneration != explicit {
			t.Fatal("generation declaration provenance", provenance, explicit)
		}
		if !explicit && !sameGroupExpression(child.ColumnMeta.Generations[1].Expression, expression) {
			t.Fatal("plain local declaration lost inherited generation")
		}
	}
}
