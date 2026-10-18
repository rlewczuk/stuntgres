package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGCursorsSource(t *testing.T) {
	s := cursorUnitSetup(t)
	e := s.Engine()
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgCursorsOID})
	ref := e.catalog.domain.Ref(e.catalog.state.epoch, d.address)
	empty, err := capturePGCursors(context.Background(), ref, d, s.cursors)
	if err != nil || len(empty.anonymous) != 0 || empty.ref != ref {
		t.Fatal(empty, err)
	}
	sessionSQL(t, s, "DECLARE own SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	cursor := s.cursors.entries["own"]
	original := cursor.root.expressions[0]
	calls := 0
	cursor.root.expressions[0] = queryProbe(original.ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("metadata opened cursor producer")
	})
	source, err := capturePGCursors(context.Background(), ref, d, s.cursors)
	if err != nil || len(source.anonymous) != 1 || calls != 0 || cursor.position != cursorBefore || cursor.stage != nil {
		t.Fatal("metadata demanded producer", source, err, calls)
	}
	row := source.anonymous[0]
	for i, value := range row {
		if value.Type() != d.Columns[i].Usage.Type || value.IsNull() {
			t.Fatal("foreign/null metadata fact", i, value)
		}
	}
	name, _ := row[0].Text()
	statement, _ := row[1].Text()
	hold, _ := row[2].Boolean()
	binary, _ := row[3].Boolean()
	scroll, _ := row[4].Boolean()
	instant, err := row[5].TimestampTime()
	if name != "own" || statement != cursor.statement || !hold || binary || !scroll || err != nil || instant.IsZero() {
		t.Fatal("wrong cursor fact", row, err)
	}
	// A new catalog's consumer must receive its own timestamp descriptor,
	// while the registry and earlier detached facts retain their original one.
	current := New()
	currentDescriptor := current.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgCursorsOID})
	currentRef := current.catalog.domain.Ref(current.catalog.state.epoch, currentDescriptor.address)
	stored, epoch := cursor.created, cursor.epoch
	reowned, err := capturePGCursors(context.Background(), currentRef, currentDescriptor, s.cursors)
	if err != nil {
		t.Fatal(err)
	}
	projected := reowned.anonymous[0][5]
	projectedInstant, err := projected.TimestampTime()
	if err != nil || !projectedInstant.Equal(instant) || projected.Type() != currentDescriptor.Columns[5].Usage.Type || projected.Type() == stored.Type() || cursor.created != stored || cursor.epoch != epoch || row[5] != stored || calls != 0 {
		t.Fatal("timestamp projection changes datum, registry, prior result or producer", projected, err)
	}
	// A bad scalar identity is an error, never a fallback or partial source.
	invalid := *currentDescriptor
	invalid.Columns = append([]ColumnDescriptor(nil), currentDescriptor.Columns...)
	invalid.Columns[5].Usage.Type = currentDescriptor.Columns[0].Usage.Type
	if got, err := capturePGCursors(context.Background(), currentRef, &invalid, s.cursors); got != nil || err == nil {
		t.Fatal("incompatible timestamp ownership published facts", got, err)
	}
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "DECLARE peer NO SCROLL CURSOR WITH HOLD FOR SELECT 99")
	peerSource, err := capturePGCursors(context.Background(), ref, d, peer.cursors)
	if err != nil || len(peerSource.anonymous) != 1 {
		t.Fatal(peerSource, err)
	}
	peerName, _ := peerSource.anonymous[0][0].Text()
	if peerName != "peer" {
		t.Fatal("peer reads another registry", peerName)
	}

	// Restore the real expression, then explicitly fail the cursor producer.
	// The recovered failed entry is still observable without rerunning it.
	cursor.root.expressions[0] = original
	sessionSQL(t, s, "SAVEPOINT recover")
	cursor.root.expressions[0] = queryProbe(original.ResultUsage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("failed producer")
	})
	if _, err := cursorUnitExecute(t, s, context.Background(), "FETCH 1 FROM own"); err == nil {
		t.Fatal("producer unexpectedly succeeded")
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	failed, err := capturePGCursors(context.Background(), ref, d, s.cursors)
	if err != nil || !reflect.DeepEqual(failed.anonymous, source.anonymous) || cursor.position != cursorFailed || calls != 1 {
		t.Fatal("failed cursor metadata changed/demanded", failed, err, calls)
	}

	// With two entries, cancel at preflight, either entry, and the final
	// publication boundary. No canceled observation publishes a prefix.
	sessionSQL(t, s, "DECLARE second NO SCROLL CURSOR FOR SELECT 2")
	for _, at := range []int32{1, 2, 3, 4} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		got, err := capturePGCursors(probe, ref, d, s.cursors)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial metadata publication", at, got, err)
		}
	}
	snapshot := append([]sqltype.Value(nil), source.anonymous[0]...)
	if err := disposeSQLCursors(context.Background(), s.cursors.clear()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.anonymous[0], snapshot) || len(s.cursors.entries) != 0 {
		t.Fatal("registry clear mutates captured facts")
	}
	cleared, err := capturePGCursors(context.Background(), ref, d, s.cursors)
	if err != nil || len(cleared.anonymous) != 0 {
		t.Fatal(cleared, err)
	}
}

func TestPGCursorsCatalog(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgCursorsOID})
	if err := (relationProvider{}).Validate(catalog, d); err != nil {
		t.Fatal(err)
	}
	if !d.VirtualView || d.RelationKind != catalogRelation || d.namespace.ObjectID != ids.PGCatalogNamespaceOID {
		t.Fatal("invalid view identity", d)
	}
	wantNames := []string{"name", "statement", "is_holdable", "is_binary", "is_scrollable", "creation_time"}
	wantTypes := []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Timestamptz}
	if len(d.Columns) != 6 {
		t.Fatal("invalid view arity", d.Columns)
	}
	for i, column := range d.Columns {
		owner, ok := catalog.LookupType(wantTypes[i])
		if !ok || column.Name != wantNames[i] || column.Usage.Type != owner || column.Usage.Typmod != -1 || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgCursorsOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("invalid logical consumer descriptor", i, column)
		}
	}
	if len(d.ACL.Entries) != 1 || !d.ACL.Entries[0].Grantee.Public || d.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("catalog view lacks public SELECT", d.ACL)
	}
	// These native builtin row/array IDs are independent identities, not
	// PostgreSQL-generated relation OIDs. Both types must belong to this view
	// and the array must resolve back to this exact catalog-owned row descriptor.
	if d.RowType != sqltype.ID(12079) {
		t.Fatal("wrong cursor row type", d.RowType)
	}
	rowDescriptor, ok := catalog.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12079})
	if !ok {
		t.Fatal("missing cursor row type")
	}
	row, ok := rowDescriptor.(*typeDescriptor)
	if !ok || row.owningRelation != d.address || row.array != sqltype.ID(12078) {
		t.Fatal("cursor row ownership/array association", rowDescriptor)
	}
	arrayType, ok := catalog.LookupArray(row.typ)
	if !ok || arrayType.ID() != sqltype.ID(12078) || arrayType.Element() != row.typ {
		t.Fatal("cursor array does not resolve reciprocally", arrayType)
	}
	arrayDescriptor, ok := catalog.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12078})
	if !ok {
		t.Fatal("missing cursor associated array")
	}
	array, ok := arrayDescriptor.(*typeDescriptor)
	if !ok || array.typ != arrayType || array.owningRelation != d.address {
		t.Fatal("cursor array owner differs from view", arrayDescriptor)
	}
	for _, descriptor := range []Descriptor{row, array} {
		if err := (typeProvider{}).Validate(catalog, descriptor); err != nil {
			t.Fatal(err)
		}
	}
	invalidRow := *row
	invalidRow.owningRelation = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}
	if err := (typeProvider{}).Validate(catalog, &invalidRow); err == nil {
		t.Fatal("cursor row type accepted another backing relation")
	}
	// Validator must reject a virtual table or a type descriptor from another
	// engine, not just accept a superficially matching OID/schema.
	invalid := *d
	invalid.RelationKind = tableRelation
	if err := (relationProvider{}).Validate(catalog, &invalid); err == nil {
		t.Fatal("virtual table accepted")
	}
	invalid = *d
	invalid.Columns = append([]ColumnDescriptor(nil), d.Columns...)
	foreign, _ := New().CatalogRead().LookupType(sqltype.Timestamptz)
	invalid.Columns[5].Usage.Type = foreign
	if err := (relationProvider{}).Validate(catalog, &invalid); err == nil {
		t.Fatal("foreign timestamp owner accepted")
	}
}
