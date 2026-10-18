package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
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
	state := *base.state
	state.descriptors = maps.Clone(base.state.descriptors)
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
	catalog := e.CatalogRead()
	address := pgDependUnitAddress(1259, 2608, 0)
	source, ok := catalog.Relation(address)
	if !ok {
		t.Fatal("missing pg_depend descriptor")
	}
	if source.name != "pg_depend" || source.namespace.ObjectID != 11 || source.relationKind != catalogRelation || source.attributeHighWater != 7 || len(source.columns) != 7 {
		t.Fatal(source)
	}
	names := []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}
	types := []sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Char}
	for i, column := range source.columns {
		if column.Name != names[i] || column.Usage.Type.ID() != types[i] || column.Nullable || !column.NullabilityKnown || column.OriginTable != 2608 || column.OriginAttribute != int16(i+1) || source.attributes[i] != int32(i+1) {
			t.Fatal("descriptor", i, column)
		}
	}
	if _, ok := source.index["oid"]; ok {
		t.Fatal("fabricated oid")
	}
	if err := (relationProvider{}).Validate(catalog, source); err != nil {
		t.Fatal(err)
	}
	ref := e.catalog.domain.Ref(catalog.state.epoch, address)
	rows, err := capturePGDepend(context.Background(), catalog, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.anonymous) != 0 {
		t.Fatal("synthetic bootstrap graph", rows.anonymous)
	}
}
func TestPGDependConstructionBags(t *testing.T) {
	e, catalog, integer := pgDependUnitCatalog(t)
	tableAddress := pgDependUnitAddress(1259, 41001, 0)
	column := tableAddress
	column.SubID = 2
	ref := e.catalog.domain.Ref(catalog.state.epoch, column)
	variable := columnExpression{Info: expressionInfo{Usage: integer.Usage()}, origin: ref}
	repeated := functions.Binary{Info: expressionInfo{Usage: integer.Usage()}, Args: []BoundExpr{variable, variable}}
	constant := literalExpression{Info: expressionInfo{Usage: integer.Usage()}}
	uq := uniqueConstraint{OID: 42003, Index: pgDependUnitAddress(1259, 44003, 0), Attributes: []int32{5}}
	table := &relationDescriptor{address: tableAddress, namespace: pgDependUnitAddress(2615, 2200, 0), relationKind: tableRelation, attributes: []int32{2, 3, 4, 5}, attributeHighWater: 5, columns: []ColumnDescriptor{{Usage: integer.Usage()}, {Usage: integer.Usage()}, {Usage: integer.Usage()}, {Usage: integer.Usage()}}, notNullConstraints: []notNullConstraint{{OID: 42001, Attribute: 3}}, checkConstraints: []checkConstraint{{OID: 42002, expression: repeated}, {OID: 42004, expression: constant}}, uniqueConstraints: []uniqueConstraint{uq}, defaults: map[int32]columnDefault{3: {OID: 43001, expression: constant}}, generations: map[int32]columnGeneration{4: {kind: generationStored, OID: 43002, expression: repeated}}}
	catalog.state.setDescriptor(tableAddress, table)
	simple := &relationDescriptor{address: pgDependUnitAddress(1259, 44001, 0), relationKind: indexRelation, sqlIndex: &indexDefinition{parent: tableAddress, keys: []indexKey{{attribute: 2}, {attribute: 2}}, include: []int32{3}}}
	expression := &relationDescriptor{address: pgDependUnitAddress(1259, 44002, 0), relationKind: indexRelation, sqlIndex: &indexDefinition{parent: tableAddress, keys: []indexKey{{derived: &derivedIndexKey{definition: repeated}}}}}
	backing := &relationDescriptor{address: pgDependUnitAddress(1259, 44003, 0), relationKind: indexRelation, sqlIndex: &indexDefinition{parent: tableAddress, unique: true, keys: []indexKey{{attribute: 5}}, include: []int32{3}, constraint: uniqueBacking(&uq)}}
	standalone := &relationDescriptor{address: pgDependUnitAddress(1259, 44004, 0), relationKind: indexRelation, sqlIndex: &indexDefinition{parent: tableAddress, unique: true, keys: []indexKey{{attribute: 2}}, include: []int32{3}}}
	sequence := &relationDescriptor{address: pgDependUnitAddress(1259, 45001, 0), namespace: pgDependUnitAddress(2615, 2200, 0), relationKind: sequenceRelation, sequence: &sequenceOptions{owned: column}}
	identity := &relationDescriptor{address: pgDependUnitAddress(1259, 45002, 0), namespace: pgDependUnitAddress(2615, 2200, 0), relationKind: sequenceRelation, sequence: &sequenceOptions{owned: column, identity: true}}
	for _, d := range []*relationDescriptor{simple, expression, backing, standalone, sequence, identity} {
		catalog.state.setDescriptor(d.address, d)
	}
	got, err := collectPGDepend(context.Background(), catalog)
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
	source := catalog.MustRelation(pgDependUnitAddress(1259, 2608, 0))
	captured, err := capturePGDepend(context.Background(), catalog, ids.ObjectRef{}, source)
	if err != nil {
		t.Fatal(err)
	}
	catalog.state.removeDescriptor(tableAddress)
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
	_, catalog, integer := pgDependUnitCatalog(t)
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
		d := &typeDescriptor{typ: typ, namespace: ns, revision: 1}
		catalog.state.setDescriptor(d.Address(), d)
	}
	if !pgDependPinned(catalog, pgDependUnitAddress(1247, 23, 0)) || !pgDependPinned(catalog, pgDependUnitAddress(2615, 11, 0)) || pgDependPinned(catalog, pgDependUnitAddress(2615, 2200, 0)) || pgDependPinned(catalog, pgDependUnitAddress(1247, 47003, 0)) {
		t.Fatal("pin authorities")
	}
	got, err := collectPGDepend(context.Background(), catalog)
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
