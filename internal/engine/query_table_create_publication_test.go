package engine

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func queryCreateStage(t *testing.T, e *Engine, name string, width int) (*CatalogStage, *catalog.Relation) {
	t.Helper()
	columns := make([]catalog.Column, width)
	for i := range columns {
		columns[i] = catalog.Column{Name: "i", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}
	}
	e.mu.Lock()
	d, err := e.newTableDescriptorLocked(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, name, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), columns, nil)
	stage := e.BeginCatalogStage(e.catalog.read(e.catalog.state))
	if err == nil {
		err = stage.Create(d)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage, d
}

func queryCreateTypes(e *Engine) map[ids.ObjectAddress]catalog.Descriptor {
	types := make(map[ids.ObjectAddress]catalog.Descriptor)
	for address, descriptor := range e.CatalogRead().Descriptors() {
		if address.ClassID == ids.TypeClassID {
			types[address] = descriptor
		}
	}
	return types
}

func queryCreateAssertTypes(t *testing.T, e *Engine, want map[ids.ObjectAddress]catalog.Descriptor) {
	t.Helper()
	got := queryCreateTypes(e)
	if len(got) != len(want) {
		t.Fatal("type registry leaked a definition", len(got), len(want))
	}
	for address, descriptor := range want {
		if got[address] != descriptor {
			t.Fatal("type generation changed", address)
		}
	}
}

func queryCreateAssertAbsent(t *testing.T, e *Engine, d *catalog.Relation) {
	t.Helper()
	view := e.CatalogRead()
	if _, ok := view.Descriptor(d.Address()); ok || e.storage.Relations[d.Address()] != nil {
		t.Fatal("partial descriptor/storage publication")
	}
	for _, class := range []ids.OID{ids.RelationClassID, ids.TypeClassID} {
		if len(view.Lookup(ids.NameKey{ClassID: class, Namespace: d.Namespace(), Name: d.Name()})) != 0 {
			t.Fatal("partial name/type publication", class)
		}
	}
}

func queryCreatePublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestQueryTableCreateInitialPublication(t *testing.T) {
	for _, tc := range []struct {
		name         string
		width, count int
	}{{"duplicates", 1, 3}, {"zero_width", 0, 3}, {"empty", 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			registry := queryCreateTypes(e)
			stage, d := queryCreateStage(t, e, "copy", tc.width)
			before := e.visibility.commitSeq
			rows := make([][]sqltype.Value, tc.count)
			for i := range rows {
				rows[i] = make([]sqltype.Value, tc.width)
				if tc.width != 0 {
					rows[i][0], _ = sqltype.Integer(d.Columns[0].Usage.Type, 7)
				}
			}
			if err := e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreateAssertAbsent(t, e, d)
			queryCreateAssertTypes(t, e, registry)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			queryCreateAssertAbsent(t, e, d)
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			descriptor, ok := e.CatalogRead().Descriptor(d.Address())
			storage := e.storage.Relations[d.Address()]
			if !ok || descriptor != d || storage == nil || len(storage.Versions) != tc.count {
				t.Fatal(descriptor, storage)
			}
			wantFrontier := before
			if tc.count != 0 {
				wantFrontier++
			}
			if e.visibility.commitSeq != wantFrontier || e.storage.RowFrontier() != uint64(tc.count) || e.storage.VersionFrontier() != uint64(tc.count) {
				t.Fatal("frontier or shared counters", e.visibility.commitSeq, e.storage.RowFrontier(), e.storage.VersionFrontier())
			}
			if tc.count != 0 && storage.Generation != 1 {
				t.Fatal(storage.Generation)
			}
			for i, row := range storage.Versions {
				if row.Row != ids.RowID(i+1) || row.Version != ids.VersionID(i+1) || row.Born != wantFrontier || len(row.Values) != tc.width {
					t.Fatal(row)
				}
				if tc.width != 0 && !sqltype.SameDatum(row.Values[0], rows[i][0]) {
					t.Fatal(row.Values, rows[i])
				}
			}
			queryCreateAssertTypes(t, e, registry)
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil || e.visibility.commitSeq != wantFrontier {
				t.Fatal("replayed publication", err)
			}
		})
	}
}

func TestQueryTableCreateInitialPublicationFailures(t *testing.T) {
	for _, phase := range []string{"row_identity", "version_identity", "frontier", "cancel", "name", "epoch", "provider", "authorization", "role_revision", "source_definition", "invalid_width", "invalid_type"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			registry := queryCreateTypes(e)
			stage, d := queryCreateStage(t, e, "copy", 1)
			defer e.DiscardCatalogStage(stage)
			value, _ := sqltype.Integer(d.Columns[0].Usage.Type, 7)
			rows := [][]sqltype.Value{{value}}
			if phase == "row_identity" {
				e.storage.SetFrontierLocked(math.MaxUint64, e.storage.VersionFrontier())
			}
			if phase == "version_identity" {
				e.storage.SetFrontierLocked(e.storage.RowFrontier(), math.MaxUint64)
			}
			beforeRow, beforeVersion := e.storage.RowFrontier(), e.storage.VersionFrontier()
			err := e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{})
			if phase == "row_identity" || phase == "version_identity" {
				want := "stuntgres: row identity exhausted"
				if phase == "version_identity" {
					want = "stuntgres: version identity exhausted"
				}
				if err == nil || err.Error() != want || e.storage.RowFrontier() != beforeRow || e.storage.VersionFrontier() != beforeVersion {
					t.Fatal(err, e.storage.RowFrontier(), e.storage.VersionFrontier())
				}
				queryCreateAssertAbsent(t, e, d)
				queryCreateAssertTypes(t, e, registry)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("definition provider veto")
			if phase == "provider" {
				e.catalog.Providers[ids.RelationClassID] = queryCreateProbeProvider{Provider: e.catalog.Providers[ids.RelationClassID], e: e, t: t, check: func() error { return sentinel }}
			}
			if phase == "invalid_width" {
				stage.initialTables[d.Address()].Versions[0].Values = nil
			}
			if phase == "invalid_type" {
				stage.initialTables[d.Address()].Versions[0].Values = []sqltype.Value{sqltype.Null(testUsage(e, sqltype.Text).Type)}
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if phase == "provider" || phase == "invalid_width" || phase == "invalid_type" {
				want := sentinel.Error()
				if phase == "invalid_width" {
					want = "stuntgres: invalid initial table row"
				}
				if phase == "invalid_type" {
					want = "stuntgres: invalid initial table value type"
				}
				if prepared != nil || err == nil || err.Error() != want {
					t.Fatal(prepared, err)
				}
				queryCreateAssertAbsent(t, e, d)
				queryCreateAssertTypes(t, e, registry)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			before := e.visibility.commitSeq
			switch phase {
			case "frontier":
				e.visibility.commitSeq = ids.CommitSeq(math.MaxUint64)
				before = e.visibility.commitSeq
			case "cancel":
				cancel()
			case "name":
				peer, _ := queryCreateStage(t, e, "copy", 1)
				queryCreatePublish(t, e, peer)
			case "epoch":
				if _, err = replaceWithFreshCatalog(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "authorization", "role_revision":
				op, done, opErr := testOperation(e, context.Background())
				if opErr != nil {
					t.Fatal(opErr)
				}
				defer done()
				e.mu.Lock()
				auth, authErr := e.authorizationLocked(op)
				if authErr == nil {
					stage.authSession = op.session
					stage.authRevision = auth.Revision
					stage.roleRevision = e.roles.published.Revision
				}
				e.mu.Unlock()
				if authErr != nil {
					t.Fatal(authErr)
				}
				if phase == "authorization" {
					stage.authRevision++
				} else {
					stage.roleRevision++
				}
			case "source_definition":
				original := e.CatalogRead().Get(d.Namespace())
				stage.querySources = []catalog.Descriptor{original}
				next := *original.(*catalog.Namespace)
				next.ObjectRevision++
				peer := e.BeginCatalogStage(e.CatalogRead())
				if err = peer.Replace(&next); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, peer)
			}
			before = e.visibility.commitSeq
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if err == nil {
				t.Fatal("invalid stage published", phase)
			}
			switch phase {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "epoch":
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "frontier":
				if err.Error() != "stuntgres: commit sequence exhausted" {
					t.Fatal(err)
				}
			case "authorization", "role_revision":
				if err.Error() != "stuntgres: authorization changed before publication" {
					t.Fatal(err)
				}
			default:
				var conflict catalog.Conflict
				if !errors.As(err, &conflict) {
					t.Fatal(err)
				}
			}
			if phase == "name" {
				if _, ok := e.CatalogRead().Descriptor(d.Address()); ok || e.storage.Relations[d.Address()] != nil {
					t.Fatal("loser published")
				}
			} else {
				queryCreateAssertAbsent(t, e, d)
			}
			if phase != "epoch" {
				queryCreateAssertTypes(t, e, registry)
			}
			if e.visibility.commitSeq != before || stage.initialTables[d.Address()].Versions[0].Born != 0 {
				t.Fatal("failed birth/frontier", e.visibility.commitSeq)
			}
		})
	}
}

type queryCreateProbeProvider struct {
	catalog.Provider
	e     *Engine
	t     *testing.T
	check func() error
}

func (p queryCreateProbeProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	if !p.e.mu.TryLock() {
		p.t.Fatal("definition callback under Engine.mu")
	}
	p.e.mu.Unlock()
	if err := p.Provider.Validate(view, d); err != nil {
		return err
	}
	return p.check()
}

func TestQueryTableCreateUnrelatedRefresh(t *testing.T) {
	e := New()
	stage, d := queryCreateStage(t, e, "copy", 1)
	value, _ := sqltype.Integer(d.Columns[0].Usage.Type, 7)
	if err := e.stageQueryTableRows(context.Background(), stage, d, [][]sqltype.Value{{value}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := queryCreateStage(t, e, "unrelated", 1)
	queryCreatePublish(t, e, peer)
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if e.storage.RowFrontier() != 1 || e.storage.VersionFrontier() != 1 || e.visibility.commitSeq != 1 || len(e.storage.Relations[d.Address()].Versions) != 1 || !sqltype.SameDatum(e.storage.Relations[d.Address()].Versions[0].Values[0], value) {
		t.Fatal("refresh replayed or lost data")
	}
}

func TestQueryTableCreateSharedRowIdentities(t *testing.T) {
	e := New()
	ordinaryStage, ordinary := queryCreateStage(t, e, "ordinary", 1)
	queryCreatePublish(t, e, ordinaryStage)
	value, _ := sqltype.Integer(ordinary.Columns[0].Usage.Type, 7)
	discarded, d := queryCreateStage(t, e, "discarded", 1)
	if err := e.stageQueryTableRows(context.Background(), discarded, d, [][]sqltype.Value{{value}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	reserved := discarded.initialTables[d.Address()].Versions[0]
	e.DiscardCatalogStage(discarded)
	queryCreateAssertAbsent(t, e, d)
	session := sessionStart(t, e, nil)
	op := transactionOperation(t, session)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	var outcome error
	defer func() { _ = scope.Finish(outcome) }()
	if outcome = scope.gate(&parser.Insert{}); outcome != nil {
		t.Fatal(outcome)
	}
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), ordinary.Address())
	if outcome = scope.stage.Apply(ref, [][]sqltype.Value{{value}}); outcome != nil {
		t.Fatal(outcome)
	}
	staged := scope.stage.rows[ref.Address][0]
	if staged.Row != reserved.Row+1 || staged.Version != reserved.Version+1 || staged.Born != 0 {
		t.Fatal("ordinary stage reused a discarded initial identity")
	}
	if outcome = scope.Finish(nil); outcome != nil {
		t.Fatal(outcome)
	}
	stored := e.storage.Relations[ref.Address].Versions[0]
	if stored.Row != staged.Row || stored.Version != staged.Version || stored.Born != e.visibility.commitSeq {
		t.Fatal("ordinary publication changed staged identity")
	}
	before := e.visibility.commitSeq
	nextStage, next := queryCreateStage(t, e, "next_copy", 1)
	if err = e.stageQueryTableRows(context.Background(), nextStage, next, [][]sqltype.Value{{value}}, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, nextStage)
	copied := e.storage.Relations[next.Address()].Versions[0]
	if copied.Row != stored.Row+1 || copied.Version != stored.Version+1 || copied.Born != before+1 || e.visibility.commitSeq != before+1 || reserved.Born != 0 {
		t.Fatal("initial storage did not share the ordinary non-reusing identity/frontier owner")
	}
}
