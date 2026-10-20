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
	"stuntgres/internal/engine/plan"
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
	session    *Session
	op         *Operation
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
	d.Owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	oldAddress := d.Address()
	d.ObjectAddress.ObjectID = 26000
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	floating, _ := view.LookupType(sqltype.Float8)
	d.Columns[1].Usage = oid.Usage()
	ref := e.catalog.Domain.Ref(view.Epoch()+1, d.Address())
	leaf := plan.TableOIDExpr{expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, ref}
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
	equal := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: repairedCell, Right: plan.LiteralExpr{expr.Info{Usage: oid.Usage()}, expectedOID}, Predicate: predicate, Operator: "="}
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
	e.catalog.state = catalog.CloneState(e.catalog.state)
	e.catalog.state.SetDescriptor(tempNamespace, &catalog.Namespace{ObjectAddress: tempNamespace, ObjectName: "pg_temp_1", Owner: d.Owner, ObjectRevision: 1, Temporary: e.catalog.Domain.Session(99)})
	e.catalog.state.SetDescriptor(tempAddress, &temp)
	e.mu.Lock()
	e.storage.InstallRelationLocked(tempAddress, &storage.Relation{Versions: []storage.RowVersion{{Values: tempValues}}})
	e.mu.Unlock()
	stage := e.restoreStage()
	session, err := stage.StartSession(context.Background(), SessionStart{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	op, err := session.Acquire(context.Background())
	if err != nil {
		session.RequestEnd(CleanClose)
		_ = session.WaitEnd(context.Background())
		t.Fatal(err)
	}
	gate := &generatedRepairSource{entered: make(chan struct{}), release: make(chan struct{})}
	session.random.rand = *rand.New(gate)
	session.random.initialized = true
	f := &generatedRepairFixture{target: e, stage: stage, session: session, op: op, next: next, storage: map[ids.ObjectAddress]*storage.Relation{d.Address(): {Versions: []storage.RowVersion{{Values: source}}}}, sequences: map[ids.ObjectAddress]*storage.SequenceGenerator{{ClassID: ids.RelationClassID, ObjectID: 28000}: {Last: 77, Called: true}}, mapping: map[ids.ObjectAddress]ids.ObjectAddress{oldAddress: d.Address()}, imported: d.Address(), temp: tempAddress, source: source, tempValues: tempValues, gate: gate}
	t.Cleanup(func() {
		op.Done()
		session.RequestEnd(CleanClose)
		if err := session.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *generatedRepairFixture) run(ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() {
		f.target.replacementMu.Lock()
		defer f.target.replacementMu.Unlock()
		f.target.mu.Lock()
		defer f.target.mu.Unlock()
		_, err := f.target.repairRemappedGenerationsLocked(ctx, f.next, f.storage, f.sequences, f.mapping, f.op, nil)
		if err == nil {
			var prepared remintedIndexStorage
			prepared, err = f.target.prepareForeignIndexStorage(ctx, f.next, f.storage, &indexSnapshot{})
			if err == nil {
				err = f.target.installForeignCatalogLocked(ctx, f.next, f.storage, f.sequences, f.target.catalog.nextRevision, prepared)
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
	f.session.RequestEnd(CleanClose)
	if err := f.session.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.session.mu.Lock()
	defer f.session.mu.Unlock()
	if f.session.state != ended || f.session.leases != 0 || len(f.session.operations) != 0 {
		t.Fatal("session cleanup did not join operations", f.session.state, f.session.leases, len(f.session.operations))
	}
}

func TestGeneratedReplacementStaleAuthority(t *testing.T) {
	for _, change := range []string{"Catalog", "Roles", "Temporary", "CommitFrontier"} {
		t.Run(change, func(t *testing.T) {
			f := newGeneratedRepairFixture(t)
			result := f.run(context.Background())
			var release sync.Once
			t.Cleanup(func() { release.Do(func() { close(f.gate.release) }) })
			awaitGeneratedRepairEval(t, f.gate, result)
			f.target.mu.Lock() // actual Eval must not retain the target mutex
			switch change {
			case "Catalog":
				f.target.catalog.state = catalog.CloneState(f.target.catalog.state)
				f.target.catalog.state.SetEpoch(f.target.catalog.state.Epoch() + 1)
				f.target.catalog.state.SetNamespaceGeneration(f.target.catalog.state.NamespaceGeneration() + 1)
			case "Roles":
				roles := *f.target.roles.published
				roles.Revision++
				f.target.roles.published = &roles
			case "Temporary":
				next := catalog.CloneState(f.target.catalog.state)
				copy := *next.MustRelation(f.temp)
				copy.ObjectAddress.ObjectID++
				copy.ObjectName = "concurrent_temp"
				next.SetDescriptor(copy.Address(), &copy)
				f.target.storage.InstallRelationLocked(copy.Address(), &storage.Relation{Versions: []storage.RowVersion{{Values: append([]sqltype.Value(nil), f.tempValues...)}}})
				next.SetEpoch(next.Epoch() + 1)
				f.target.catalog.state = next
			case "CommitFrontier":
				f.target.visibility.AdvanceCommitSeqLocked()
				f.target.storage.InstallRelationLocked(f.temp, &storage.Relation{Versions: append(append([]storage.RowVersion(nil), f.target.storage.Relations[f.temp].Versions...), storage.RowVersion{Values: append([]sqltype.Value(nil), f.tempValues...)})})
			}
			expectedState, expectedRoles, expectedFrontier := f.target.catalog.state, f.target.roles.published, f.target.visibility.CommitSeq()
			expectedStorage := cloneGeneratedRepairStorage(f.target.storage.Relations)
			expectedSequences, expectedIndexes := f.target.storage.Sequences, f.target.storage.IndexRoots
			nextRow, nextVersion, nextRevision := f.target.storage.RowFrontier(), f.target.storage.VersionFrontier(), f.target.catalog.nextRevision
			f.target.mu.Unlock()
			release.Do(func() { close(f.gate.release) })
			if err := <-result; !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("stale repair accepted", err)
			}
			if f.gate.calls != 1 {
				t.Fatal("repair retried provider", f.gate.calls)
			}
			if f.target.catalog.state != expectedState || f.target.roles.published != expectedRoles || f.target.visibility.CommitSeq() != expectedFrontier || !reflect.DeepEqual(f.target.storage.Relations, expectedStorage) || !reflect.DeepEqual(f.target.storage.Sequences, expectedSequences) || !reflect.DeepEqual(f.target.storage.IndexRoots, expectedIndexes) || f.target.storage.RowFrontier() != nextRow || f.target.storage.VersionFrontier() != nextVersion || f.target.catalog.nextRevision != nextRevision {
				t.Fatal("repair installed candidate or erased concurrent target change")
			}
			if _, exists := f.target.catalog.state.Find(f.imported); exists {
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
			state, roles, frontier := f.target.catalog.state, f.target.roles.published, f.target.visibility.CommitSeq()
			storage := cloneGeneratedRepairStorage(f.target.storage.Relations)
			sequences, indexes := f.target.storage.Sequences, f.target.storage.IndexRoots
			nextRow, nextVersion, nextRevision := f.target.storage.RowFrontier(), f.target.storage.VersionFrontier(), f.target.catalog.nextRevision
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
			if f.gate.calls != 1 || f.target.catalog.state != state || f.target.roles.published != roles || f.target.visibility.CommitSeq() != frontier || !reflect.DeepEqual(f.target.storage.Relations, storage) || !reflect.DeepEqual(f.target.storage.Sequences, sequences) || !reflect.DeepEqual(f.target.storage.IndexRoots, indexes) || f.target.storage.RowFrontier() != nextRow || f.target.storage.VersionFrontier() != nextVersion || f.target.catalog.nextRevision != nextRevision {
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
