package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgDependUnitCatalog(t *testing.T) (*Engine, CatalogRead, *sqltype.Descriptor) {
	t.Helper()
	e := New()
	base := e.CatalogRead()
	typ, ok := base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	state := *base.State()
	state.CloneDescriptors()
	return e, e.catalog.read(&state), typ
}
func pgDependUnitAddress(class, id ids.OID, sub int32) ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: class, ObjectID: id, SubID: sub}
}
func pgDependUnitEdge(c, id ids.OID, sub int32, rc, rid ids.OID, rs int32, kind byte) pgDependEdge {
	return pgDependEdge{pgDependUnitAddress(c, id, sub), pgDependUnitAddress(rc, rid, rs), kind}
}
func pgDependUnitBag(t *testing.T, got, want []pgDependEdge) {
	t.Helper()
	counts := func(rows []pgDependEdge) map[pgDependEdge]int {
		out := map[pgDependEdge]int{}
		for _, row := range rows {
			out[row]++
		}
		return out
	}
	if !reflect.DeepEqual(counts(got), counts(want)) {
		t.Fatalf("edge bag got %#v want %#v", counts(got), counts(want))
	}
}
func TestPGDependShape(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	address := pgDependUnitAddress(1259, 2608, 0)
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing pg_depend descriptor")
	}
	if source.Name() != "pg_depend" || source.Namespace().ObjectID != 11 || source.RelationKind != catalog.CatalogRelation || source.AttributeHighWater != 7 || len(source.Columns) != 7 {
		t.Fatal(source)
	}
	names := []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Char}
	for i, column := range source.Columns {
		if column.Name != names[i] || column.Usage.Type.ID() != types[i] || column.Nullable || !column.NullabilityKnown || column.OriginTable != 2608 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			t.Fatal("descriptor", i, column)
		}
	}
	if _, ok := source.Index["oid"]; ok {
		t.Fatal("fabricated oid")
	}
	if err := (relationProvider{}).Validate(view, source); err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.Domain.Ref(view.State().Epoch(), address)
	rows, err := capturePGDepend(context.Background(), view, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.anonymous) != 0 {
		t.Fatal("synthetic bootstrap graph", rows.anonymous)
	}
}
func TestPGDependConstructionBags(t *testing.T) {
	e, view, integer := pgDependUnitCatalog(t)
	tableAddress := pgDependUnitAddress(1259, 41001, 0)
	column := tableAddress
	column.SubID = 2
	ref := e.catalog.Domain.Ref(view.State().Epoch(), column)
	variable := columnExpression{Info: expr.Info{Usage: integer.Usage()}, origin: ref}
	repeated := functions.Binary{Info: expr.Info{Usage: integer.Usage()}, Args: []expr.Expr{variable, variable}}
	constant := literalExpression{Info: expr.Info{Usage: integer.Usage()}}
	uq := catalog.UniqueConstraint{OID: 42003, Index: pgDependUnitAddress(1259, 44003, 0), Attributes: []int32{5}}
	table := &catalog.Relation{ObjectAddress: tableAddress, ObjectNamespace: pgDependUnitAddress(2615, 2200, 0), RelationKind: catalog.TableRelation, Attributes: []int32{2, 3, 4, 5}, AttributeHighWater: 5, Columns: []ColumnDescriptor{{Usage: integer.Usage()}, {Usage: integer.Usage()}, {Usage: integer.Usage()}, {Usage: integer.Usage()}}, NotNullConstraints: []catalog.NotNullConstraint{{OID: 42001, Attribute: 3}}, CheckConstraints: []catalog.CheckConstraint{{OID: 42002, Expression: repeated}, {OID: 42004, Expression: constant}}, UniqueConstraints: []catalog.UniqueConstraint{uq}, Defaults: map[int32]catalog.ColumnDefault{3: {OID: 43001, Expression: constant}}, Generations: map[int32]catalog.ColumnGeneration{4: {Kind: catalog.GenerationStored, OID: 43002, Expression: repeated}}}
	view.State().SetDescriptor(tableAddress, table)
	simple := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 44001, 0), RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: tableAddress, Keys: []catalog.IndexKey{{Attribute: 2}, {Attribute: 2}}, Include: []int32{3}}}
	expression := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 44002, 0), RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: tableAddress, Keys: []catalog.IndexKey{{Derived: &catalog.DerivedIndexKey{Definition: repeated}}}}}
	backing := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 44003, 0), RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: tableAddress, Unique: true, Keys: []catalog.IndexKey{{Attribute: 5}}, Include: []int32{3}, Constraint: uniqueBacking(&uq)}}
	standalone := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 44004, 0), RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Parent: tableAddress, Unique: true, Keys: []catalog.IndexKey{{Attribute: 2}}, Include: []int32{3}}}
	sequence := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 45001, 0), ObjectNamespace: pgDependUnitAddress(2615, 2200, 0), RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{Owned: column}}
	identity := &catalog.Relation{ObjectAddress: pgDependUnitAddress(1259, 45002, 0), ObjectNamespace: pgDependUnitAddress(2615, 2200, 0), RelationKind: catalog.SequenceRelation, Sequence: &catalog.SequenceOptions{Owned: column, Identity: true}}
	for _, d := range []*catalog.Relation{simple, expression, backing, standalone, sequence, identity} {
		view.State().SetDescriptor(d.Address(), d)
	}
	got, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	want := []pgDependEdge{
		pgDependUnitEdge(1259, 41001, 0, 2615, 2200, 0, 'n'),
		pgDependUnitEdge(2606, 42001, 0, 1259, 41001, 3, 'a'),
		pgDependUnitEdge(2606, 42002, 0, 1259, 41001, 2, 'a'), pgDependUnitEdge(2606, 42002, 0, 1259, 41001, 2, 'n'),
		pgDependUnitEdge(2606, 42004, 0, 1259, 41001, 0, 'a'),
		pgDependUnitEdge(2606, 42003, 0, 1259, 41001, 5, 'a'),
		pgDependUnitEdge(2606, 42003, 0, 1259, 41001, 3, 'a'),
		pgDependUnitEdge(2604, 43001, 0, 1259, 41001, 3, 'a'),
		pgDependUnitEdge(2604, 43002, 0, 1259, 41001, 4, 'i'), pgDependUnitEdge(2604, 43002, 0, 1259, 41001, 2, 'n'),
		pgDependUnitEdge(1259, 44001, 0, 1259, 41001, 2, 'a'), pgDependUnitEdge(1259, 44001, 0, 1259, 41001, 3, 'a'),
		pgDependUnitEdge(1259, 44002, 0, 1259, 41001, 0, 'a'), pgDependUnitEdge(1259, 44002, 0, 1259, 41001, 2, 'a'),
		pgDependUnitEdge(1259, 44003, 0, 2606, 42003, 0, 'i'),
		pgDependUnitEdge(1259, 44004, 0, 1259, 41001, 2, 'a'), pgDependUnitEdge(1259, 44004, 0, 1259, 41001, 3, 'a'),
		pgDependUnitEdge(1259, 45001, 0, 2615, 2200, 0, 'n'), pgDependUnitEdge(1259, 45001, 0, 1259, 41001, 2, 'a'),
		pgDependUnitEdge(1259, 45002, 0, 2615, 2200, 0, 'n'), pgDependUnitEdge(1259, 45002, 0, 1259, 41001, 2, 'i'),
	}
	pgDependUnitBag(t, got, want)
	source := view.MustRelation(pgDependUnitAddress(1259, 2608, 0))
	captured, err := capturePGDepend(context.Background(), view, ids.ObjectRef{}, source)
	if err != nil {
		t.Fatal(err)
	}
	view.State().RemoveDescriptor(tableAddress)
	// Completed rows are owned values, not a later live-catalog enumeration.
	decoded := make([]pgDependEdge, 0, len(captured.anonymous))
	for _, row := range captured.anonymous {
		if len(row) != 7 {
			t.Fatal("row width", row)
		}
		class, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		id, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		sub, err := row[2].Integer()
		if err != nil {
			t.Fatal(err)
		}
		refclass, err := row[3].OID()
		if err != nil {
			t.Fatal(err)
		}
		refid, err := row[4].OID()
		if err != nil {
			t.Fatal(err)
		}
		refsub, err := row[5].Integer()
		if err != nil {
			t.Fatal(err)
		}
		kind, err := row[6].Text()
		if err != nil || len(kind) != 1 {
			t.Fatal(kind, err)
		}
		decoded = append(decoded, pgDependUnitEdge(ids.OID(class), ids.OID(id), int32(sub), ids.OID(refclass), ids.OID(refid), int32(refsub), kind[0]))
	}
	pgDependUnitBag(t, decoded, want)
}
func TestPGDependDomainTopologyAndPinning(t *testing.T) {
	_, view, integer := pgDependUnitCatalog(t)
	ns := pgDependUnitAddress(2615, 47000, 0)
	d1, err := sqltype.Domain(47001, "d1", "s.d1", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	d2, err := sqltype.Domain(47002, "d2", "s.d2", d1.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(47003, "_d1", d1)
	if err != nil {
		t.Fatal(err)
	}
	enum, err := sqltype.Enum(47004, "e", "s.e", []string{"x", "y"})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{d1, d2, array, enum} {
		d := &typeDescriptor{SQLType: typ, ObjectNamespace: ns, ObjectRevision: 1}
		view.State().SetDescriptor(d.Address(), d)
	}
	if !pgDependPinned(view, pgDependUnitAddress(1247, 23, 0)) || !pgDependPinned(view, pgDependUnitAddress(2615, 11, 0)) || pgDependPinned(view, pgDependUnitAddress(2615, 2200, 0)) || pgDependPinned(view, pgDependUnitAddress(1247, 47003, 0)) {
		t.Fatal("pin authorities")
	}
	got, err := collectPGDepend(context.Background(), view)
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitBag(t, got, []pgDependEdge{pgDependUnitEdge(1247, 47001, 0, 2615, 47000, 0, 'n'), pgDependUnitEdge(1247, 47002, 0, 2615, 47000, 0, 'n'), pgDependUnitEdge(1247, 47002, 0, 1247, 47001, 0, 'n'), pgDependUnitEdge(1247, 47003, 0, 1247, 47001, 0, 'i'), pgDependUnitEdge(1247, 47004, 0, 2615, 47000, 0, 'n')})
}
func TestPGDependCanceledCapture(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	source := catalog.MustRelation(pgDependUnitAddress(1259, 2608, 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if edges, err := collectPGDepend(ctx, catalog); !errors.Is(err, context.Canceled) || edges != nil {
		t.Fatal(edges, err)
	}
	if rows, err := capturePGDepend(ctx, catalog, ids.ObjectRef{}, source); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal(rows, err)
	}
}

// Deterministic cancellation inside a walk, without timing or huge SQL fixtures.
type pgDependUnitWalkContext struct {
	context.Context
	remaining int
}

func (c *pgDependUnitWalkContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestPGDependCancellationDuringWalk(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	source := catalog.MustRelation(pgDependUnitAddress(1259, 2608, 0))
	ctx := &pgDependUnitWalkContext{Context: context.Background(), remaining: 4}
	if edges, err := collectPGDepend(ctx, catalog); !errors.Is(err, context.Canceled) || edges != nil {
		t.Fatal("partial collection published", edges, err)
	}
	ctx = &pgDependUnitWalkContext{Context: context.Background(), remaining: 4}
	if rows, err := capturePGDepend(ctx, catalog, ids.ObjectRef{}, source); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("partial source published", rows, err)
	}
}
