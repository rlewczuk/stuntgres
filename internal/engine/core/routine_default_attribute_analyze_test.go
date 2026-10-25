package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These are genuine provider descriptors and the engine's real binding host;
// no SQL session or callback stands in for descriptor authority.
func routineAttributeAnalyzerBinder(t *testing.T, e *core.Engine, reader catalog.DefinitionReader) *analyze.Binder {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	op.Definition = reader
	e.Mu.Lock()
	t.Cleanup(func() { e.Mu.Unlock(); done() })
	return &analyze.Binder{Host: e.AnalysisHost(op), Ctx: context.Background()}
}

func routineAttributeAnalyzerBase(typ *sqltype.Descriptor) plan.LiteralExpr {
	return plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}
}

func routineAttributeAnalyzerCapture(t *testing.T, b *analyze.Binder, read catalog.Read, relation *catalog.Relation, attribute int32, revision ids.DefinitionRevision) {
	t.Helper()
	address := relation.Address()
	address.SubID = attribute
	want := catalog.ExpressionDependency{Ref: read.Owner().Domain.Ref(read.Epoch(), address), Revision: revision}
	var actual []catalog.ExpressionDependency
	for _, dependency := range b.Dependencies {
		if dependency.Ref.Address.ClassID == ids.RelationClassID {
			actual = append(actual, dependency)
		}
	}
	if !slices.Equal(actual, []catalog.ExpressionDependency{want}) {
		t.Fatalf("exact analyzer attribute authority got=%#v want=%#v", actual, want)
	}
}

func TestRoutineDefaultAttributeAnalyzerCapture(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	read := e.CatalogRead()
	typ, _ := read.LookupType(source.RowType)
	candidate := catalog.NewCandidate(read)
	owner := *source
	owner.ObjectRevision = 7
	if err := candidate.Replace(&owner); err != nil {
		t.Fatal(err)
	}
	b := routineAttributeAnalyzerBinder(t, e, candidate.Definitions())
	base := routineAttributeAnalyzerBase(typ)
	for range 2 {
		field, err := b.SelectCompositeField(base, "note", parser.Position{})
		if err != nil {
			t.Fatal(err)
		}
		node, ok := field.(plan.FieldExpr)
		if !ok || node.Attribute != 4 || node.Base.ResultUsage() != base.ResultUsage() || node.ResultUsage() != source.Columns[1].Usage {
			t.Fatalf("wrong bound field: %#v", field)
		}
		routineAttributeAnalyzerCapture(t, b, read, &owner, 4, 7)
	}
	before := slices.Clone(b.Dependencies)
	key := analyze.PreparedTypeKey{Kind: analyze.CompositeSelection}
	captured := analyze.TypeOccurrence{Kind: analyze.CompositeSelection, Base: typ.Usage(), Attributes: []int32{4}}
	field, err := b.RetainedCompositeField(base, key, captured, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := field.(plan.FieldExpr); !ok || node.Attribute != 4 {
		t.Fatalf("retained field changed identity: %#v", field)
	}
	if !slices.Equal(before, b.Dependencies) {
		t.Fatal("repeated/retained selection duplicated or refreshed captures", b.Dependencies)
	}
	if read.Get(source.Address()) != source || source.Revision() == 7 {
		t.Fatal("staged capture changed committed owner")
	}
}

func TestRoutineDefaultAttributeAnalyzerAuthority(t *testing.T) {
	for _, kind := range []string{"missing-type", "foreign-type", "missing-owner", "wrong-backing-owner", "wrong-rowtype", "missing-member", "wrong-name", "wrong-usage"} {
		t.Run(kind, func(t *testing.T) {
			e, source, other := typedUnitCatalog(t, false)
			read := e.CatalogRead()
			typ := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
			candidate := catalog.NewCandidate(read)
			relation, provider := *source, *typ
			relation.Columns = slices.Clone(source.Columns)
			switch kind {
			case "missing-type":
				candidate.RecordRemoval(read.Owner().Domain.Ref(read.Epoch(), typ.Address()), typ)
			case "foreign-type":
				foreign, err := sqltype.Composite(typ.SQLType.ID(), typ.SQLType.Name(), typ.SQLType.SQLName(), typ.SQLType.AssociatedArray(), []sqltype.CompositeMember{typ.SQLType.CompositeMember(0), typ.SQLType.CompositeMember(1)})
				if err != nil {
					t.Fatal(err)
				}
				provider.SQLType = foreign
				if err := candidate.Replace(&provider); err != nil {
					t.Fatal(err)
				}
			case "missing-owner":
				candidate.RecordRemoval(read.Owner().Domain.Ref(read.Epoch(), source.Address()), source)
			case "wrong-backing-owner":
				provider.OwningRelation = other.Address()
				if err := candidate.Replace(&provider); err != nil {
					t.Fatal(err)
				}
			case "wrong-rowtype":
				relation.RowType = other.RowType
			case "missing-member":
				relation.Attributes, relation.Columns = []int32{2}, relation.Columns[:1]
			case "wrong-name":
				relation.Columns[1].Name = "substituted"
			case "wrong-usage":
				relation.Columns[1].Usage = source.Columns[0].Usage
			}
			if kind == "wrong-rowtype" || kind == "missing-member" || kind == "wrong-name" || kind == "wrong-usage" {
				if err := candidate.Replace(&relation); err != nil {
					t.Fatal(err)
				}
			}
			b := routineAttributeAnalyzerBinder(t, e, candidate.Definitions())
			field, err := b.SelectCompositeField(routineAttributeAnalyzerBase(typ.SQLType), "note", parser.Position{})
			if err == nil || field != nil {
				t.Fatalf("invalid staged %s provider admitted: %#v / %v", kind, field, err)
			}
			for _, dependency := range b.Dependencies {
				if dependency.Ref.Address.ClassID == ids.RelationClassID {
					t.Fatal("invalid provider acquired attribute authority", dependency)
				}
			}
			if read.Get(source.Address()) != source || read.Get(typ.Address()) != typ {
				t.Fatal("invalid staging changed committed authority")
			}
		})
	}
}

func TestRoutineDefaultAttributeAnalyzerStaging(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	read := e.CatalogRead()
	shape, err := sqltype.Composite(900002, "staged_attribute", "staged_attribute", 0, []sqltype.CompositeMember{{Attribute: 4, Name: "note", Usage: source.Columns[1].Usage}})
	if err != nil {
		t.Fatal(err)
	}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900001}, ObjectNamespace: source.Namespace(), ObjectName: "staged_attribute", ObjectRevision: 13, RelationKind: catalog.CompositeRelation, RowType: shape.ID(), Columns: []catalog.Column{source.Columns[1]}, Attributes: []int32{4}, Owner: source.Owner}
	typ := &catalog.Type{SQLType: shape, ObjectNamespace: source.Namespace(), ObjectRevision: 5, OwningRelation: relation.Address(), Owner: source.Owner}
	candidate := catalog.NewCandidate(read)
	if err := candidate.Create(relation); err != nil {
		t.Fatal(err)
	}
	if err := candidate.Create(typ); err != nil {
		t.Fatal(err)
	}
	b := routineAttributeAnalyzerBinder(t, e, candidate.Definitions())
	base := routineAttributeAnalyzerBase(shape)
	field, err := b.SelectCompositeField(base, "note", parser.Position{})
	if err != nil {
		t.Fatal("stage-only provider", err)
	}
	if node, ok := field.(plan.FieldExpr); !ok || node.Attribute != 4 {
		t.Fatalf("stage-only field: %#v", field)
	}
	routineAttributeAnalyzerCapture(t, b, read, relation, 4, 13)
	if _, ok := read.Find(relation.Address()); ok {
		t.Fatal("staged owner leaked into base")
	}
	if _, ok := read.Find(typ.Address()); ok {
		t.Fatal("staged type leaked into base")
	}
	baseBinder := &analyze.Binder{Host: e.AnalysisHost(nil), Ctx: context.Background()}
	if field, err := baseBinder.SelectCompositeField(base, "note", parser.Position{}); err == nil || field != nil {
		t.Fatal("base discovered unpublished provider", field, err)
	}
	before := slices.Clone(b.Dependencies)
	candidate.RecordRemoval(read.Owner().Domain.Ref(read.Epoch(), relation.Address()), relation)
	if field, err := b.SelectCompositeField(base, "note", parser.Position{}); err == nil || field != nil {
		t.Fatal("removed staged owner resurrected", field, err)
	}
	if !slices.Equal(before, b.Dependencies) {
		t.Fatal("rejection changed original staged capture")
	}
}

func TestRoutineDefaultAttributeAnalyzerAnonymousRetained(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	read := e.CatalogRead()
	b := routineAttributeAnalyzerBinder(t, e, nil)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 4, Name: "note", Usage: source.Columns[1].Usage}})
	if err != nil {
		t.Fatal(err)
	}
	field, err := b.SelectCompositeField(routineAttributeAnalyzerBase(shape), "note", parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := field.(plan.FieldExpr); !ok || node.Attribute != 4 {
		t.Fatalf("anonymous field: %#v", field)
	}
	typ, _ := read.LookupType(source.RowType)
	retired, err := sqltype.Composite(typ.ID(), typ.Name(), typ.SQLName(), typ.AssociatedArray(), []sqltype.CompositeMember{typ.CompositeMember(0)})
	if err != nil {
		t.Fatal(err)
	}
	captured := analyze.TypeOccurrence{Kind: analyze.CompositeSelection, Base: typ.Usage(), Attributes: []int32{4}}
	before := slices.Clone(b.Dependencies)
	field, err = b.RetainedCompositeField(routineAttributeAnalyzerBase(retired), analyze.PreparedTypeKey{Kind: analyze.CompositeSelection}, captured, parser.Position{})
	if err != nil {
		t.Fatal(err)
	}
	literal, ok := field.(plan.LiteralExpr)
	if !ok || !literal.Value.IsNull() || literal.ResultUsage() != source.Columns[1].Usage {
		t.Fatalf("retired retained field lost typed NULL: %#v", field)
	}
	if !slices.Equal(before, b.Dependencies) {
		t.Fatal("retired field resurrected authority")
	}
	for _, dependency := range b.Dependencies {
		if dependency.Ref.Address.ClassID == ids.RelationClassID {
			t.Fatal("anonymous/retired field invented a backing relation edge", dependency)
		}
	}
}

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []catalog.Column
	if !empty {
		columns = []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: enginetest.Usage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.Mu.Lock()
	source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "pair", auth.State.CurrentUser, append([]catalog.Column(nil), columns...), index)
	if err == nil {
		source.RelationKind = catalog.CompositeRelation
		if !empty {
			source.Attributes = []int32{2, 4}
			source.ColumnMeta.HighWater = 4
			source.Columns[0].OriginAttribute = 2
			source.Columns[1].OriginAttribute = 4
		}
		err = e.StageRowTypeLocked(stage, source)
	}
	var tbl *catalog.Relation
	if err == nil {
		tbl, err = table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "typed", source.Owner, append([]catalog.Column(nil), columns...), index)
	}
	if err == nil {
		tbl.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
		if !empty {
			tbl.Attributes = []int32{1, 7}
			tbl.ColumnMeta.HighWater = 7
			tbl.Columns[1].OriginAttribute = 7
		}
		err = e.StageRowTypeLocked(stage, tbl)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(tbl); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.MustRelation(source.Address()), view.MustRelation(tbl.Address())
}
