package core_test

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func TestTruncateReplayPrivateGraphVeto(t *testing.T) {
	for _, kind := range []string{"foreign_key", "inheritance"} {
		t.Run(kind, func(t *testing.T) {
			e, s, p, q := truncateUnitFixture(t)
			oldP, oldQ := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address]
			tx := enginetest.TransactionBegin(t, s)
			enginetest.SessionSQL(t, s, "CREATE TABLE private_before(id INT)")
			truncateReplayRetain(t, e, s, false, "p")
			enginetest.SessionSQL(t, s, "SAVEPOINT edge_prefix")
			parent := s.Transactions.Catalog
			root := parent.Storage[p.Address].Replacement
			query := "CREATE TABLE late_child(id INT REFERENCES p)"
			if kind == "inheritance" {
				query = "CREATE TABLE late_child() INHERITS(p)"
			}
			// The CREATE executor translates every publication Conflict to
			// duplicate-relation. Exercise its real stage/provider/publication
			// path before that SQL diagnostic translation instead.
			err := truncateReplayPrivateCreate(t, e, s, query)
			if err != (catalog.Conflict{}) {
				t.Fatal("later private incoming edge escaped retained graph", err)
			}
			if s.Transactions.Catalog != parent || parent.Storage[p.Address].Replacement != root || e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || len(oldP.Versions) != 2 || oldP.TruncateLineage != nil {
				t.Fatal("failed private graph change published or mutated admitted prefix")
			}
			enginetest.TransactionEnd(t, s, tx, false)
		})
	}
}

func truncateReplayPrivateCreate(t *testing.T, e *core.Engine, s *session.Session, query string) error {
	t.Helper()
	statement := enginetest.Statement(t, query).(*parser.CreateTable)
	scope, op := truncateUnitScope(t, e, s, context.Background())
	if err := scope.Gate(statement); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireRelation(statement, true); err != nil {
		t.Fatal(err)
	}
	stage, err := func() (*core.CatalogStage, error) {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			return nil, err
		}
		changes := scope.PendingCatalog().Clone()
		namespace, err := e.RelationCreationNamespaceLocked(auth, statement.Name, "logged", changes)
		if err != nil {
			return nil, err
		}
		auth.Catalog = e.PrivateCatalogLocked(changes)
		auth.Recapture(auth.Catalog)
		if err := auth.CheckCreate(namespace); err != nil {
			return nil, err
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.CreationName = statement.Name
		stage.AuthorizeLocked(auth, s)
		var targets []ids.ObjectRef
		if scope.ForeignKeysSlot != nil {
			targets = scope.ForeignKeysSlot.KeyTargets
		}
		err = tablecmds.SchemaTableLocked(e, op, auth, stage, statement, namespace.Address, auth.State.CurrentUser, scope.Input, targets)
		return stage, err
	}()
	if stage != nil {
		defer e.DiscardCatalogStage(stage)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := tablecmds.AcquireTypedSources(scope, stage, false); err != nil {
		t.Fatal(err)
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
	finished := scope.Finish(err)
	op.Done()
	return finished
}

func TestTruncateReplayHiddenHostPredecessor(t *testing.T) {
	e, s, p, q := truncateUnitFixture(t)
	enginetest.SessionSQL(t, s, "CREATE TABLE c(id INT)")
	child := roleTableRef(t, e, "c")
	oldP, oldQ := e.Storage.Relations[p.Address], e.Storage.Relations[q.Address]
	tx := enginetest.TransactionBegin(t, s)
	truncateReplayRetain(t, e, s, false, "p")
	enginetest.SessionSQL(t, s, "ALTER TABLE c ADD private_note TEXT")
	truncateReplayRetain(t, e, s, false, "p")
	parent := s.Transactions.Catalog
	original := parent.Originals[child.Address]
	scope := truncateUnitStage(t, e, s, "p")
	truncateUnitFK(t, e, p, child)
	if err := scope.Finish(nil); err != (catalog.Conflict{}) {
		t.Fatal("host incoming edge hidden by private descriptor escaped originals", err)
	}
	scope.Op.Done()
	if parent.Originals[child.Address] != original || parent.Descriptors[child.Address].(*catalog.Relation).Constraints.ForeignKeys != nil || e.Storage.Relations[p.Address] != oldP || e.Storage.Relations[q.Address] != oldQ || oldP.TruncateLineage != nil {
		t.Fatal("hidden host veto changed original provenance or published rows")
	}
	enginetest.TransactionEnd(t, s, tx, false)
}

func TestTruncateReplayPartitionMembership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{"CREATE TABLE r(id INT) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF r FOR VALUES FROM(0) TO(10)", "INSERT INTO r VALUES(1)"} {
		enginetest.SessionSQL(t, s, sql)
	}
	root, leaf := roleTableRef(t, e, "r"), roleTableRef(t, e, "leaf")
	attached := e.CatalogRead()
	partition := attached.MustRelation(root.Address).Partition
	membership := attached.MustRelation(leaf.Address).Membership
	enginetest.SessionSQL(t, s, "ALTER TABLE r DETACH PARTITION leaf")
	oldLeaf := e.Storage.Relations[leaf.Address]
	tx := enginetest.TransactionBegin(t, s)
	truncateReplayRetain(t, e, s, false, "r")
	scope := truncateUnitStage(t, e, s, "r")
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	for _, ref := range []ids.ObjectRef{root, leaf} {
		next := *base.MustRelation(ref.Address)
		next.ObjectRevision++
		if ref == root {
			next.Partition = partition
		} else {
			next.Membership = membership
		}
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
	}
	truncateUnitPublish(t, e, stage)
	graph := dml.TruncateGraph{Base: base.CaptureDescriptorVersion(), Targets: map[ids.ObjectAddress]int{root.Address: 1}}
	if err := graph.CheckCurrent(e.CatalogRead(), e.CatalogRead(), scope.Catalog, e.Storage.Sequences); err != (catalog.Conflict{}) {
		t.Fatal("actual late membership did not fail graph reconciliation", err)
	}
	if err := scope.Finish(nil); err != (catalog.Conflict{}) {
		t.Fatal("host partition membership escaped retained final preflight", err)
	}
	scope.Op.Done()
	if e.Storage.Relations[leaf.Address] != oldLeaf || len(oldLeaf.Versions) != 1 || oldLeaf.TruncateLineage != nil {
		t.Fatal("partition membership veto changed unselected leaf storage")
	}
	enginetest.TransactionEnd(t, s, tx, false)
}

func TestTruncateReplayTemporaryNamespace(t *testing.T) {
	e, owner, parent, _ := truncateUnitFixture(t)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE peer_child() INHERITS(p)")
	base := e.CatalogRead()
	namespace := base.MustNamespace(peer.TemporaryNamespace)
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "peer_child"})
	oldParent, oldPeer := e.Storage.Relations[parent.Address], e.Storage.Relations[address]
	tx := enginetest.TransactionBegin(t, owner)
	truncateReplayRetain(t, e, owner, false, "p")
	scope := truncateUnitStage(t, e, owner, "p")
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *namespace
	next.ObjectRevision++
	next.Temporary = owner.SessionToken
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	changedChild := *base.MustRelation(address)
	changedChild.ObjectRevision++
	changedChild.ObjectName = "changed_peer_child"
	if err := stage.Replace(&changedChild); err != nil {
		t.Fatal(err)
	}
	truncateUnitPublish(t, e, stage)
	if err := scope.Finish(nil); err != (catalog.Conflict{}) {
		t.Fatal("changed TEMP namespace authority reused peer exclusion", err)
	}
	scope.Op.Done()
	if e.Storage.Relations[parent.Address] != oldParent || e.Storage.Relations[address] != oldPeer || oldParent.TruncateLineage != nil || !storage.SameVersionRoot(oldPeer.Versions, e.Storage.Relations[address].Versions) {
		t.Fatal("namespace veto published target or peer storage")
	}
	enginetest.TransactionEnd(t, owner, tx, false)
}

func TestTruncateReplayMissingTemporaryNamespace(t *testing.T) {
	e, owner, parent, _ := truncateUnitFixture(t)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TEMP TABLE peer_child() INHERITS(p)")
	base := e.CatalogRead()
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: peer.TemporaryNamespace, Name: "peer_child"})
	tx := enginetest.TransactionBegin(t, owner)
	truncateReplayRetain(t, e, owner, false, "p")
	journal := owner.Transactions.Catalog
	graph := dml.TruncateGraph{Base: base.CaptureDescriptorVersion(), Targets: map[ids.ObjectAddress]int{parent.Address: 1}, Token: owner.SessionToken}
	if err := graph.CheckCurrent(base, e.CatalogRead(), journal, e.Storage.Sequences); err != nil {
		t.Fatal("unchanged genuine peer TEMP graph", err)
	}
	// Exercise invalid namespace authority through the actual journal/read
	// owner, without pretending invalid DDL was admissible for publication.
	changed := journal.Clone()
	next := *base.MustRelation(address)
	next.ObjectRevision++
	changed.RecordDescriptor(base.State(), address, &next)
	changed.RecordDescriptor(base.State(), peer.TemporaryNamespace, nil)
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(changed)
	e.Mu.Unlock()
	count := 0
	for range graph.Base.DescriptorsToReconcile(current) {
		count++
	}
	if count != current.DescriptorCount() || current.Get(peer.TemporaryNamespace) != nil {
		t.Fatal("missing namespace did not expose genuine changed descriptor inventory")
	}
	if err := graph.CheckCurrent(current, e.CatalogRead(), changed, e.Storage.Sequences); err != (catalog.Conflict{}) {
		t.Fatal("missing TEMP namespace authority escaped graph reconciliation", err)
	}
	if owner.Transactions.Catalog != journal || base.Get(peer.TemporaryNamespace) == nil {
		t.Fatal("invalid candidate mutated installed/saved namespace authority")
	}
	enginetest.TransactionEnd(t, owner, tx, false)
}

func TestTruncateReplayPLExceptionRoots(t *testing.T) {
	e, s, p, _ := truncateUnitFixture(t)
	original := e.Storage.Relations[p.Address]
	tx := enginetest.TransactionBegin(t, s)
	truncateReplayRetain(t, e, s, false, "p")
	enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(55)")
	retained := enginetest.SessionSQL(t, s, "SELECT id FROM p")
	parent := s.Transactions.Catalog
	parentRoot := parent.Storage[p.Address].Replacement
	fences := len(parent.Fences)
	writes := s.Transactions.Writes[p.Address]
	enginetest.SessionSQL(t, s, `DO $$ BEGIN
		BEGIN
			TRUNCATE p;
			CREATE TABLE exception_child(id INT);
			INSERT INTO p VALUES(99);
			RAISE EXCEPTION 'discard replay child';
		EXCEPTION WHEN raise_exception THEN
			PERFORM 1;
		END;
	END $$`)
	if s.Transactions.Catalog != parent || parent.Storage[p.Address].Replacement != parentRoot || len(parent.Fences) != fences || !storage.SameVersionRoot(s.Transactions.Writes[p.Address], writes) {
		t.Fatal("real procedural rollback lost enclosing graph journal/write/storage roots")
	}
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(s.Transactions.Catalog)
	e.Mu.Unlock()
	if private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "exception_child"}) != (ids.ObjectAddress{}) {
		t.Fatal("discarded procedural child retained private DDL authority")
	}
	enginetest.TransactionRows(t, s, "SELECT id FROM p", 55)
	enginetest.SessionSQL(t, s, `DO $$ BEGIN
		BEGIN
			TRUNCATE p;
			INSERT INTO p VALUES(77);
		EXCEPTION WHEN raise_exception THEN
			PERFORM 1;
		END;
	END $$`)
	enginetest.TransactionRows(t, s, "SELECT id FROM p", 77)
	truncateUnitExecute(t, e, s, truncateUnitAST("p"))
	enginetest.SessionSQL(t, s, "INSERT INTO p VALUES(88)")
	if parent.Storage[p.Address].Replacement != parentRoot || len(parent.Fences) != fences || len(writes) != 1 || parent == s.Transactions.Catalog {
		t.Fatal("successful procedural release/reappend mutated saved parent authority or rows")
	}
	if got := enginetest.Tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(55)}}) {
		t.Fatal("live eager buffer changed across exception rollback/release/reappend", got)
	}
	enginetest.TransactionEnd(t, s, tx, false)
	if e.Storage.Relations[p.Address] != original || len(original.Versions) != 2 || original.TruncateLineage != nil {
		t.Fatal("outer rollback published procedural private roots")
	}
}
