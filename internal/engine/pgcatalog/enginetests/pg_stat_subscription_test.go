package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatSubscriptionSource(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSubscriptionOID}
	source, ok := view.Relation(address)
	if !ok || source.CatalogReturnRule != pgcatalog.StatSubscriptionRuleOID {
		t.Fatal("missing registered subscription statistics source")
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatSubscriptionOID)
	if !ok {
		t.Fatal("missing subscription statistics producer")
	}
	input := pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}), Owner: view.Owner(), Ref: e.Catalog.Domain.Ref(view.Epoch(), address), Source: source}
	captured, err := catalogrt.CaptureRows(input, produce)
	if err != nil || captured == nil || captured.Anonymous == nil || len(captured.Anonymous) != 0 || captured.Ref != input.Ref {
		t.Fatal("typed empty source", captured, err)
	}
	reject := func(in pgcatalog.Input) {
		t.Helper()
		if rows, err := produce(in); err == nil || rows != nil {
			t.Fatal("invalid authority published rows", rows, err)
		}
		if rows, err := catalogrt.CaptureRows(in, produce); err == nil || rows != nil {
			t.Fatal("invalid authority published capture", rows, err)
		}
	}
	for _, change := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Context = nil },
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Owner = foreign.CatalogRead().Owner() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Ref = ids.ObjectRef{} },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.ObjectID++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Domain.Ref(view.Epoch(), address) },
		func(in *pgcatalog.Input) { copied := *source; in.Source = &copied },
		func(in *pgcatalog.Input) { in.Roles = authorization.Read{} },
		func(in *pgcatalog.Input) { in.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{}) },
	} {
		in := input
		change(&in)
		reject(in)
	}
	for _, change := range []func(*catalog.Relation){
		func(r *catalog.Relation) { r.ObjectName = "forged_subscription" },
		func(r *catalog.Relation) { r.RelationKind = catalog.TableRelation },
		func(r *catalog.Relation) { r.VirtualView = false },
		func(r *catalog.Relation) { r.CatalogView = false },
		func(r *catalog.Relation) { r.CatalogReturnRule++ },
		func(r *catalog.Relation) { r.Columns = r.Columns[:10] },
		func(r *catalog.Relation) { r.Columns[3], r.Columns[4] = r.Columns[4], r.Columns[3] },
		func(r *catalog.Relation) { r.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Int4) },
		func(r *catalog.Relation) { r.Columns[1].Usage.Type, _ = foreign.CatalogRead().LookupType(sqltype.Name) },
		func(r *catalog.Relation) { r.Columns[0].Usage.Typmod = 0 },
		func(r *catalog.Relation) { r.Columns[1].Usage.Collation.ID = 0 },
		func(r *catalog.Relation) { r.Columns[2].Usage.Collation.ID = 950 },
		func(r *catalog.Relation) { r.Columns[3].Nullable = false },
		func(r *catalog.Relation) { r.Columns[3].NullabilityKnown = false },
		func(r *catalog.Relation) { r.Columns[3].DeclaredDimensions = 1 },
	} {
		state := *view.State()
		copied := *source
		copied.Columns = append([]catalog.Column(nil), source.Columns...)
		change(&copied)
		state.SetDescriptor(address, &copied)
		in := input
		in.Catalog, in.Source = e.Catalog.Read(&state), &copied
		reject(in)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := input
	in.Context, in.Source = ctx, nil
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation before invalid source dereference", rows, err)
	}
	if rows, err := catalogrt.CaptureRows(in, produce); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled common source published backing", rows, err)
	}

	// Real admitted creation supplies identity, not a fabricated source row.
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, enginetest.Statement(t, `CREATE SUBSCRIPTION "SourceIdentity" CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)`), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	view = e.CatalogRead()
	input.Catalog, input.Source = view, view.Get(address).(*catalog.Relation)
	retained, err := catalogrt.CaptureRows(input, produce)
	if err != nil || len(retained.Anonymous) != 1 || len(retained.Anonymous[0]) != 11 {
		t.Fatal("actual identity source", retained, err)
	}
	row := retained.Anonymous[0]
	var identity ids.OID
	for address, d := range view.Subscriptions() {
		if d.Name() == "SourceIdentity" {
			identity = address.ObjectID
		}
	}
	for i, value := range row {
		if value.Type() != input.Source.Columns[i].Usage.Type || value.IsNull() != (i >= 2) {
			t.Fatalf("column %d lost typed backing/absence", i)
		}
	}
	id, err := row[0].Type().Output(t.Context(), row[0])
	if err != nil || id != fmt.Sprint(identity) {
		t.Fatal("catalog identity", id, identity, err)
	}
	name, err := row[1].Type().Output(t.Context(), row[1])
	if err != nil || name != "SourceIdentity" {
		t.Fatal("exact NAME", name, err)
	}
	// Cancel after enumeration has built a prefix, before it can publish.
	cut := &subscriptionCaptureCancellation{Context: t.Context(), remaining: 2}
	in = input
	in.Context = cut
	if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation published a partial rowset", rows, err)
	}
	oldType, oldRef, oldState := row[1].Type(), retained.Ref, view.State()
	if err := engine.Reset(t.Context(), e); err != nil {
		t.Fatal(err)
	}
	fresh := e.CatalogRead()
	freshType, _ := fresh.LookupType(sqltype.Name)
	if fresh.State() == oldState || fresh.Epoch() == oldRef.Epoch || freshType == oldType || row[1].Type() != oldType || retained.Ref != oldRef || &retained.Anonymous[0][0] != &row[0] {
		t.Fatal("replacement changed retained immutable owner/generation/backing")
	}
	// The old captured root remains valid, but cannot be paired to the new root.
	if rows, err := produce(input); err != nil || len(rows) != 1 || rows[0][1].Type() != oldType {
		t.Fatal("old immutable catalog capture", rows, err)
	}
	input.Catalog = fresh
	reject(input)
}

type subscriptionCaptureCancellation struct {
	context.Context
	remaining int
}

func (c *subscriptionCaptureCancellation) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}
