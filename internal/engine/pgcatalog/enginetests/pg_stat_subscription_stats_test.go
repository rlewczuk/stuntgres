package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/statistics"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPGStatSubscriptionStatsSource(t *testing.T) {
	e, foreign := engine.New(), engine.New()
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatSubscriptionStatsOID}
	source, ok := view.Relation(address)
	if !ok || source.CatalogReturnRule != pgcatalog.StatSubscriptionStatsRuleOID {
		t.Fatal("missing registered subscription statistics source")
	}
	names := [...]string{"subid", "subname", "apply_error_count", "sync_error_count", "confl_insert_exists", "confl_update_origin_differs", "confl_update_exists", "confl_update_missing", "confl_delete_origin_differs", "confl_delete_missing", "confl_multiple_unique_conflicts", "stats_reset"}
	types := [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Int8, sqltype.Timestamptz}
	if len(source.Columns) != 12 || source.RowType != 12349 || source.Address().ObjectID != 12347 || source.CatalogReturnRule != 12350 {
		t.Fatal("native allocated relation/type/rule identity", source)
	}
	for i, column := range source.Columns {
		typ, _ := view.LookupType(types[i])
		collation := uint32(0)
		if i == 1 {
			collation = 950
		}
		if column.Name != names[i] || column.Usage.Type != typ || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 || column.OriginTable != 12347 || column.OriginAttribute != int16(i+1) || source.Attributes[i] != int32(i+1) {
			t.Fatal("independently specified source descriptor", i, column)
		}
	}
	produce, ok := pgcatalog.ProducerFor(pgcatalog.StatSubscriptionStatsOID)
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
		func(in *pgcatalog.Input) {
			in.Roles = foreign.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
		},
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
		func(r *catalog.Relation) { r.Columns = r.Columns[:11] },
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
	var subscriptionAddress ids.ObjectAddress
	for address, d := range view.Subscriptions() {
		if d.Name() == "SourceIdentity" {
			subscriptionAddress = address
		}
	}
	at := time.Unix(1700000000, 123456000).UTC()
	fact := statistics.Fact{ApplyErrorCount: 11, SyncErrorCount: 23, ConflInsertExists: 37, ConflUpdateOriginDiffers: 41, ConflUpdateExists: 53, ConflUpdateMissing: 67, ConflDeleteOriginDiffers: 71, ConflDeleteMissing: 83, ConflMultipleUniqueConflicts: 97, ResetAt: at}
	input.SubscriptionStats = map[ids.ObjectAddress]statistics.Fact{subscriptionAddress: fact, {ClassID: ids.SubscriptionClassID, ObjectID: 999999}: fact}
	retained, err := catalogrt.CaptureRows(input, produce)
	if err != nil || len(retained.Anonymous) != 1 || len(retained.Anonymous[0]) != 12 {
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
		if value.Type() != input.Source.Columns[i].Usage.Type || value.IsNull() {
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
	for i, want := range []string{"11", "23", "37", "41", "53", "67", "71", "83", "97"} {
		got, err := row[i+2].Type().Output(t.Context(), row[i+2])
		if err != nil || got != want {
			t.Fatal("distinct counter field mapping", i, got, want, err)
		}
	}
	stamp, err := sqltype.TimestampFromTime(input.Source.Columns[11].Usage.Type, at)
	if err != nil {
		t.Fatal(err)
	}
	gotStamp, err := row[11].Type().Output(t.Context(), row[11])
	if err != nil {
		t.Fatal(err)
	}
	wantStamp, err := stamp.Type().Output(t.Context(), stamp)
	if err != nil || gotStamp != wantStamp {
		t.Fatal("reset instant mapping", gotStamp, wantStamp, err)
	}
	missing := input
	missing.SubscriptionStats = nil
	zeros, err := produce(missing)
	if err != nil || len(zeros) != 1 {
		t.Fatal("missing facts preserve descriptor cardinality", zeros, err)
	}
	for i := 2; i <= 10; i++ {
		text, err := zeros[0][i].Type().Output(t.Context(), zeros[0][i])
		if err != nil || zeros[0][i].IsNull() || text != "0" {
			t.Fatal("missing counter typed zero", i, text, err)
		}
	}
	if !zeros[0][11].IsNull() {
		t.Fatal("missing reset must be SQL NULL")
	}
	t.Run("NameKeep", func(t *testing.T) {
		result := enginetest.SessionSQL(t, s, `CREATE SUBSCRIPTION "SourcePeer" CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)`)
		if result.Rows != nil {
			result.Rows.Close()
		}
		current := e.CatalogRead()
		filtered := input
		filtered.Catalog, filtered.Source = current, current.MustRelation(address)
		var peerAddress ids.ObjectAddress
		for key, subscription := range current.Subscriptions() {
			if subscription.Name() == "SourcePeer" {
				peerAddress = key
			}
		}
		if peerAddress == (ids.ObjectAddress{}) || peerAddress == subscriptionAddress {
			t.Fatal("distinct genuine subscription descriptors")
		}
		peerFact := statistics.Fact{ApplyErrorCount: 101, SyncErrorCount: 103, ConflInsertExists: 107, ConflUpdateOriginDiffers: 109, ConflUpdateExists: 113, ConflUpdateMissing: 127, ConflDeleteOriginDiffers: 131, ConflDeleteMissing: 137, ConflMultipleUniqueConflicts: 139, ResetAt: time.Unix(1800000000, 234567000).UTC()}
		filtered.SubscriptionStats = map[ids.ObjectAddress]statistics.Fact{subscriptionAddress: fact, peerAddress: peerFact}
		keys := pgcatalog.KeyColumns(pgcatalog.StatSubscriptionStatsOID)
		if len(keys) != 1 || keys[0] != 1 {
			t.Fatal("only catalog-owned name may isolate getter demand", keys)
		}
		keepName := func(name string) pgcatalog.Keep {
			return pgcatalog.Keep{Column: pgcatalog.StatSubscriptionStatsNameColumn, Test: func(value sqltype.Value) bool {
				if value.Type() != filtered.Source.Columns[1].Usage.Type || value.Type().ID() != sqltype.Name || value.IsNull() {
					t.Fatal("Keep must receive the actual typed catalog NAME", value)
				}
				text, err := value.Type().Output(t.Context(), value)
				if err != nil {
					t.Fatal(err)
				}
				return text == name
			}}
		}
		for _, name := range []string{"SourceIdentity", "SourcePeer", "absent"} {
			filtered.Keep = keepName(name)
			rows, err := catalogrt.CaptureRows(filtered, produce)
			if err != nil || rows == nil || rows.Anonymous == nil || rows.Ref != filtered.Ref || len(filtered.Source.Columns) != len(names) {
				t.Fatal("filtered source preserves typed metadata/authority", rows, err)
			}
			if name == "absent" {
				if len(rows.Anonymous) != 0 {
					t.Fatal("unknown name manufactured a row", rows.Anonymous)
				}
				continue
			}
			wantID, wantFact := subscriptionAddress, fact
			if name == "SourcePeer" {
				wantID, wantFact = peerAddress, peerFact
			}
			if len(rows.Anonymous) != 1 {
				t.Fatal("excluded descriptor leaked into filtered producer", rows.Anonymous)
			}
			values := rows.Anonymous[0]
			gotID, err := values[0].OID()
			gotName, nameErr := values[1].Type().Output(t.Context(), values[1])
			if err != nil || nameErr != nil || ids.OID(gotID) != wantID.ObjectID || gotName != name {
				t.Fatal("selected actual identity", gotID, gotName, err, nameErr)
			}
			counts := [...]int64{wantFact.ApplyErrorCount, wantFact.SyncErrorCount, wantFact.ConflInsertExists, wantFact.ConflUpdateOriginDiffers, wantFact.ConflUpdateExists, wantFact.ConflUpdateMissing, wantFact.ConflDeleteOriginDiffers, wantFact.ConflDeleteMissing, wantFact.ConflMultipleUniqueConflicts}
			for i, want := range counts {
				if got, err := values[i+2].Integer(); err != nil || got != want {
					t.Fatal("selected distinct provider fields", name, i, got, want, err)
				}
			}
			if got, err := values[11].TimestampTime(); err != nil || !got.Equal(wantFact.ResetAt) {
				t.Fatal("selected exact reset", got, wantFact.ResetAt, err)
			}
		}
		// An intentionally uncaptured B must not become a synthetic zero/NULL row.
		filtered.Keep = keepName("SourceIdentity")
		delete(filtered.SubscriptionStats, peerAddress)
		rows, err := produce(filtered)
		if err != nil || len(rows) != 1 {
			t.Fatal("excluded absent fact manufactured a row", rows, err)
		}
		filtered.Keep = pgcatalog.Keep{Column: 0, Test: func(sqltype.Value) bool { return false }}
		rows, err = produce(filtered)
		if err != nil || len(rows) != 2 {
			t.Fatal("getter-produced subid is not a demand-isolating key", rows, err)
		}
		filtered.Keep = keepName("absent")
		invalid := filtered
		invalid.Roles = authorization.Read{}
		reject(invalid)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		filtered.Context = ctx
		filtered.Keep = pgcatalog.Keep{Column: 1, Test: func(sqltype.Value) bool {
			cancel()
			return true
		}}
		if rows, err := produce(filtered); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("filtered cancellation published a partial producer prefix", rows, err)
		}
	})
	input.SubscriptionStats[subscriptionAddress] = statistics.Fact{}
	for i, want := range []string{"11", "23", "37", "41", "53", "67", "71", "83", "97"} {
		got, _ := row[i+2].Type().Output(t.Context(), row[i+2])
		if got != want {
			t.Fatal("retained values borrowed mutable input", i, got)
		}
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
