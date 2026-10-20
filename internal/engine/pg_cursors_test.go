package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGCursorsSource(t *testing.T) {
	s := cursorUnitSetup(t)
	e := engineOf(s)
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.CursorsOID})
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), d.Address())
	empty, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
	if err != nil || len(empty.Anonymous) != 0 || empty.Ref != ref {
		t.Fatal(empty, err)
	}
	sessionSQL(t, s, "DECLARE own SCROLL CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
	cursor := s.cursors.entries["own"]
	original := cursor.root.Expressions[0]
	calls := 0
	cursor.root.Expressions[0] = queryProbe(original.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("metadata opened cursor producer")
	})
	source, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
	if err != nil || len(source.Anonymous) != 1 || calls != 0 || cursor.position != cursorBefore || cursor.stage != nil {
		t.Fatal("metadata demanded producer", source, err, calls)
	}
	row := source.Anonymous[0]
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
	currentDescriptor := current.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.CursorsOID})
	currentRef := current.catalog.Domain.Ref(current.catalog.state.Epoch(), currentDescriptor.Address())
	stored, epoch := cursor.created, cursor.epoch
	reowned, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: currentRef, Source: currentDescriptor, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
	if err != nil {
		t.Fatal(err)
	}
	projected := reowned.Anonymous[0][5]
	projectedInstant, err := projected.TimestampTime()
	if err != nil || !projectedInstant.Equal(instant) || projected.Type() != currentDescriptor.Columns[5].Usage.Type || projected.Type() == stored.Type() || cursor.created != stored || cursor.epoch != epoch || row[5] != stored || calls != 0 {
		t.Fatal("timestamp projection changes datum, registry, prior result or producer", projected, err)
	}
	// A bad scalar identity is an error, never a fallback or partial source.
	invalid := *currentDescriptor
	invalid.Columns = append([]catalog.Column(nil), currentDescriptor.Columns...)
	invalid.Columns[5].Usage.Type = currentDescriptor.Columns[0].Usage.Type
	if got, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: currentRef, Source: &invalid, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors); got != nil || err == nil {
		t.Fatal("incompatible timestamp ownership published facts", got, err)
	}
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "DECLARE peer NO SCROLL CURSOR WITH HOLD FOR SELECT 99")
	peerSource, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: peer.cursors}}, capturePGCursors)
	if err != nil || len(peerSource.Anonymous) != 1 {
		t.Fatal(peerSource, err)
	}
	peerName, _ := peerSource.Anonymous[0][0].Text()
	if peerName != "peer" {
		t.Fatal("peer reads another registry", peerName)
	}

	// Restore the real expression, then explicitly fail the cursor producer.
	// The recovered failed entry is still observable without rerunning it.
	cursor.root.Expressions[0] = original
	sessionSQL(t, s, "SAVEPOINT recover")
	cursor.root.Expressions[0] = queryProbe(original.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("failed producer")
	})
	if _, err := cursorUnitExecute(t, s, context.Background(), "FETCH 1 FROM own"); err == nil {
		t.Fatal("producer unexpectedly succeeded")
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	failed, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
	if err != nil || !reflect.DeepEqual(failed.Anonymous, source.Anonymous) || cursor.position != cursorFailed || calls != 1 {
		t.Fatal("failed cursor metadata changed/demanded", failed, err, calls)
	}

	// With two entries, cancel at preflight, either entry, and the final
	// publication boundary. No canceled observation publishes a prefix.
	sessionSQL(t, s, "DECLARE second NO SCROLL CURSOR FOR SELECT 2")
	for _, at := range []int32{1, 2, 3, 4} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		got, err := captureCatalogSourceRows(pgcatalog.Input{Context: probe, Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial metadata publication", at, got, err)
		}
	}
	snapshot := append([]sqltype.Value(nil), source.Anonymous[0]...)
	if err := disposeSQLCursors(context.Background(), s.cursors.clear()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.Anonymous[0], snapshot) || len(s.cursors.entries) != 0 {
		t.Fatal("registry clear mutates captured facts")
	}
	cleared, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Ref: ref, Source: d, Runtime: engineCatalogRuntime{cursors: s.cursors}}, capturePGCursors)
	if err != nil || len(cleared.Anonymous) != 0 {
		t.Fatal(cleared, err)
	}
}

func TestPGCursorsCatalog(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	d := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.CursorsOID})
	if err := (relationProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if !d.VirtualView || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != ids.PGCatalogNamespaceOID {
		t.Fatal("invalid view identity", d)
	}
	wantNames := []string{"name", "statement", "is_holdable", "is_binary", "is_scrollable", "creation_time"}
	wantTypes := []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Timestamptz}
	if len(d.Columns) != 6 {
		t.Fatal("invalid view arity", d.Columns)
	}
	for i, column := range d.Columns {
		owner, ok := view.LookupType(wantTypes[i])
		if !ok || column.Name != wantNames[i] || column.Usage.Type != owner || column.Usage.Typmod != -1 || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.CursorsOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("invalid logical consumer descriptor", i, column)
		}
	}
	if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("catalog view lacks public SELECT", d.Security.ACL)
	}
	// These native builtin row/array IDs are independent identities, not
	// PostgreSQL-generated relation OIDs. Both types must belong to this view
	// and the array must resolve back to this exact catalog-owned row descriptor.
	if d.RowType != sqltype.ID(12079) {
		t.Fatal("wrong cursor row type", d.RowType)
	}
	rowDescriptor, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12079})
	if !ok {
		t.Fatal("missing cursor row type")
	}
	row, ok := rowDescriptor.(*catalog.Type)
	if !ok || row.OwningRelation != d.Address() || row.Array != sqltype.ID(12078) {
		t.Fatal("cursor row ownership/array association", rowDescriptor)
	}
	arrayType, ok := view.LookupArray(row.SQLType)
	if !ok || arrayType.ID() != sqltype.ID(12078) || arrayType.Element() != row.SQLType {
		t.Fatal("cursor array does not resolve reciprocally", arrayType)
	}
	arrayDescriptor, ok := view.Descriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12078})
	if !ok {
		t.Fatal("missing cursor associated array")
	}
	array, ok := arrayDescriptor.(*catalog.Type)
	if !ok || array.SQLType != arrayType || array.OwningRelation != d.Address() {
		t.Fatal("cursor array owner differs from view", arrayDescriptor)
	}
	for _, descriptor := range []catalog.Descriptor{row, array} {
		if err := (typeProvider{}).Validate(view, descriptor); err != nil {
			t.Fatal(err)
		}
	}
	invalidRow := *row
	invalidRow.OwningRelation = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}
	if err := (typeProvider{}).Validate(view, &invalidRow); err == nil {
		t.Fatal("cursor row type accepted another backing relation")
	}
	// Validator must reject a virtual table or a type descriptor from another
	// engine, not just accept a superficially matching OID/schema.
	invalid := *d
	invalid.RelationKind = catalog.TableRelation
	if err := (relationProvider{}).Validate(view, &invalid); err == nil {
		t.Fatal("virtual table accepted")
	}
	invalid = *d
	invalid.Columns = append([]catalog.Column(nil), d.Columns...)
	foreign, _ := New().CatalogRead().LookupType(sqltype.Timestamptz)
	invalid.Columns[5].Usage.Type = foreign
	if err := (relationProvider{}).Validate(view, &invalid); err == nil {
		t.Fatal("foreign timestamp owner accepted")
	}
}
