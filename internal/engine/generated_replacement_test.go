package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

// The existing session random provider gates actual CHECK Eval, after generation
// has completed the candidate row. No scheduling delays or production hooks are
// needed; the handshake also proves that a genuine statement scope was admitted.
type generatedRepairSource struct {
	entered  chan struct{}
	release  chan struct{}
	onReturn func()
	calls    int
}

func (s *generatedRepairSource) Uint64() uint64 {
	s.calls++
	if s.calls == 1 {
		close(s.entered)
		<-s.release
		if s.onReturn != nil {
			s.onReturn()
		}
	}
	return 1 << 63
}

type generatedRepairFixture struct {
	target     *Engine
	stage      *Engine
	session    *session.Session
	op         *session.Operation
	next       *catalog.State
	storage    map[ids.ObjectAddress]*storage.Relation
	sequences  map[ids.ObjectAddress]*storage.SequenceGenerator
	mapping    map[ids.ObjectAddress]ids.ObjectAddress
	imported   ids.ObjectAddress
	temp       ids.ObjectAddress
	source     []sqltype.Value
	tempValues []sqltype.Value
	gate       *generatedRepairSource
}

func newGeneratedRepairFixture(t *testing.T) *generatedRepairFixture {
	t.Helper()
	e, view, d := generatedUnitDescriptor(t)
	d.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	oldAddress := d.Address()
	d.ObjectAddress.ObjectID = 26000
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	floating, _ := view.LookupType(sqltype.Float8)
	d.Columns[1].Usage = oid.Usage()
	ref := e.Catalog.Domain.Ref(view.Epoch()+1, d.Address())
	leaf := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ref}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: leaf, Planned: leaf}}
	// CHECK consumes the repaired cell as well as the scoped provider. A stale
	// source tableoid vetoes the row instead of reaching the final stale fence.
	probe := functions.Random{Info: expr.Info{Usage: floating.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "random"}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	expectedOID, err := sqltype.OIDValue(oid, uint32(d.Address().ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	columnRef := ref
	columnRef.Address.SubID = 2
	repairedCell := plan.ColumnExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: columnRef, Scope: 1, Frame: 1, Slot: 1}
	equal := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: repairedCell, Right: plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: expectedOID}, Predicate: predicate, Operator: "="}
	scoped := plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: probe, Not: true}
	d.Constraints.Check = []catalog.CheckConstraint{{Name: "scoped_gate", Expression: plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalAnd, Left: equal, Right: scoped}}}
	base, err := sqltype.Integer(d.Columns[0].Usage.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	oldOID, err := sqltype.OIDValue(oid, uint32(oldAddress.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	source := []sqltype.Value{base, oldOID}
	next := catalog.CloneState(view.State())
	next.SetEpoch(next.Epoch() + 1)
	next.SetDescriptor(d.Address(), d)
	next.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: d.Name()}, d.Address())
	// Retain a real TEMP namespace and row vector in the target generation.
	tempNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 27000}
	tempAddress := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 27001}
	temp := *d
	temp.ObjectAddress, temp.ObjectNamespace, temp.ObjectName = tempAddress, tempNamespace, "retained"
	temp.ColumnMeta.Generations, temp.Constraints.Check = nil, nil
	tempValues := append([]sqltype.Value(nil), source...)
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetDescriptor(tempNamespace, &catalog.Namespace{ObjectAddress: tempNamespace, ObjectName: "pg_temp_1", Owner: d.Owner, ObjectRevision: 1, Temporary: e.Catalog.Domain.Session(99)})
	e.Catalog.State.SetDescriptor(tempAddress, &temp)
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(tempAddress, &storage.Relation{Versions: []storage.RowVersion{{Values: tempValues}}})
	e.Mu.Unlock()
	stage := e.RestoreStage()
	sess, err := stage.StartSession(context.Background(), session.SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	op, err := sess.Acquire(context.Background())
	if err != nil {
		sess.RequestEnd(session.CleanClose)
		_ = sess.WaitEnd(context.Background())
		t.Fatal(err)
	}
	gate := &generatedRepairSource{entered: make(chan struct{}), release: make(chan struct{})}
	sess.Random.Rand = *rand.New(gate)
	sess.Random.Initialized = true
	f := &generatedRepairFixture{target: e, stage: stage, session: sess, op: op, next: next, storage: map[ids.ObjectAddress]*storage.Relation{d.Address(): {Versions: []storage.RowVersion{{Values: source}}}}, sequences: map[ids.ObjectAddress]*storage.SequenceGenerator{{ClassID: ids.RelationClassID, ObjectID: 28000}: {Last: 77, Called: true}}, mapping: map[ids.ObjectAddress]ids.ObjectAddress{oldAddress: d.Address()}, imported: d.Address(), temp: tempAddress, source: source, tempValues: tempValues, gate: gate}
	t.Cleanup(func() {
		op.Done()
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *generatedRepairFixture) run(ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() {
		f.target.ReplacementMu.Lock()
		defer f.target.ReplacementMu.Unlock()
		f.target.Mu.Lock()
		defer f.target.Mu.Unlock()
		oids := f.target.Catalog.OIDReservationLocked()
		_, err := f.target.Catalog.AllocateOIDFromLocked(&oids.Next, 0)
		if err == nil {
			_, err = f.target.repairRemappedGenerationsLocked(ctx, f.next, f.storage, f.sequences, f.mapping, f.op, nil)
		}
		if err == nil {
			var prepared index.RemintedIndexStorage
			prepared, err = f.target.prepareForeignIndexStorage(ctx, f.next, f.storage, &index.Snapshot{})
			if err == nil {
				err = f.target.installForeignCatalogLocked(ctx, f.next, f.storage, f.sequences, f.target.Catalog.NextRevision(), prepared, oids)
			}
		}
		result <- err
	}()
	return result
}

func (f *generatedRepairFixture) assertJoined(t *testing.T) {
	t.Helper()
	// A second statement proves Finish released admission, rather than merely
	// returning an error while leaving the real session's statement locked.
	scope, err := f.stage.StartStatement(f.op)
	if err != nil {
		t.Fatal("repair left operation unusable", err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	f.op.Done()
	f.session.RequestEnd(session.CleanClose)
	if err := f.session.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.session.Mu.Lock()
	defer f.session.Mu.Unlock()
	if f.session.State != session.Ended || f.session.Leases != 0 || len(f.session.Operations) != 0 {
		t.Fatal("session cleanup did not join operations", f.session.State, f.session.Leases, len(f.session.Operations))
	}
}

func TestGeneratedReplacementStaleAuthority(t *testing.T) {
	for _, change := range []string{"Catalog", "Roles", "Temporary", "CommitFrontier", "OIDFrontier"} {
		t.Run(change, func(t *testing.T) {
			f := newGeneratedRepairFixture(t)
			result := f.run(context.Background())
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(f.gate.release) }) })
			awaitGeneratedRepairEval(t, f.gate, result)
			f.target.Mu.Lock() // actual Eval must not retain the target mutex
			switch change {
			case "Catalog":
				f.target.Catalog.PublishLocked(catalog.CloneState(f.target.Catalog.State))
				f.target.Catalog.State.SetEpoch(f.target.Catalog.State.Epoch() + 1)
				f.target.Catalog.State.SetNamespaceGeneration(f.target.Catalog.State.NamespaceGeneration() + 1)
			case "Roles":
				roles := *f.target.RoleStore.Published
				roles.Revision++
				f.target.RoleStore.PublishLocked(&roles)
			case "Temporary":
				next := catalog.CloneState(f.target.Catalog.State)
				copy := *next.MustRelation(f.temp)
				copy.ObjectAddress.ObjectID++
				copy.ObjectName = "concurrent_temp"
				next.SetDescriptor(copy.Address(), &copy)
				f.target.Storage.InstallRelationLocked(copy.Address(), &storage.Relation{Versions: []storage.RowVersion{{Values: append([]sqltype.Value(nil), f.tempValues...)}}})
				next.SetEpoch(next.Epoch() + 1)
				f.target.Catalog.PublishLocked(next)
			case "CommitFrontier":
				f.target.Visibility.AdvanceCommitSeqLocked()
				f.target.Storage.InstallRelationLocked(f.temp, &storage.Relation{Versions: append(append([]storage.RowVersion(nil), f.target.Storage.Relations[f.temp].Versions...), storage.RowVersion{Values: append([]sqltype.Value(nil), f.tempValues...)})})
			case "OIDFrontier":
				if _, err := f.target.Catalog.AllocateOIDLocked(); err != nil {
					f.target.Mu.Unlock()
					t.Fatal(err)
				}
			}
			expectedState, expectedRoles, expectedFrontier := f.target.Catalog.State, f.target.RoleStore.Published, f.target.Visibility.CommitSeq()
			expectedStorage := cloneGeneratedRepairStorage(f.target.Storage.Relations)
			expectedSequences, expectedIndexes := f.target.Storage.Sequences, f.target.Storage.IndexRoots
			nextRow, nextVersion, nextRevision := f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier(), f.target.Catalog.NextRevision()
			expectedOID := f.target.Catalog.NextOID()
			f.target.Mu.Unlock()
			release.Do(func() { close(f.gate.release) })
			if err := <-result; !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("stale repair accepted", err)
			}
			if f.gate.calls != 1 {
				t.Fatal("repair retried provider", f.gate.calls)
			}
			if f.target.Catalog.State != expectedState || f.target.RoleStore.Published != expectedRoles || f.target.Visibility.CommitSeq() != expectedFrontier || !reflect.DeepEqual(f.target.Storage.Relations, expectedStorage) || !reflect.DeepEqual(f.target.Storage.Sequences, expectedSequences) || !reflect.DeepEqual(f.target.Storage.IndexRoots, expectedIndexes) || f.target.Storage.RowFrontier() != nextRow || f.target.Storage.VersionFrontier() != nextVersion || f.target.Catalog.NextRevision() != nextRevision || f.target.Catalog.NextOID() != expectedOID {
				t.Fatal("repair installed candidate or erased concurrent target change")
			}
			if _, exists := f.target.Catalog.State.Find(f.imported); exists {
				t.Fatal("imported catalog descriptor published")
			}
			if got, _ := f.source[1].OID(); got != 25000 {
				t.Fatal("source held row mutated", got)
			}
			if got, _ := f.tempValues[1].OID(); got != 25000 {
				t.Fatal("TEMP held row mutated", got)
			}
			if got, _ := f.storage[f.imported].Versions[0].Values[1].OID(); got != 26000 {
				t.Fatal("test never completed affected generation repair", got)
			}
			f.assertJoined(t)
		})
	}
}

func awaitGeneratedRepairEval(t *testing.T, source *generatedRepairSource, result <-chan error) {
	t.Helper()
	select {
	case <-source.entered:
	case err := <-result:
		t.Fatal("repair returned before completed-row CHECK Eval", err)
	}
}

func cloneGeneratedRepairStorage(source map[ids.ObjectAddress]*storage.Relation) map[ids.ObjectAddress]*storage.Relation {
	out := make(map[ids.ObjectAddress]*storage.Relation, len(source))
	for address, rows := range source {
		copy := *rows
		copy.Versions = append([]storage.RowVersion(nil), rows.Versions...)
		out[address] = &copy
	}
	return out
}

func TestGeneratedReplacementLateCancellation(t *testing.T) {
	for _, point := range []string{"AdmittedCheck", "CompletedCheck"} {
		t.Run(point, func(t *testing.T) {
			f := newGeneratedRepairFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if point == "CompletedCheck" {
				f.gate.onReturn = cancel
			}
			state, roles, frontier := f.target.Catalog.State, f.target.RoleStore.Published, f.target.Visibility.CommitSeq()
			storage := cloneGeneratedRepairStorage(f.target.Storage.Relations)
			sequences, indexes := f.target.Storage.Sequences, f.target.Storage.IndexRoots
			nextRow, nextVersion, nextRevision := f.target.Storage.RowFrontier(), f.target.Storage.VersionFrontier(), f.target.Catalog.NextRevision()
			nextOID := f.target.Catalog.NextOID()
			result := f.run(ctx)
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(f.gate.release) }) })
			awaitGeneratedRepairEval(t, f.gate, result)
			if point == "AdmittedCheck" {
				cancel()
			}
			release.Do(func() { close(f.gate.release) })
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatal("late cancellation accepted", err)
			}
			if f.gate.calls != 1 || f.target.Catalog.State != state || f.target.RoleStore.Published != roles || f.target.Visibility.CommitSeq() != frontier || !reflect.DeepEqual(f.target.Storage.Relations, storage) || !reflect.DeepEqual(f.target.Storage.Sequences, sequences) || !reflect.DeepEqual(f.target.Storage.IndexRoots, indexes) || f.target.Storage.RowFrontier() != nextRow || f.target.Storage.VersionFrontier() != nextVersion || f.target.Catalog.NextRevision() != nextRevision || f.target.Catalog.NextOID() != nextOID {
				t.Fatal("canceled repair published catalog/storage/index/sequence state")
			}
			if got, _ := f.source[1].OID(); got != 25000 {
				t.Fatal("canceled repair mutated source", got)
			}
			if got, _ := f.tempValues[1].OID(); got != 25000 {
				t.Fatal("canceled repair mutated retained TEMP", got)
			}
			f.assertJoined(t)
		})
	}
}
