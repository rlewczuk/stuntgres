package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestDriverBatchCatalogRowsRolesAndIndexes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			beforeCatalog, beforeRoles, frontier := e.Catalog.State, e.RoleStore.Published, e.Visibility.CommitSeq()
			original, revision := a.Authorization.State, a.Authorization.Revision
			m := batchUnitStart(t, a, context.Background())
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE ROLE batch_member LOGIN", "CREATE SCHEMA batch_schema AUTHORIZATION batch_member", "CREATE TABLE batch_schema.t(v INT)", "INSERT INTO batch_schema.t VALUES(1),(2)", "CREATE UNIQUE INDEX ix ON batch_schema.t(v)", "GRANT SELECT ON batch_schema.t TO batch_member", "SET ROLE batch_member"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			result, err := m.step(statement(t, "SELECT v FROM batch_schema.t ORDER BY v"))
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			a.Transactions.Mu.Lock()
			changes := a.Transactions.Catalog
			ref := a.Transactions.Ref
			a.Transactions.Mu.Unlock()
			if changes == nil || changes.Roles == nil || changes.RolesBase != beforeRoles || ref == (ids.TxRef{}) {
				t.Fatal("missing joint candidate authority")
			}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(changes)
			e.Mu.Unlock()
			ns, ok := private.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "batch_schema"})
			if !ok {
				t.Fatal("pending namespace")
			}
			table, ok := private.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "t"})
			if !ok {
				t.Fatal("pending table")
			}
			idx, ok := private.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ix"})
			if !ok {
				t.Fatal("pending index")
			}
			if changes.Storage[table].Original != nil || len(index.JournalIndexesOrZero(changes).UniqueRoots[idx].Entries) != 0 || len(a.Transactions.Writes[table]) != 2 {
				t.Fatal("baseline and own writes conflated")
			}
			if e.Catalog.State != beforeCatalog || e.RoleStore.Published != beforeRoles || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[table] != nil || e.Storage.IndexRoots[idx] != nil {
				t.Fatal("candidate escaped before message end")
			}
			if _, ok := e.Roles().Lookup("batch_member"); ok {
				t.Fatal("pending login visible outside owner")
			}
			if commit {
				if err = m.end(nil); err != nil {
					t.Fatal(err)
				}
				if _, ok := e.Roles().Lookup("batch_member"); !ok {
					t.Fatal("role root not installed with catalog")
				}
				if e.Catalog.State == beforeCatalog || e.RoleStore.Published == beforeRoles || len(e.Storage.Relations[table].Versions) != 2 || len(e.Storage.IndexRoots[idx].Entries) != 2 {
					t.Fatal("incomplete atomic installation")
				}
				sessionSQL(t, a, "RESET ROLE")
				transactionRows(t, a, "SELECT v FROM batch_schema.t ORDER BY v", 1, 2)
			} else {
				_, err = m.step(statement(t, "SELECT missing"))
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" {
					t.Fatal(err)
				}
				if end := m.end(err); end != err {
					t.Fatal("primary error replaced", end, err)
				}
				if e.Catalog.State != beforeCatalog || e.RoleStore.Published != beforeRoles || e.Visibility.CommitSeq() != frontier || a.Authorization.State != original || a.Authorization.Revision <= revision || a.Transactions.IdentityCaptured {
					t.Fatal("joint rollback or identity revision")
				}
			}
			if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
				t.Fatal("retained output lost after disposition", got)
			}
		})
	}
}
func TestDriverBatchNewIndexReconstructsPrefix(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(v INT)")
			m := batchUnitStart(t, a, context.Background())
			defer m.end(context.Canceled)
			for _, q := range []string{"INSERT INTO t VALUES(1),(2)", "CREATE INDEX ix ON t USING " + method + "(v)"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			if err := m.end(nil); err != nil {
				t.Fatal(err)
			}
			ref := catalogRef(t, e, e.CatalogRead(), "ix")
			root := e.Storage.IndexRoots[ref.Address]
			if method == "btree" {
				if root == nil || len(root.Entries) != 2 {
					t.Fatal("new index omitted earlier private versions")
				}
			} else {
				if root == nil || root.Hash == nil {
					t.Fatal("new real hash root")
				}
				table := catalogRef(t, e, e.CatalogRead(), "t")
				store := e.Storage.Relations[table.Address]
				if root.Hash.Storage != store || root.Hash.Prefix != len(store.Versions) || root.Hash.Generation != store.Generation || root.Hash.Ends != store.Ends {
					t.Fatal("hash root source is not published storage")
				}
			}
			transactionRows(t, a, "SELECT v FROM t WHERE v=2", 2)
		})
	}
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	if _, err := m.step(statement(t, "INSERT INTO t VALUES(1),(1)")); err != nil {
		t.Fatal(err)
	}
	_, err := m.step(statement(t, "CREATE UNIQUE INDEX ix ON t(v)"))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" || diagnostic.Message != `could not create unique index "ix"` || diagnostic.Detail != "Key (v)=(1) is duplicated." {
		t.Fatal("CREATE failed at its own statement with build diagnostic", err)
	}
	if end := m.end(err); end != err {
		t.Fatal(end)
	}
	transactionRows(t, s, "SELECT v FROM t")
}

func TestDriverBatchOverlayCaptureEntryAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE entry_authority(v INT)")
	ref := catalogRef(t, e, e.CatalogRead(), "entry_authority")
	e.Mu.Lock()
	defer e.Mu.Unlock()
	before := e.Catalog.State
	old := before.MustRelation(ref.Address)
	stage := e.BeginCatalogStage(e.Catalog.Read(before))
	defer e.DiscardCatalogStage(stage)
	next := *old
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	candidate := session.NewJournal()
	// Projection can already replace descriptors in the proposed changes.
	// Those replacements are not the statement's entry authority.
	e.CatalogChangeLocked(candidate, ref.Address, &next)
	if err := core.EngineCaptureCatalogStageLocked(e, stage, candidate, before, nil); err != nil {
		t.Fatal("proposed replacement mistaken for entry predecessor", err)
	}
	if candidate.Originals[ref.Address] != old || e.Catalog.State != before || e.Catalog.State.Get(ref.Address) != old {
		t.Fatal("capture rewrote committed predecessor or published candidate")
	}
	peer := catalog.CloneState(before)
	changed := *old
	changed.ObjectRevision += 2
	peer.SetDescriptor(ref.Address, &changed)
	if err := core.EngineCaptureCatalogStageLocked(e, stage, candidate.Clone(), peer, nil); err == nil {
		t.Fatal("changed entry predecessor accepted")
	}
}

func TestDriverBatchOverlayCaptureNamespaceIntent(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	definition := statement(t, "CREATE TEMP TABLE namespace_entry(v INT)").(*parser.CreateTable)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	before := e.Catalog.State
	auth, err := e.AuthorizationLocked(m.op)
	if err != nil {
		t.Fatal(err)
	}
	candidate := s.Transactions.Catalog.Clone()
	ns, err := e.RelationCreationNamespaceLocked(auth, definition.Name, "temporary", candidate)
	if err != nil {
		t.Fatal(err)
	}
	auth.Catalog = e.PrivateCatalogLocked(candidate)
	auth.Recapture(auth.Catalog)
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	stage.AuthorizeLocked(auth, s)
	if err := SchemaTableLocked(e, m.op, auth, stage, definition, ns.Address, auth.State.CurrentUser, m.scope.Input, nil); err != nil {
		t.Fatal(err)
	}
	if err := core.EngineCaptureCatalogStageLocked(e, stage, candidate, before, nil); err != nil {
		t.Fatal("authentic pre-stage namespace creation rejected", err)
	}
	namespace := candidate.Descriptors[ns.Address].(*catalog.Namespace)
	key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: namespace.Name()}
	if original, exists := candidate.Originals[ns.Address]; !exists || original != nil {
		t.Fatal("namespace creation lost its committed absence fence")
	}
	if original, exists := candidate.Names[key]; !exists || original != (ids.ObjectAddress{}) {
		t.Fatal("namespace creation lost its name absence fence")
	}
	if e.Catalog.State != before || before.Get(ns.Address) != nil {
		t.Fatal("namespace intent published during capture")
	}
	if err := e.CatalogPreflightLocked(candidate); err != nil {
		t.Fatal("valid namespace creation failed committed preflight", err)
	}
	missing := candidate.Clone()
	delete(missing.Originals, ns.Address)
	if err := core.EngineCaptureCatalogStageLocked(e, stage, missing, before, nil); err == nil {
		t.Fatal("namespace without creation intent admitted")
	}
	missingName := candidate.Clone()
	delete(missingName.Names, key)
	if err := core.EngineCaptureCatalogStageLocked(e, stage, missingName, before, nil); err == nil {
		t.Fatal("namespace without creation-name fence admitted")
	}
	changed := *namespace
	changed.ObjectRevision++
	replaced := candidate.Clone()
	replaced.Descriptors[ns.Address] = &changed
	if err := core.EngineCaptureCatalogStageLocked(e, stage, replaced, before, nil); err == nil {
		t.Fatal("changed namespace intent admitted")
	}
	current := catalog.CloneState(before)
	current.SetDescriptor(ns.Address, &changed)
	if err := core.EngineCaptureCatalogStageLocked(e, stage, candidate.Clone(), current, nil); err == nil {
		t.Fatal("different current namespace admitted")
	}
	claimed := catalog.CloneState(before)
	claim := *namespace
	oid, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		t.Fatal(err)
	}
	claim.ObjectAddress.ObjectID = oid
	claimed.SetDescriptor(claim.Address(), &claim)
	claimed.BindName(key, claim.Address())
	if err := core.EngineCaptureCatalogStageLocked(e, stage, candidate.Clone(), claimed, nil); err == nil {
		t.Fatal("already claimed entry namespace name admitted")
	}
	// Even the exact same namespace appearing in the committed root conflicts
	// with the original absence stamp; entry admission cannot bypass publication.
	e.Catalog.PublishLocked(e.PrivateCatalogLocked(candidate).State())
	defer func() { e.Catalog.PublishLocked(before) }()
	if err := e.CatalogPreflightLocked(candidate); err == nil {
		t.Fatal("committed namespace defeated original absence fence")
	}
}

func TestDriverBatchUtilityComposition(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			before := e.Catalog.State
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			for _, command := range []struct {
				sql   string
				count int64
			}{
				{"CREATE SCHEMA bs", 0},
				{"CREATE TABLE bs.u(i INT)", 0},
				{"INSERT INTO bs.u VALUES(1),(2)", 2},
				{"ALTER TABLE bs.u ADD COLUMN j INT DEFAULT 4", 0},
				{"CREATE INDEX bi ON bs.u(i)", 0},
				{"CREATE VIEW bs.v AS SELECT i FROM bs.u", 0},
				{"CREATE TABLE bs.c AS SELECT i FROM bs.v", 2},
			} {
				result, err := m.step(statement(t, command.sql))
				if err != nil {
					t.Fatal(command.sql, err)
				}
				if result.Count != command.count {
					t.Fatal(command.sql, result.Count, command.count)
				}
			}
			private, err := m.step(statement(t, "SELECT i,j FROM bs.u ORDER BY i"))
			if err != nil {
				t.Fatal(err)
			}
			if got := tuples(private); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(4)}, {int64(2), int64(4)}}) {
				t.Fatal("own row projection was not preserved", got)
			}
			private.Rows.Close()
			retained, err := m.step(statement(t, "SELECT i FROM bs.c ORDER BY i"))
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Rows.Close()
			e.Mu.Lock()
			catalog := e.PrivateCatalogLocked(s.Transactions.Catalog)
			ns := catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "bs"})
			table := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "u"})
			copyTable := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "c"})
			index := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "bi"})
			unpublished := e.Catalog.State == before && e.Storage.Relations[table] == nil && e.Storage.Relations[copyTable] == nil && e.Storage.IndexRoots[index] == nil
			e.Mu.Unlock()
			if !unpublished {
				t.Fatal("composition published catalog, storage or index before message disposition")
			}
			if commit {
				if err := m.end(nil); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, s, "SELECT i FROM bs.c ORDER BY i", 1, 2)
				transactionRows(t, s, "SELECT j FROM bs.u ORDER BY i", 4, 4)
				if e.Storage.Relations[table] == nil || e.Storage.Relations[copyTable] == nil || len(e.Storage.Relations[table].Versions) != 2 || len(e.Storage.Relations[copyTable].Versions) != 2 || e.Storage.IndexRoots[index] == nil || len(e.Storage.IndexRoots[index].Entries) != 2 {
					t.Fatal("composition did not publish storage and index together")
				}
			} else {
				_, err := m.step(statement(t, "SELECT missing"))
				err = m.op.CommandError(err)
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: `column "missing" does not exist`, Position: "8"}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal("complete internal command diagnostic", err, want)
				}
				if end := m.end(err); end != err {
					t.Fatal("composition rollback replaced primary error", end)
				}
				if e.Catalog.State != before || e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "bs"}) != (ids.ObjectAddress{}) {
					t.Fatal("failed composition published catalog prefix")
				}
			}
			if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
				t.Fatal("composition lost retained CTAS consumer output", got)
			}
		})
	}
}
