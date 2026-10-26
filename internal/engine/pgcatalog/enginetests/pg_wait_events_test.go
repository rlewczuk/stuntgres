package enginetests_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func waitEventsUnitInput(t *testing.T, e *core.Engine) pgcatalog.Input {
	t.Helper()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12351}
	source, ok := view.Relation(address)
	if !ok {
		t.Fatal("missing pg_wait_events")
	}
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), address), Source: source}
}

func TestPGWaitEventsShapeAndOwnership(t *testing.T) {
	e := engine.New()
	in := waitEventsUnitInput(t, e)
	source, view := in.Source, in.Catalog
	if pgcatalog.WaitEventsOID != 12351 || source.Namespace().ObjectID != 11 || source.RowType != 12353 || !source.VirtualView || source.History.HasIndexes || pgcatalog.CatalogHasIndexes(12351) || pgcatalog.ClassKind(view, source) != "v" || source.ColumnMeta.HighWater != 3 || len(source.Columns) != 3 {
		t.Fatal("wait-event view shape", source)
	}
	text, ok := view.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing TEXT")
	}
	for i, name := range []string{"type", "name", "description"} {
		column := source.Columns[i]
		if column.Name != name || column.Usage.Type != text || column.Usage.Typmod != -1 || column.Usage.Collation != text.Usage().Collation || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 12351 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) || source.Index[name] != i {
			t.Fatal("owned nullable TEXT descriptor", i, column)
		}
	}
	if _, exists := source.Index["oid"]; exists {
		t.Fatal("invented oid column")
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12353})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12352})
	if row.OwningRelation != source.Address() || array.OwningRelation != source.Address() || row.Array != 12352 {
		t.Fatal("row/array ownership", row, array)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	if pgcatalog.SummaryViewDefinition(12351) == "" || pgcatalog.BuiltinViewEvents(12351, nil) != 0 || pgcatalog.BuiltinViewEvents(12351, []int32{1, 2, 3}) != 0 {
		t.Fatal("builtin view definition/write classification")
	}
}

func waitEventsUnitExpected() [][3]string {
	return [][3]string{
		{"IPC", "SafeSnapshot", "Waiting to obtain a valid snapshot for a READ ONLY DEFERRABLE transaction"},
		{"Lock", "advisory", "Waiting to acquire an advisory user lock"},
		{"Lock", "object", "Waiting to acquire a lock on a non-relation database object"},
		{"Lock", "relation", "Waiting to acquire a lock on a relation"},
		{"Lock", "transactionid", "Waiting for a transaction to finish"},
		{"Lock", "virtualxid", "Waiting to acquire a virtual transaction ID lock"},
		{"Timeout", "PgSleep", "Waiting due to a call to pg_sleep or a sibling function"},
	}
}

func waitEventsUnitRows(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 7 {
		t.Fatal("complete inventory", len(rows))
	}
	want := waitEventsUnitExpected()
	text, _ := in.Catalog.LookupType(sqltype.Text)
	seen := make(map[[2]string]bool)
	for i, row := range rows {
		if len(row) != 3 || cap(row) != 3 {
			t.Fatal("bounded width-three row", len(row), cap(row))
		}
		var got [3]string
		for j, value := range row {
			if value.Type() != text || value.IsNull() {
				t.Fatal("owned non-NULL TEXT", i, j, value)
			}
			var err error
			got[j], err = text.Output(t.Context(), value)
			if err != nil {
				t.Fatal(err)
			}
		}
		if got != want[i] || seen[[2]string{got[0], got[1]}] {
			t.Fatal("exact unique triple", got, want[i])
		}
		seen[[2]string{got[0], got[1]}] = true
	}
}

func TestPGWaitEventsProducer(t *testing.T) {
	e := engine.New()
	in := waitEventsUnitInput(t, e)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.WaitEventsOID)
	if !ok || produce == nil {
		t.Fatal("missing registered producer")
	}
	if _, ok := pgcatalog.ProducerFor(pgcatalog.WaitEventsOID + 1); ok {
		t.Fatal("producer registered for array type")
	}
	// Static definitions require no Runtime and accept an omitted role frontier.
	first, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil || first == nil || first.Ref != in.Ref {
		t.Fatal("capture without runtime", first, err)
	}
	waitEventsUnitRows(t, in, first.Anonymous)
	second, err := produce(in)
	if err != nil {
		t.Fatal(err)
	}
	waitEventsUnitRows(t, in, second)
	if &first.Anonymous[0][0] == &second[0][0] {
		t.Fatal("captures share mutable row backing")
	}
	// Mutating only this unpublished test-owned result distinguishes detached
	// backing from equal strings. The earlier capture must remain unchanged.
	second[0][0] = sqltype.String(in.Source.Columns[0].Usage.Type, "changed")
	waitEventsUnitRows(t, in, first.Anonymous)
	paired := in
	paired.Roles = e.RoleStore.PublishedRead(in.Owner, in.Catalog)
	pairedRows, err := produce(paired)
	if err != nil {
		t.Fatal(err)
	}
	waitEventsUnitRows(t, in, pairedRows)

	foreign := engine.New()
	foreignIn := waitEventsUnitInput(t, foreign)
	for _, tc := range []struct {
		name string
		edit func(*pgcatalog.Input)
	}{
		{"owner", func(x *pgcatalog.Input) { x.Owner = foreignIn.Owner }},
		{"missing_owner", func(x *pgcatalog.Input) { x.Owner = nil }},
		{"catalog", func(x *pgcatalog.Input) { x.Catalog = foreignIn.Catalog }},
		{"source", func(x *pgcatalog.Input) { x.Source = foreignIn.Source }},
		{"missing_source", func(x *pgcatalog.Input) { x.Source = nil }},
		{"ref_domain", func(x *pgcatalog.Input) { x.Ref = foreignIn.Ref }},
		{"ref_epoch", func(x *pgcatalog.Input) { x.Ref.Epoch++ }},
		{"ref_address", func(x *pgcatalog.Input) { x.Ref.Address.ObjectID++ }},
		{"roles", func(x *pgcatalog.Input) { x.Roles = foreign.RoleStore.PublishedRead(foreignIn.Owner, foreignIn.Catalog) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrong := in
			tc.edit(&wrong)
			waitEventsUnitReject(t, wrong, produce)
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*catalog.Relation)
	}{
		{"width", func(r *catalog.Relation) { r.Columns = r.Columns[:2] }},
		{"name", func(r *catalog.Relation) { r.Columns[0].Name = "wrong" }},
		{"type", func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = in.Catalog.LookupType(sqltype.Name) }},
		{"foreign_type", func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = foreignIn.Catalog.LookupType(sqltype.Text) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := *in.Catalog.State()
			source := *in.Source
			source.Columns = slices.Clone(source.Columns)
			tc.edit(&source)
			state.SetDescriptor(source.Address(), &source)
			wrong := in
			wrong.Catalog, wrong.Source = e.Catalog.Read(&state), &source
			waitEventsUnitReject(t, wrong, produce)
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{canceled, &pgEnumCancelDuringCapture{Context: t.Context(), after: 4}, &pgEnumCancelDuringCapture{Context: t.Context(), after: 9}} {
		wrong := in
		wrong.Context = ctx
		rows, err := produce(wrong)
		var sqlError interface{ SQLState() string }
		if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &sqlError) {
			t.Fatal("cancellation published rows or SQLSTATE", rows, err)
		}
	}
	waitEventsUnitRows(t, in, first.Anonymous)
}

func waitEventsUnitReject(t *testing.T, in pgcatalog.Input, produce pgcatalog.Producer) {
	t.Helper()
	rows, err := produce(in)
	var sqlError interface{ SQLState() string }
	if rows != nil || err == nil || errors.As(err, &sqlError) {
		t.Fatal("invalid capture must fail with ordinary error and no rows", rows, err)
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if captured != nil || err == nil || errors.As(err, &sqlError) {
		t.Fatal("invalid capture published source", captured, err)
	}
}
