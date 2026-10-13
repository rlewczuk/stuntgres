package engine

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func queryCreateStage(t *testing.T, e *Engine, name string, width int) (*CatalogStage, *relationDescriptor) {
	t.Helper()
	columns := make([]ColumnDescriptor, width)
	for i := range columns {
		columns[i] = ColumnDescriptor{Name: "i", Usage: testUsage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}
	}
	e.mu.Lock()
	d, err := e.newTableDescriptorLocked(ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name, e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), columns, nil)
	stage := e.BeginCatalogStage(CatalogRead{e, e.state})
	if err == nil {
		err = stage.Create(d)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return stage, d
}

func queryCreateTypes(e *Engine) map[ObjectAddress]Descriptor {
	types := make(map[ObjectAddress]Descriptor)
	for address, descriptor := range e.CatalogRead().state.descriptors {
		if address.ClassID == TypeClassID {
			types[address] = descriptor
		}
	}
	return types
}

func queryCreateAssertTypes(t *testing.T, e *Engine, want map[ObjectAddress]Descriptor) {
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

func queryCreateAssertAbsent(t *testing.T, e *Engine, d *relationDescriptor) {
	t.Helper()
	view := e.CatalogRead()
	if _, ok := view.Descriptor(d.address); ok || e.storage[d.address] != nil {
		t.Fatal("partial descriptor/storage publication")
	}
	for _, class := range []OID{RelationClassID, TypeClassID} {
		if len(view.Lookup(NameKey{ClassID: class, Namespace: d.namespace, Name: d.name})) != 0 {
			t.Fatal("partial name/type publication", class)
		}
	}
}

func queryCreatePublish(t *testing.T, e *Engine, stage *CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil {
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
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
			before := e.commitSeq
			rows := make([][]sqltype.Value, tc.count)
			for i := range rows {
				rows[i] = make([]sqltype.Value, tc.width)
				if tc.width != 0 {
					rows[i][0], _ = sqltype.Integer(d.columns[0].Usage.Type, 7)
				}
			}
			if err := e.stageQueryTableRows(context.Background(), stage, d, rows, TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreateAssertAbsent(t, e, d)
			queryCreateAssertTypes(t, e, registry)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			queryCreateAssertAbsent(t, e, d)
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			descriptor, ok := e.CatalogRead().Descriptor(d.address)
			storage := e.storage[d.address]
			if !ok || descriptor != d || storage == nil || len(storage.versions) != tc.count {
				t.Fatal(descriptor, storage)
			}
			wantFrontier := before
			if tc.count != 0 {
				wantFrontier++
			}
			if e.commitSeq != wantFrontier || e.nextRow != uint64(tc.count) || e.nextVersion != uint64(tc.count) {
				t.Fatal("frontier or shared counters", e.commitSeq, e.nextRow, e.nextVersion)
			}
			if tc.count != 0 && storage.generation != 1 {
				t.Fatal(storage.generation)
			}
			for i, row := range storage.versions {
				if row.row != RowID(i+1) || row.version != VersionID(i+1) || row.born != wantFrontier || len(row.values) != tc.width {
					t.Fatal(row)
				}
				if tc.width != 0 && !sqltype.SameDatum(row.values[0], rows[i][0]) {
					t.Fatal(row.values, rows[i])
				}
			}
			queryCreateAssertTypes(t, e, registry)
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err == nil || e.commitSeq != wantFrontier {
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
			value, _ := sqltype.Integer(d.columns[0].Usage.Type, 7)
			rows := [][]sqltype.Value{{value}}
			if phase == "row_identity" {
				e.nextRow = math.MaxUint64
			}
			if phase == "version_identity" {
				e.nextVersion = math.MaxUint64
			}
			beforeRow, beforeVersion := e.nextRow, e.nextVersion
			err := e.stageQueryTableRows(context.Background(), stage, d, rows, TxRef{})
			if phase == "row_identity" || phase == "version_identity" {
				want := "stuntgres: row identity exhausted"
				if phase == "version_identity" {
					want = "stuntgres: version identity exhausted"
				}
				if err == nil || err.Error() != want || e.nextRow != beforeRow || e.nextVersion != beforeVersion {
					t.Fatal(err, e.nextRow, e.nextVersion)
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
				e.providers[RelationClassID] = queryCreateProbeProvider{CatalogProvider: e.providers[RelationClassID], e: e, t: t, check: func() error { return sentinel }}
			}
			if phase == "invalid_width" {
				stage.initialTables[d.address].versions[0].values = nil
			}
			if phase == "invalid_type" {
				stage.initialTables[d.address].versions[0].values = []sqltype.Value{sqltype.Null(testUsage(e, sqltype.Text).Type)}
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
			before := e.commitSeq
			switch phase {
			case "frontier":
				e.commitSeq = CommitSeq(math.MaxUint64)
				before = e.commitSeq
			case "cancel":
				cancel()
			case "name":
				peer, _ := queryCreateStage(t, e, "copy", 1)
				queryCreatePublish(t, e, peer)
			case "epoch":
				if _, err = e.ReplaceCatalog(context.Background(), New().CatalogRead()); err != nil {
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
					stage.authRevision = auth.revision
					stage.roleRevision = e.roles.revision
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
				original := e.CatalogRead().state.descriptors[d.namespace]
				stage.querySources = []Descriptor{original}
				next := *original.(*namespaceDescriptor)
				next.revision++
				peer := e.BeginCatalogStage(e.CatalogRead())
				if err = peer.Replace(&next); err != nil {
					t.Fatal(err)
				}
				queryCreatePublish(t, e, peer)
			}
			before = e.commitSeq
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()})
			if err == nil {
				t.Fatal("invalid stage published", phase)
			}
			switch phase {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "epoch":
				if !errors.Is(err, ErrCatalogReplaced) {
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
				var conflict CatalogConflict
				if !errors.As(err, &conflict) {
					t.Fatal(err)
				}
			}
			if phase == "name" {
				if _, ok := e.CatalogRead().Descriptor(d.address); ok || e.storage[d.address] != nil {
					t.Fatal("loser published")
				}
			} else {
				queryCreateAssertAbsent(t, e, d)
			}
			if phase != "epoch" {
				queryCreateAssertTypes(t, e, registry)
			}
			if e.commitSeq != before || stage.initialTables[d.address].versions[0].born != 0 {
				t.Fatal("failed birth/frontier", e.commitSeq)
			}
		})
	}
}

type queryCreateProbeProvider struct {
	CatalogProvider
	e     *Engine
	t     *testing.T
	check func() error
}

func (p queryCreateProbeProvider) Validate(view CatalogRead, d Descriptor) error {
	if !p.e.mu.TryLock() {
		p.t.Fatal("definition callback under Engine.mu")
	}
	p.e.mu.Unlock()
	if err := p.CatalogProvider.Validate(view, d); err != nil {
		return err
	}
	return p.check()
}

func TestQueryTableCreateUnrelatedRefresh(t *testing.T) {
	e := New()
	stage, d := queryCreateStage(t, e, "copy", 1)
	value, _ := sqltype.Integer(d.columns[0].Usage.Type, 7)
	if err := e.stageQueryTableRows(context.Background(), stage, d, [][]sqltype.Value{{value}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	peer, _ := queryCreateStage(t, e, "unrelated", 1)
	queryCreatePublish(t, e, peer)
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	if e.nextRow != 1 || e.nextVersion != 1 || e.commitSeq != 1 || len(e.storage[d.address].versions) != 1 || !sqltype.SameDatum(e.storage[d.address].versions[0].values[0], value) {
		t.Fatal("refresh replayed or lost data")
	}
}

func TestQueryTableCreateSharedRowIdentities(t *testing.T) {
	e := New()
	ordinaryStage, ordinary := queryCreateStage(t, e, "ordinary", 1)
	queryCreatePublish(t, e, ordinaryStage)
	value, _ := sqltype.Integer(ordinary.columns[0].Usage.Type, 7)
	discarded, d := queryCreateStage(t, e, "discarded", 1)
	if err := e.stageQueryTableRows(context.Background(), discarded, d, [][]sqltype.Value{{value}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	reserved := discarded.initialTables[d.address].versions[0]
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
	ref := ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: ordinary.address}
	if outcome = scope.stage.Apply(ref, [][]sqltype.Value{{value}}); outcome != nil {
		t.Fatal(outcome)
	}
	staged := scope.stage.rows[ref.Address][0]
	if staged.row != reserved.row+1 || staged.version != reserved.version+1 || staged.born != 0 {
		t.Fatal("ordinary stage reused a discarded initial identity")
	}
	if outcome = scope.Finish(nil); outcome != nil {
		t.Fatal(outcome)
	}
	stored := e.storage[ref.Address].versions[0]
	if stored.row != staged.row || stored.version != staged.version || stored.born != e.commitSeq {
		t.Fatal("ordinary publication changed staged identity")
	}
	before := e.commitSeq
	nextStage, next := queryCreateStage(t, e, "next_copy", 1)
	if err = e.stageQueryTableRows(context.Background(), nextStage, next, [][]sqltype.Value{{value}}, TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, nextStage)
	copied := e.storage[next.address].versions[0]
	if copied.row != stored.row+1 || copied.version != stored.version+1 || copied.born != before+1 || e.commitSeq != before+1 || reserved.born != 0 {
		t.Fatal("initial storage did not share the ordinary non-reusing identity/frontier owner")
	}
}
