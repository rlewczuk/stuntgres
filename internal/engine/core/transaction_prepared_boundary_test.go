package core_test

import (
	"context"
	"errors"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func preparedBoundaryName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
func preparedBoundaryInteger(value int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: value}}
}

const preparedBoundaryDeadline = 5 * time.Second

func preparedBoundaryReceive[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(preparedBoundaryDeadline):
		t.Fatal(description + " did not arrive before deadline")
		var zero T
		return zero
	}
}

func preparedBoundaryUpdate(t *testing.T, entry *session.PreparedTransaction, parent ids.ObjectAddress) (int, storage.RowVersion) {
	t.Helper()
	position := -1
	var update storage.RowVersion
	for i, row := range entry.Writes[parent] {
		if row.Base == 0 {
			continue
		}
		if position != -1 {
			t.Fatal("fixture must retain exactly one surviving UPDATE")
		}
		position, update = i, row
	}
	if position == -1 {
		t.Fatal("fixture omitted surviving UPDATE predecessor")
	}
	return position, update
}

// All roots in these tests come from admitted statements, not fabricated registry
// entries. The existing expression executor supplies an outside-metadata barrier.
func preparedBoundaryFixture(t *testing.T) (*core.Engine, *session.Session, *session.PreparedTransaction, ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	origin := sessionStart(t, e, nil)
	name := preparedBoundaryName("prepared_boundary")
	preparedUnitStatement(t, e, origin, &parser.CreateTable{Name: name, Elements: []parser.TableElement{
		{Column: &parser.Column{Name: "v", TypeName: parser.TypeName{Name: "int4"}}},
		{Column: &parser.Column{Name: "spare", TypeName: parser.TypeName{Name: "int4"}}},
	}})
	preparedUnitStatement(t, e, origin, &parser.Insert{Name: name, Rows: [][]parser.Expr{
		{preparedBoundaryInteger(1), preparedBoundaryInteger(10)},
		{preparedBoundaryInteger(2), preparedBoundaryInteger(20)},
	}})
	// Keep a genuine row-dependent derived key without a callable routine:
	// callable index outputs are already produced before PREPARE and completion
	// must consume them, not replay their providers merely to reach this barrier.
	// Searched CASE/null testing instead retains actual completion-time Eval,
	// with the same integer/null key values as the former v+0 fixture.
	key := parser.CaseExpr{
		Arms: []parser.CaseArm{{
			Condition: parser.PredicateExpr{Kind: parser.IsNotNull, Left: parser.ColumnExpr{Name: preparedBoundaryName("v")}},
			Result:    parser.ColumnExpr{Name: preparedBoundaryName("v")},
		}},
		Else: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}},
	}
	preparedUnitStatement(t, e, origin, &parser.CreateIndex{Name: parser.Identifier{Text: "prepared_boundary_index"}, Table: name, Method: "btree", Keys: []parser.IndexKey{{Expression: key}}})
	op := transactionOperation(t, origin)
	_, err := e.Begin(op, session.TxOptions{Isolation: session.Serializable})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	preparedUnitStatement(t, e, origin, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{"prepared-boundary"}})
	preparedUnitStatement(t, e, origin, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Local: true, Values: []string{"local-boundary"}})
	preparedUnitStatement(t, e, origin, &parser.Delete{Name: name, Where: parser.OperatorExpr{Name: preparedBoundaryName("="), Args: []parser.Expr{parser.ColumnExpr{Name: preparedBoundaryName("v")}, preparedBoundaryInteger(2)}}})
	preparedUnitStatement(t, e, origin, &parser.AlterTable{Name: name, Actions: []parser.AlterTableAction{{Column: parser.Identifier{Text: "spare"}, Action: parser.DropColumn}}})
	preparedUnitStatement(t, e, origin, &parser.Update{Name: name, Assignments: []parser.UpdateAssignment{{Target: parser.Identifier{Text: "v"}, Value: preparedBoundaryInteger(3)}}, Where: parser.OperatorExpr{Name: preparedBoundaryName("="), Args: []parser.Expr{parser.ColumnExpr{Name: preparedBoundaryName("v")}, preparedBoundaryInteger(1)}}})
	preparedUnitStatement(t, e, origin, &parser.Insert{Name: name, Rows: [][]parser.Expr{{preparedBoundaryInteger(7)}}})
	preparedUnitStatement(t, e, origin, &parser.CreateSequence{Name: preparedBoundaryName("prepared_boundary_sequence"), Options: []parser.SequenceOption{{Name: "start", Value: 41}}})
	e.Mu.Lock()
	beforeVisibility := e.Visibility.Entries[origin.Transactions.Ref]
	e.Mu.Unlock()
	if !beforeVisibility.Snapshot || beforeVisibility.XID == 0 {
		t.Fatal("real modifying Serializable owner did not advertise visibility")
	}
	if err := preparedUnitControl(t, e, origin, parser.KindPrepareTransaction, "boundary"); err != nil {
		t.Fatal(err)
	}
	entry := core.EnginePreparedRef(e).Lookup("boundary")
	if entry == nil || len(entry.Writes) == 0 || len(entry.Deletes) == 0 || entry.Catalog == nil || len(entry.Catalog.Storage) == 0 || len(entry.Catalog.Generators) == 0 || e.Visibility.Serializable.Nodes[entry.Ref] == nil || len(e.Locks.Inspect(entry.Ticket).Journals[entry.Ticket]) == 0 {
		t.Fatal("fixture did not retain populated row/delete/catalog/generator/SSI/lock roots")
	}
	e.Mu.Lock()
	preparedVisibility, retainedVisibility := e.Visibility.Entries[entry.Ref]
	e.Mu.Unlock()
	if !retainedVisibility || preparedVisibility.XID != beforeVisibility.XID || preparedVisibility.Snapshot || preparedVisibility.Xmin != 0 || preparedVisibility.SafeBuilder {
		t.Fatal("PREPARE must drop reader snapshot but retain detached writer visibility", preparedVisibility)
	}
	var parent ids.ObjectAddress
	for address := range entry.Writes {
		parent = address
	}
	_, update := preparedBoundaryUpdate(t, entry, parent)
	predecessor, _, found := storage.RowByID(e.Storage.Relations[parent].Versions, update.Row, -1)
	if !found || predecessor.Version != update.Base || update.Born != 0 {
		t.Fatal("fixture did not retain the genuine live UPDATE base/version")
	}
	origin.Settings.Mu.Lock()
	setting := origin.Settings.Value("application_name")
	origin.Settings.Mu.Unlock()
	if setting != "prepared-boundary" {
		t.Fatal("successful prepare did not commit SET and undo SET LOCAL", setting)
	}
	return e, origin, entry, parent
}

type preparedBoundarySnapshot struct {
	catalog         *catalog.State
	descriptors     map[ids.ObjectAddress]catalog.Descriptor
	names           map[ids.NameKey]ids.ObjectAddress
	counters        [10]uint64
	storagePointers map[ids.ObjectAddress]*storage.Relation
	storage         map[ids.ObjectAddress]storage.Relation
	generators      map[ids.ObjectAddress]*storage.SequenceGenerator
	generatorValues map[ids.ObjectAddress]storage.SequenceGenerator
	indexes         map[ids.ObjectAddress]*storage.IndexRoot
	indexValues     map[ids.ObjectAddress][]storage.IndexEntry
	registry        map[string]*session.PreparedTransaction
	entries         map[string]session.PreparedTransaction
	ssi             map[ids.TxRef]visibility.SerializableTx
	creators        map[ids.VersionID]ids.TxRef
	owners          map[ids.TxRef]*lock.Ticket
	resources       map[lock.Resource]struct{}
	locks           map[lock.Resource]lock.ResourceState
}

func preparedBoundaryCapture(e *core.Engine, entry *session.PreparedTransaction) preparedBoundarySnapshot {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	lockState := e.Locks.Inspect(entry.Ticket)
	s := preparedBoundarySnapshot{
		catalog: e.Catalog.State, descriptors: maps.Collect(e.Catalog.State.Descriptors()), names: maps.Collect(e.Catalog.State.Names()),
		counters:        [10]uint64{uint64(e.Catalog.State.Epoch()), e.Catalog.State.NamespaceGeneration(), uint64(e.Catalog.NextOID()), uint64(e.Catalog.NextRevision()), e.Storage.RowFrontier(), e.Storage.VersionFrontier(), uint64(e.Visibility.CommitSeq()), uint64(e.Visibility.DiagnosticTx()), e.Visibility.SerializableOrder(), e.RoleStore.NextPrincipal()},
		storagePointers: maps.Clone(e.Storage.Relations), storage: map[ids.ObjectAddress]storage.Relation{},
		generators: maps.Clone(e.Storage.Sequences), generatorValues: map[ids.ObjectAddress]storage.SequenceGenerator{},
		indexes: maps.Clone(e.Storage.IndexRoots), indexValues: map[ids.ObjectAddress][]storage.IndexEntry{},
		registry: maps.Collect(core.EnginePreparedRef(e).All()), entries: map[string]session.PreparedTransaction{},
		ssi: map[ids.TxRef]visibility.SerializableTx{}, creators: maps.Clone(e.Visibility.Serializable.Creators),
		owners: lockState.Owners, resources: lockState.Journals[entry.Ticket], locks: map[lock.Resource]lock.ResourceState{},
	}
	for address, store := range e.Storage.Relations {
		value := *store
		value.Versions = slices.Clone(store.Versions)
		for i := range value.Versions {
			value.Versions[i].Values = slices.Clone(value.Versions[i].Values)
		}
		s.storage[address] = value
	}
	for address, generator := range e.Storage.Sequences {
		s.generatorValues[address] = *generator
	}
	for address, root := range e.Storage.IndexRoots {
		values := slices.Clone(root.Entries)
		for i := range values {
			values[i].Keys = slices.Clone(values[i].Keys)
		}
		s.indexValues[address] = values
	}
	for gid, prepared := range core.EnginePreparedRef(e).All() {
		value := *prepared
		value.Writes = maps.Clone(prepared.Writes)
		for address, writes := range value.Writes {
			rows := slices.Clone(writes)
			for i := range rows {
				rows[i].Values = slices.Clone(rows[i].Values)
			}
			value.Writes[address] = rows
		}
		value.Deletes = maps.Clone(prepared.Deletes)
		for address, deletion := range value.Deletes {
			value.Deletes[address] = &visibility.PrivateDeletes{Rows: maps.Clone(deletion.Rows)}
		}
		value.Roots = maps.Clone(prepared.Roots)
		if prepared.Catalog != nil {
			catalogue := *prepared.Catalog
			catalogue.Originals, catalogue.Descriptors = maps.Clone(prepared.Catalog.Originals), maps.Clone(prepared.Catalog.Descriptors)
			catalogue.Names, catalogue.Storage = maps.Clone(prepared.Catalog.Names), maps.Clone(prepared.Catalog.Storage)
			catalogue.Generators = maps.Clone(prepared.Catalog.Generators)
			for address, generator := range catalogue.Generators {
				frozen := *generator
				catalogue.Generators[address] = &frozen
			}
			index.JournalIndexes(&catalogue).UniqueRoots = maps.Clone(index.JournalIndexesOrZero(prepared.Catalog).UniqueRoots)
			catalogue.Dependencies = slices.Clone(prepared.Catalog.Dependencies)
			value.Catalog = &catalogue
		}
		s.entries[gid] = value
	}
	for ref, node := range e.Visibility.Serializable.Nodes {
		value := *node
		value.Reads = maps.Clone(node.Reads)
		value.Writes = maps.Clone(node.Writes)
		for address, writes := range value.Writes {
			value.Writes[address] = maps.Clone(writes)
		}
		value.Incoming, value.Outgoing, value.Possible = maps.Clone(node.Incoming), maps.Clone(node.Outgoing), maps.Clone(node.Possible)
		s.ssi[ref] = value
	}
	for resource, lock := range lockState.Resources {
		s.locks[resource] = *lock
	}
	return s
}
func preparedBoundaryUnchanged(t *testing.T, e *core.Engine, entry *session.PreparedTransaction, before preparedBoundarySnapshot) {
	t.Helper()
	after := preparedBoundaryCapture(e, entry)
	if before.catalog != after.catalog || !reflect.DeepEqual(before, after) {
		t.Fatal("failed operation changed catalogue, counters, publication roots, generators, SSI, registry, or original holds")
	}
	for address, store := range before.storagePointers {
		if after.storagePointers[address] != store {
			t.Fatal("storage identity changed")
		}
	}
	for address, root := range before.indexes {
		if after.indexes[address] != root {
			t.Fatal("index identity changed")
		}
	}
	for address, generator := range before.generators {
		if after.generators[address] != generator {
			t.Fatal("generator identity changed")
		}
	}
}

func TestPreparedPopulatedReplaceCatalogGuards(t *testing.T) {
	e, _, entry, _ := preparedBoundaryFixture(t)
	peer := sessionStart(t, e, nil)
	for _, source := range []*core.Engine{e, engine.New()} {
		before := preparedBoundaryCapture(e, entry)
		changes, err := e.ReplaceCatalog(context.Background(), source, source.CatalogRead())
		if err == nil || !strings.Contains(err.Error(), "cannot replace catalog while prepared transactions exist") || changes.Replacement {
			t.Fatal(changes, err)
		}
		preparedBoundaryUnchanged(t, e, entry, before)
	}
	if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "boundary"); err != nil {
		t.Fatal(err)
	}
}

type preparedBoundaryExpression struct {
	expr.Expr
	once    sync.Once
	barrier func()
}

func (expression *preparedBoundaryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	value, err := expression.Expr.Eval(ctx)
	if err == nil {
		expression.once.Do(expression.barrier)
	}
	return value, err
}

func preparedBoundaryBarrier(t *testing.T, e *core.Engine, origin, finisher *session.Session, entry *session.PreparedTransaction) (<-chan struct{}, chan<- struct{}) {
	t.Helper()
	arrived, release := make(chan struct{}), make(chan struct{})
	for _, descriptor := range entry.Catalog.Cached.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.Name() != "prepared_boundary_index" {
			continue
		}
		derived := index.SQLIndex.Keys[0].Derived
		derived.Execution = &preparedBoundaryExpression{Expr: derived.Execution, barrier: func() {
			// Assert the blocking callback is outside every metadata mutex, including
			// the detached origin and finishing attachment. Never wait under a lock.
			for _, lock := range []*sync.Mutex{e.Mu, &origin.Mu, &origin.Transactions.Mu, &finisher.Mu, &finisher.Transactions.Mu} {
				if !lock.TryLock() {
					t.Error("publication expression executed under metadata mutex")
					close(arrived)
					return
				}
				lock.Unlock()
			}
			if e.Locks.MutexHeld() {
				t.Error("publication expression executed under the lock manager mutex")
				close(arrived)
				return
			}
			close(arrived)
			<-release
		}}
		return arrived, release
	}
	t.Fatal("missing real derived-index expression")
	return nil, nil
}

func TestPreparedPopulatedCompetingCompletionAndRetry(t *testing.T) {
	for _, veto := range []string{"cancel", "counter"} {
		t.Run(veto, func(t *testing.T) {
			e, origin, entry, parent := preparedBoundaryFixture(t)
			finisher, competitor := sessionStart(t, e, nil), sessionStart(t, e, nil)
			arrived, release := preparedBoundaryBarrier(t, e, origin, finisher, entry)
			if veto == "counter" {
				e.Mu.Lock()
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(math.MaxUint64))
				e.Mu.Unlock()
			}
			before := preparedBoundaryCapture(e, entry)
			updatePosition, originalUpdate := preparedBoundaryUpdate(t, entry, parent)
			originalHead := &e.Storage.Relations[parent].Versions[0]
			originalTicket := entry.Ticket
			ctx, cancel := context.WithCancel(context.Background())
			op, err := finisher.Acquire(ctx)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			result := make(chan error, 1)
			var releaseOnce sync.Once
			releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
			joined := false
			t.Cleanup(func() {
				cancel()
				releaseWorker()
				if !joined {
					select {
					case <-result:
						joined = true
					case <-time.After(preparedBoundaryDeadline):
						t.Error("completion worker cleanup did not join")
					}
				}
			})
			go func() { outcome := core.EngineCompletePrepared(e, op, "boundary", true); op.Done(); result <- outcome }()
			preparedBoundaryReceive(t, arrived, "outside-metadata completion barrier")
			busy := preparedUnitControl(t, e, competitor, parser.KindRollbackPrepared, "boundary")
			var diagnostic *sqlstate.Error
			if !errors.As(busy, &diagnostic) || diagnostic.Code != "55000" {
				t.Fatal("real competing claimant", busy)
			}
			if veto == "cancel" {
				cancel()
			}
			releaseWorker()
			failure := preparedBoundaryReceive(t, result, "completion veto outcome")
			joined = true
			if veto == "cancel" && !errors.Is(failure, context.Canceled) || veto == "counter" && (failure == nil || !strings.Contains(failure.Error(), "commit sequence exhausted")) {
				t.Fatal("final veto", failure)
			}
			preparedBoundaryUnchanged(t, e, entry, before)
			if !reflect.DeepEqual(entry.Writes[parent][updatePosition], originalUpdate) || &e.Storage.Relations[parent].Versions[0] != originalHead || entry.Ticket != originalTicket || e.Locks.TransactionTicket(entry.Ref) != originalTicket {
				t.Fatal("failed completion changed retained UPDATE/base/version, committed head or original ticket")
			}
			if veto == "counter" {
				e.Mu.Lock()
				e.Visibility.SetCommitSeqLocked(entry.Frontier)
				e.Mu.Unlock()
			}
			if err := preparedUnitControl(t, e, finisher, parser.KindCommitPrepared, "boundary"); err != nil {
				t.Fatal("retry", err)
			}
			preparedBoundaryPublished(t, e, entry, parent)
			completed := preparedBoundaryCapture(e, entry)
			missing := preparedUnitControl(t, e, competitor, parser.KindCommitPrepared, "boundary")
			if !errors.As(missing, &diagnostic) || diagnostic.Code != "42704" {
				t.Fatal("completion ran twice", missing)
			}
			preparedBoundaryUnchanged(t, e, entry, completed)
		})
	}
}

func preparedBoundaryPublished(t *testing.T, e *core.Engine, entry *session.PreparedTransaction, parent ids.ObjectAddress) {
	t.Helper()
	heap := accessMethodHeapOperationsUnit(t, e.CatalogRead())
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if core.EnginePreparedRef(e).Lookup(entry.GID) != nil || e.Visibility.CommitSeq() != entry.Frontier+1 {
		t.Fatal("completion did not publish exactly once")
	}
	relation := e.Catalog.State.MustRelation(parent)
	if len(relation.Columns) != 1 || relation.Columns[0].Name != "v" {
		t.Fatal("catalogue shape did not publish")
	}
	store := e.Storage.Relations[parent]
	selected, err := exec.SelectQuerySource(context.Background(), &exec.Source{Committed: store.Versions}, e.Visibility.CommitSeq(), store.Ends, nil, heap)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	if selected.Count() != 2 {
		t.Fatal("UPDATE/insert/delete roots did not publish", selected.Count())
	}
	for i, expected := range []int64{3, 7} {
		row := selected.VersionAt(selected.RawIndex(i))
		value, err := row.Values[0].Integer()
		if err != nil || value != expected {
			t.Fatal("staged coupled UPDATE/insert outcome", i, value, err)
		}
		if i == 0 {
			_, update := preparedBoundaryUpdate(t, entry, parent)
			if row.Row != update.Row || row.Version != update.Version || row.Base != 0 || row.Born != e.Visibility.CommitSeq() {
				t.Fatal("surviving UPDATE did not publish its retained identity exactly once")
			}
		}
	}
	found := false
	for address, descriptor := range e.Catalog.State.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent {
			root := e.Storage.IndexRoots[address]
			if root == nil || len(root.Entries) != 2 {
				t.Fatal("coupled UPDATE/insert index root did not publish")
			}
			for i, expected := range []int64{3, 7} {
				key, err := root.Entries[i].Keys[0].Integer()
				if err != nil || key != expected {
					t.Fatal("coupled UPDATE/insert index key", i, key, err)
				}
			}
			found = true
		}
	}
	if !found {
		t.Fatal("substantive index disappeared")
	}
	for address, generator := range entry.Catalog.Generators {
		if e.Storage.Sequences[address] != generator || generator.Last != 41 || generator.Called {
			t.Fatal("private generator did not transfer unchanged")
		}
	}
	lockState := e.Locks.Inspect(entry.Ticket)
	if lockState.Owners[entry.Ref] != nil || len(lockState.Journals[entry.Ticket]) != 0 {
		t.Fatal("original physical ticket was not released")
	}
}

func TestPreparedCompetingGIDDisposesWholeLoser(t *testing.T) {
	e := engine.New()
	serializableUnitRelation(t, e)
	owners := []*session.Session{sessionStart(t, e, nil), sessionStart(t, e, nil)}
	refs := make([]ids.TxRef, 2)
	tickets := make([]*lock.Ticket, 2)
	for i, owner := range owners {
		op := transactionOperation(t, owner)
		var err error
		refs[i], err = e.Begin(op, session.TxOptions{Isolation: session.Serializable})
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		tickets[i] = owner.Transactions.Ticket
		preparedUnitStatement(t, e, owner, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Values: []string{"winner-setting"}})
		preparedUnitStatement(t, e, owner, &parser.Set{Name: parser.Identifier{Text: "application_name"}, Local: true, Values: []string{"local-setting"}})
		preparedUnitStatement(t, e, owner, &parser.Insert{Name: preparedBoundaryName("ssi_unit"), Rows: [][]parser.Expr{{preparedBoundaryInteger(int64(i + 1))}}})
		preparedUnitStatement(t, e, owner, &parser.CreateSequence{Name: preparedBoundaryName([]string{"gid_first", "gid_second"}[i])})
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	type outcome struct {
		owner int
		err   error
	}
	outcomes := make(chan outcome, 2)
	ctx, cancel := context.WithCancel(context.Background())
	var startOnce sync.Once
	releaseStart := func() { startOnce.Do(func() { close(start) }) }
	joined := 0
	t.Cleanup(func() {
		cancel()
		releaseStart()
		for joined < len(owners) {
			select {
			case <-outcomes:
				joined++
			case <-time.After(preparedBoundaryDeadline):
				t.Error("competing PREPARE worker cleanup did not join")
				return
			}
		}
	})
	for i, owner := range owners {
		go func(i int, owner *session.Session) {
			op, err := owner.Acquire(ctx)
			if err != nil {
				outcomes <- outcome{i, err}
				ready <- struct{}{}
				return
			}
			scope, err := e.StartStatement(op)
			ready <- struct{}{}
			<-start // admission barrier is outside metadata locks and BEFORE PREPARE.
			if err == nil {
				_, err = scope.Execute(&parser.TransactionControl{Kind: parser.KindPrepareTransaction, GID: "race"}, 0, nil)
				err = scope.Finish(err)
			}
			op.Done()
			outcomes <- outcome{i, err}
		}(i, owner)
	}
	preparedBoundaryReceive(t, ready, "first PREPARE admission")
	preparedBoundaryReceive(t, ready, "second PREPARE admission")
	releaseStart()
	first := preparedBoundaryReceive(t, outcomes, "first competing PREPARE outcome")
	joined++
	second := preparedBoundaryReceive(t, outcomes, "second competing PREPARE outcome")
	joined++
	winner, loser := first, second
	if winner.err != nil {
		winner, loser = loser, winner
	}
	var diagnostic *sqlstate.Error
	if winner.err != nil || !errors.As(loser.err, &diagnostic) || diagnostic.Code != "42710" {
		t.Fatal("genuine competing PREPARE outcomes", first.err, second.err)
	}
	entry := core.EnginePreparedRef(e).Lookup("race")
	if core.EnginePreparedRef(e).Len() != 1 || entry.Ref != refs[winner.owner] || entry.Ticket != tickets[winner.owner] || len(entry.Writes) == 0 || entry.Catalog == nil {
		t.Fatal("wrong registry winner")
	}
	for i, owner := range owners {
		if owner.TransactionState() != session.TxIdle || owner.Transactions.Ticket != nil || owner.Transactions.Catalog != nil || len(owner.Transactions.Writes) != 0 || len(owner.Transactions.Deletes) != 0 || len(owner.Transactions.Roots) != 0 {
			t.Fatal("whole transaction did not detach/end", i)
		}
		owner.Settings.Mu.Lock()
		value := owner.Settings.Value("application_name")
		owner.Settings.Mu.Unlock()
		expected := ""
		if i == winner.owner {
			expected = "winner-setting"
		}
		if value != expected {
			t.Fatal("winner SET commit / loser whole SET rollback / SET LOCAL undo", i, value, expected)
		}
	}
	if e.Locks.TransactionTicket(refs[loser.owner]) != nil || len(e.Locks.Inspect(tickets[loser.owner]).Journals[tickets[loser.owner]]) != 0 || e.Locks.TransactionTicket(refs[winner.owner]) != entry.Ticket {
		t.Fatal("loser released winner or retained its own holds")
	}
	if node := e.Visibility.Serializable.Nodes[refs[loser.owner]]; node != nil && (!node.Aborted || !node.Ended || node.Prepared) {
		t.Fatal("loser SSI incarnation survived")
	}
	if err := preparedUnitControl(t, e, owners[loser.owner], parser.KindCommitPrepared, "race"); err != nil {
		t.Fatal(err)
	}
	if e.Locks.TransactionTicket(refs[winner.owner]) != nil {
		t.Fatal("winner resources survived outcome")
	}
	heap := accessMethodHeapOperationsUnit(t, e.CatalogRead())
	for address := range entry.Writes {
		store := e.Storage.Relations[address]
		selected, err := exec.SelectQuerySource(context.Background(), &exec.Source{Committed: store.Versions}, e.Visibility.CommitSeq(), store.Ends, nil, heap)
		if err != nil {
			t.Fatal(err)
		}
		if selected.Count() != 1 {
			selected.Close()
			t.Fatal("losing GID row survived")
		}
		value, err := selected.VersionAt(selected.RawIndex(0)).Values[0].Integer()
		selected.Close()
		if err != nil || value != int64(winner.owner+1) {
			t.Fatal("winning GID row outcome", value, err)
		}
	}
	for address, generator := range entry.Catalog.Generators {
		if e.Storage.Sequences[address] != generator {
			t.Fatal("winner generator did not publish")
		}
	}
	losingName := []string{"gid_first", "gid_second"}[loser.owner]
	for _, descriptor := range e.Catalog.State.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Name() == losingName {
			t.Fatal("losing GID catalogue/generator survived")
		}
	}
}

func TestPreparedLateCancellationAndWaiterObservePublication(t *testing.T) {
	e, _, entry, parent := preparedBoundaryFixture(t)
	finisher, waiter := sessionStart(t, e, nil), sessionStart(t, e, nil)
	waiterRef := transactionBegin(t, waiter)
	var resource lock.Resource
	found := false
	for held := range e.Locks.Inspect(entry.Ticket).Journals[entry.Ticket] {
		if held.Row == 0 && held.Key() == nil && held.Address == parent {
			resource, found = held, true
			break
		}
	}
	if !found {
		t.Fatal("missing genuine held table resource")
	}
	waitContext, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	request, err := e.Locks.StartAcquire(waitContext, waiter.Transactions.Ticket, resource, lock.AccessShare, lock.Policy{})
	if err != nil || request == nil {
		t.Fatal("waiter was not queued behind prepared table hold", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan struct{})
	result := make(chan error, 1)
	waiterJoined, completionJoined, completionLaunched := false, false, false
	t.Cleanup(func() {
		cancel()
		cancelWaiter()
		if completionLaunched && !completionJoined {
			select {
			case <-result:
				completionJoined = true
			case <-time.After(preparedBoundaryDeadline):
				t.Error("late-cancellation completion worker cleanup did not join")
			}
		}
		if !waiterJoined {
			select {
			case <-observed:
				waiterJoined = true
			case <-time.After(preparedBoundaryDeadline):
				t.Error("resource waiter cleanup did not join")
			}
		}
	})
	go func() {
		defer close(observed)
		if err := request.Wait(); err != nil {
			if waitContext.Err() == nil {
				t.Error(err)
			}
		} else {
			// The original ticket wakes this real waiter only after the catalogue,
			// UPDATE/insert/delete/index/generator and registry outcome linearize.
			preparedBoundaryPublished(t, e, entry, parent)
			cancel()
		}
	}()
	op, err := finisher.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	completionLaunched = true
	go func() { outcome := core.EngineCompletePrepared(e, op, "boundary", true); op.Done(); result <- outcome }()
	outcome := preparedBoundaryReceive(t, result, "late-cancellation completion outcome")
	completionJoined = true
	preparedBoundaryReceive(t, observed, "published-state resource observer")
	waiterJoined = true
	if outcome != nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("late cancellation retracted installed outcome", outcome, ctx.Err())
	}
	preparedBoundaryPublished(t, e, entry, parent)
	transactionEnd(t, waiter, waiterRef, false)
}

func TestPreparedValidationOmitsInstallationRoots(t *testing.T) {
	e, _, entry, parent := preparedBoundaryFixture(t)
	peer := sessionStart(t, e, nil)
	before := preparedBoundaryCapture(e, entry)
	original := e.Storage.Relations[parent]
	store := e.PrivateStorageLocked(entry.Catalog, parent)
	publication := index.RowPublication{
		Address: parent, Store: store, Original: original,
		OriginalRelation: e.Catalog.State.MustRelation(parent),
		Relation:         entry.Catalog.Cached.MustRelation(parent),
		Root:             store.Versions, Writes: entry.Writes[parent], Ends: store.Ends,
		Deletion: entry.Deletes[parent], Generation: store.Generation, SingleSlot: -1,
	}
	updatePosition, update := preparedBoundaryUpdate(t, entry, parent)
	predecessor, predecessorSlot, found := storage.RowByID(publication.Root, update.Row, -1)
	if !found || predecessor.Version != update.Base || !visibility.LiveVersion(predecessor, entry.Frontier, publication.Ends, nil) {
		t.Fatal("structural fixture omitted a live genuine UPDATE predecessor")
	}
	if err := index.PrepareRowPublication(&publication, e.Catalog.Domain, entry.Ref, false); err != nil {
		t.Fatal(err)
	}
	if publication.Next != nil || publication.NextEnds != nil || publication.Slots != nil || publication.SingleSlot != -1 {
		t.Fatal("validation-only PREPARE materialized installable row/deletion/slot roots")
	}
	stores := index.CaptureIndexStores(e.Storage.Relations, e.Catalog.State)
	rebuild := map[ids.ObjectAddress]bool{}
	for address, change := range entry.Catalog.Storage {
		stores[address], rebuild[address] = change.Replacement, true
	}
	roots, err := core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, e.Catalog.State, entry.Catalog.Cached.State(), stores, e.Storage.IndexRoots, rebuild, nil, []index.RowPublication{publication}, false, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || roots != nil {
		t.Fatal("validation-only PREPARE materialized installable index map/root", roots, err)
	}
	preparedBoundaryUnchanged(t, e, entry, before)
	materialized := publication
	if err := index.PrepareRowPublication(&materialized, e.Catalog.Domain, entry.Ref, true); err != nil {
		t.Fatal(err)
	}
	if materialized.Next == nil || materialized.NextEnds == nil || len(materialized.Slots) != len(publication.Writes) || materialized.Slots[updatePosition] != predecessorSlot || &materialized.Next[0] == &publication.Root[0] {
		t.Fatal("completion omitted separate UPDATE replacement heads/deletion/slot installation roots")
	}
	roots, err = core.EnginePrepareIndexRoots(e, &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, e.Catalog.State, entry.Catalog.Cached.State(), stores, e.Storage.IndexRoots, rebuild, nil, []index.RowPublication{materialized}, true, sqltype.OutputEnvironment{}, nil, nil, nil)
	if err != nil || len(roots) == 0 {
		t.Fatal("completion omitted substantive index installation roots", err)
	}
	invalid := publication
	invalid.Writes = slices.Clone(publication.Writes)
	invalid.Writes[0].Born = 1
	validationError := index.PrepareRowPublication(&invalid, e.Catalog.Domain, entry.Ref, false)
	installationError := index.PrepareRowPublication(&invalid, e.Catalog.Domain, entry.Ref, true)
	if validationError == nil || validationError.Error() != "stuntgres: invalid private row version" || installationError == nil || installationError.Error() != "stuntgres: invalid private row version" {
		t.Fatal("phase switch weakened canonical predecessor validation", validationError, installationError)
	}
	for _, staleKind := range []string{"wrong_base_version", "ended_predecessor"} {
		for _, materialize := range []bool{false, true} {
			stale := publication
			stale.Writes = slices.Clone(publication.Writes)
			if staleKind == "wrong_base_version" {
				stale.Writes[updatePosition].Base = ids.VersionID(^uint64(0))
			} else {
				stale.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{update.Row: {Version: update.Base, End: entry.Frontier}}}
			}
			err := index.PrepareRowPublication(&stale, e.Catalog.Domain, entry.Ref, materialize)
			if err == nil || err.Error() != "stuntgres: stale update predecessor" {
				t.Fatal("phase switch failed to reject stale UPDATE predecessor", staleKind, materialize, err)
			}
			if !materialize && (stale.Next != nil || stale.NextEnds != nil || stale.Slots != nil || stale.SingleSlot != -1) {
				t.Fatal("rejected validation-only UPDATE allocated replacement heads or slots", staleKind)
			}
			preparedBoundaryUnchanged(t, e, entry, before)
		}
	}
	preparedBoundaryUnchanged(t, e, entry, before)
	if err := preparedUnitControl(t, e, peer, parser.KindRollbackPrepared, "boundary"); err != nil {
		t.Fatal(err)
	}
}
