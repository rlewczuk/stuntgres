package engine

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/functions"
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
	next       *catalogState
	storage    map[ObjectAddress]*relationStorage
	sequences  map[ObjectAddress]*sequenceGenerator
	mapping    map[ObjectAddress]ObjectAddress
	imported   ObjectAddress
	temp       ObjectAddress
	source     []sqltype.Value
	tempValues []sqltype.Value
	gate       *generatedRepairSource
}

func newGeneratedRepairFixture(t *testing.T) *generatedRepairFixture {
	t.Helper()
	e, view, d := generatedUnitDescriptor(t)
	d.owner = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	oldAddress := d.address
	d.address.ObjectID = 26000
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	floating, _ := view.LookupType(sqltype.Float8)
	d.columns[1].Usage = oid.Usage()
	ref := ObjectRef{owner: e.owner, Epoch: view.Epoch() + 1, Address: d.address}
	leaf := tableOIDExpression{expressionInfo{usage: oid.Usage(), properties: ExprProperties{RowDependent: true}}, ref}
	d.generations = map[int32]columnGeneration{2: {kind: generationStored, expression: leaf, planned: leaf}}
	// CHECK consumes the repaired cell as well as the scoped provider. A stale
	// source tableoid vetoes the row instead of reaching the final stale fence.
	probe := functions.Random[BoundExpr, *EvalContext, ExprProperties]{Info: functionInfo(expressionInfo{usage: floating.Usage(), properties: ExprProperties{Volatility: Volatile}}), Name: "random"}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	expectedOID, err := sqltype.OIDValue(oid, uint32(d.address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	columnRef := ref
	columnRef.Address.SubID = 2
	repairedCell := columnExpression{expressionInfo: expressionInfo{usage: oid.Usage(), properties: ExprProperties{RowDependent: true}}, origin: columnRef, scope: 1, frame: 1, slot: 1}
	equal := comparisonExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, left: repairedCell, right: literalExpression{expressionInfo{usage: oid.Usage()}, expectedOID}, predicate: predicate, operator: "="}
	scoped := nullTestExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, child: probe, not: true}
	d.checkConstraints = []checkConstraint{{Name: "scoped_gate", expression: logicalExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, op: parser.LogicalAnd, left: equal, right: scoped}}}
	base, err := sqltype.Integer(d.columns[0].Usage.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	oldOID, err := sqltype.OIDValue(oid, uint32(oldAddress.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	source := []sqltype.Value{base, oldOID}
	next := cloneCatalog(view.state)
	next.epoch++
	next.descriptors[d.address] = d
	next.names[NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: d.name}] = d.address
	// Retain a real TEMP namespace and row vector in the target generation.
	tempNamespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: 27000}
	tempAddress := ObjectAddress{ClassID: RelationClassID, ObjectID: 27001}
	temp := *d
	temp.address, temp.namespace, temp.name = tempAddress, tempNamespace, "retained"
	temp.generations, temp.checkConstraints = nil, nil
	tempValues := append([]sqltype.Value(nil), source...)
	e.state = cloneCatalog(e.state)
	e.state.descriptors[tempNamespace] = &namespaceDescriptor{address: tempNamespace, name: "pg_temp_1", owner: d.owner, revision: 1, temporary: SessionToken{owner: e, id: 99}}
	e.state.descriptors[tempAddress] = &temp
	e.storage[tempAddress] = &relationStorage{versions: []rowVersion{{values: tempValues}}}
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
	f := &generatedRepairFixture{target: e, stage: stage, session: session, op: op, next: next, storage: map[ObjectAddress]*relationStorage{d.address: {versions: []rowVersion{{values: source}}}}, sequences: map[ObjectAddress]*sequenceGenerator{{ClassID: RelationClassID, ObjectID: 28000}: {last: 77, called: true}}, mapping: map[ObjectAddress]ObjectAddress{oldAddress: d.address}, imported: d.address, temp: tempAddress, source: source, tempValues: tempValues, gate: gate}
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
		err := f.target.repairRemappedGenerationsLocked(ctx, f.next, f.storage, f.sequences, f.mapping, f.op, nil)
		if err == nil {
			var prepared remintedIndexStorage
			prepared, err = f.target.prepareForeignIndexStorage(ctx, f.next, f.storage, &indexSnapshot{})
			if err == nil {
				err = f.target.installForeignCatalogLocked(ctx, f.next, f.storage, f.sequences, f.target.nextRevision, prepared)
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
				f.target.state = cloneCatalog(f.target.state)
				f.target.state.epoch++
				f.target.state.namespaceGeneration++
			case "Roles":
				roles := *f.target.roles
				roles.revision++
				f.target.roles = &roles
			case "Temporary":
				next := cloneCatalog(f.target.state)
				copy := *next.descriptors[f.temp].(*relationDescriptor)
				copy.address.ObjectID++
				copy.name = "concurrent_temp"
				next.descriptors[copy.address] = &copy
				f.target.storage[copy.address] = &relationStorage{versions: []rowVersion{{values: append([]sqltype.Value(nil), f.tempValues...)}}}
				next.epoch++
				f.target.state = next
			case "CommitFrontier":
				f.target.commitSeq++
				f.target.storage[f.temp] = &relationStorage{versions: append(append([]rowVersion(nil), f.target.storage[f.temp].versions...), rowVersion{values: append([]sqltype.Value(nil), f.tempValues...)})}
			}
			expectedState, expectedRoles, expectedFrontier := f.target.state, f.target.roles, f.target.commitSeq
			expectedStorage := cloneGeneratedRepairStorage(f.target.storage)
			expectedSequences, expectedIndexes := f.target.sequences, f.target.indexRoots
			nextRow, nextVersion, nextRevision := f.target.nextRow, f.target.nextVersion, f.target.nextRevision
			f.target.mu.Unlock()
			release.Do(func() { close(f.gate.release) })
			if err := <-result; !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal("stale repair accepted", err)
			}
			if f.gate.calls != 1 {
				t.Fatal("repair retried provider", f.gate.calls)
			}
			if f.target.state != expectedState || f.target.roles != expectedRoles || f.target.commitSeq != expectedFrontier || !reflect.DeepEqual(f.target.storage, expectedStorage) || !reflect.DeepEqual(f.target.sequences, expectedSequences) || !reflect.DeepEqual(f.target.indexRoots, expectedIndexes) || f.target.nextRow != nextRow || f.target.nextVersion != nextVersion || f.target.nextRevision != nextRevision {
				t.Fatal("repair installed candidate or erased concurrent target change")
			}
			if _, exists := f.target.state.descriptors[f.imported]; exists {
				t.Fatal("imported catalog descriptor published")
			}
			if got, _ := f.source[1].OID(); got != 25000 {
				t.Fatal("source held row mutated", got)
			}
			if got, _ := f.tempValues[1].OID(); got != 25000 {
				t.Fatal("TEMP held row mutated", got)
			}
			if got, _ := f.storage[f.imported].versions[0].values[1].OID(); got != 26000 {
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

func cloneGeneratedRepairStorage(source map[ObjectAddress]*relationStorage) map[ObjectAddress]*relationStorage {
	out := make(map[ObjectAddress]*relationStorage, len(source))
	for address, rows := range source {
		copy := *rows
		copy.versions = append([]rowVersion(nil), rows.versions...)
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
			state, roles, frontier := f.target.state, f.target.roles, f.target.commitSeq
			storage := cloneGeneratedRepairStorage(f.target.storage)
			sequences, indexes := f.target.sequences, f.target.indexRoots
			nextRow, nextVersion, nextRevision := f.target.nextRow, f.target.nextVersion, f.target.nextRevision
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
			if f.gate.calls != 1 || f.target.state != state || f.target.roles != roles || f.target.commitSeq != frontier || !reflect.DeepEqual(f.target.storage, storage) || !reflect.DeepEqual(f.target.sequences, sequences) || !reflect.DeepEqual(f.target.indexRoots, indexes) || f.target.nextRow != nextRow || f.target.nextVersion != nextVersion || f.target.nextRevision != nextRevision {
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
