package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestDriverBatchCatalogRowsRolesAndIndexes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			beforeCatalog, beforeRoles, frontier := e.catalog.state, e.roles.published, e.visibility.commitSeq
			original, revision := a.authorization.state, a.authorization.revision
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
			a.transactions.mu.Lock()
			changes := a.transactions.catalog
			ref := a.transactions.ref
			a.transactions.mu.Unlock()
			if changes == nil || changes.roles == nil || changes.rolesBase != beforeRoles || ref == (ids.TxRef{}) {
				t.Fatal("missing joint candidate authority")
			}
			e.mu.Lock()
			private := e.privateCatalogLocked(changes)
			e.mu.Unlock()
			ns, ok := private.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "batch_schema"})
			if !ok {
				t.Fatal("pending namespace")
			}
			table, ok := private.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "t"})
			if !ok {
				t.Fatal("pending table")
			}
			index, ok := private.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ix"})
			if !ok {
				t.Fatal("pending index")
			}
			if changes.storage[table].original != nil || len(changes.uniqueRoots[index].Entries) != 0 || len(a.transactions.writes[table]) != 2 {
				t.Fatal("baseline and own writes conflated")
			}
			if e.catalog.state != beforeCatalog || e.roles.published != beforeRoles || e.visibility.commitSeq != frontier || e.storage.Relations[table] != nil || e.storage.IndexRoots[index] != nil {
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
				if e.catalog.state == beforeCatalog || e.roles.published == beforeRoles || len(e.storage.Relations[table].Versions) != 2 || len(e.storage.IndexRoots[index].Entries) != 2 {
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
				if e.catalog.state != beforeCatalog || e.roles.published != beforeRoles || e.visibility.commitSeq != frontier || a.authorization.state != original || a.authorization.revision <= revision || a.transactions.identityCaptured {
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
			root := e.storage.IndexRoots[ref.Address]
			if method == "btree" {
				if root == nil || len(root.Entries) != 2 {
					t.Fatal("new index omitted earlier private versions")
				}
			} else {
				if root == nil || root.Hash == nil {
					t.Fatal("new real hash root")
				}
				table := catalogRef(t, e, e.CatalogRead(), "t")
				store := e.storage.Relations[table.Address]
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
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.catalog.state
	old := before.MustRelation(ref.Address)
	stage := e.BeginCatalogStage(e.catalog.read(before))
	defer e.DiscardCatalogStage(stage)
	next := *old
	next.ObjectRevision++
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	candidate := newCatalogChanges()
	// Projection can already replace descriptors in the proposed changes.
	// Those replacements are not the statement's entry authority.
	e.catalogChangeLocked(candidate, ref.Address, &next)
	if err := e.captureCatalogStageLocked(stage, candidate, before, nil); err != nil {
		t.Fatal("proposed replacement mistaken for entry predecessor", err)
	}
	if candidate.originals[ref.Address] != old || e.catalog.state != before || e.catalog.state.Get(ref.Address) != old {
		t.Fatal("capture rewrote committed predecessor or published candidate")
	}
	peer := catalog.CloneState(before)
	changed := *old
	changed.ObjectRevision += 2
	peer.SetDescriptor(ref.Address, &changed)
	if err := e.captureCatalogStageLocked(stage, cloneCatalogChanges(candidate), peer, nil); err == nil {
		t.Fatal("changed entry predecessor accepted")
	}
}

func TestDriverBatchOverlayCaptureNamespaceIntent(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	definition := statement(t, "CREATE TEMP TABLE namespace_entry(v INT)").(*parser.CreateTable)
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.catalog.state
	auth, err := e.authorizationLocked(m.op)
	if err != nil {
		t.Fatal(err)
	}
	candidate := cloneCatalogChanges(s.transactions.catalog)
	ns, err := e.relationCreationNamespaceLocked(auth, definition.Name, "temporary", candidate)
	if err != nil {
		t.Fatal(err)
	}
	auth.catalog = e.privateCatalogLocked(candidate)
	auth.Recapture(auth.catalog)
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, s)
	if err := e.schemaTableLocked(m.op, auth, stage, definition, ns.Address, auth.state.CurrentUser, m.scope.input, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.captureCatalogStageLocked(stage, candidate, before, nil); err != nil {
		t.Fatal("authentic pre-stage namespace creation rejected", err)
	}
	namespace := candidate.descriptors[ns.Address].(*catalog.Namespace)
	key := ids.NameKey{ClassID: ids.NamespaceClassID, Name: namespace.Name()}
	if original, exists := candidate.originals[ns.Address]; !exists || original != nil {
		t.Fatal("namespace creation lost its committed absence fence")
	}
	if original, exists := candidate.names[key]; !exists || original != (ids.ObjectAddress{}) {
		t.Fatal("namespace creation lost its name absence fence")
	}
	if e.catalog.state != before || before.Get(ns.Address) != nil {
		t.Fatal("namespace intent published during capture")
	}
	if err := e.catalogPreflightLocked(candidate); err != nil {
		t.Fatal("valid namespace creation failed committed preflight", err)
	}
	missing := cloneCatalogChanges(candidate)
	delete(missing.originals, ns.Address)
	if err := e.captureCatalogStageLocked(stage, missing, before, nil); err == nil {
		t.Fatal("namespace without creation intent admitted")
	}
	missingName := cloneCatalogChanges(candidate)
	delete(missingName.names, key)
	if err := e.captureCatalogStageLocked(stage, missingName, before, nil); err == nil {
		t.Fatal("namespace without creation-name fence admitted")
	}
	changed := *namespace
	changed.ObjectRevision++
	replaced := cloneCatalogChanges(candidate)
	replaced.descriptors[ns.Address] = &changed
	if err := e.captureCatalogStageLocked(stage, replaced, before, nil); err == nil {
		t.Fatal("changed namespace intent admitted")
	}
	current := catalog.CloneState(before)
	current.SetDescriptor(ns.Address, &changed)
	if err := e.captureCatalogStageLocked(stage, cloneCatalogChanges(candidate), current, nil); err == nil {
		t.Fatal("different current namespace admitted")
	}
	claimed := catalog.CloneState(before)
	claim := *namespace
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		t.Fatal(err)
	}
	claim.ObjectAddress.ObjectID = oid
	claimed.SetDescriptor(claim.Address(), &claim)
	claimed.BindName(key, claim.Address())
	if err := e.captureCatalogStageLocked(stage, cloneCatalogChanges(candidate), claimed, nil); err == nil {
		t.Fatal("already claimed entry namespace name admitted")
	}
	// Even the exact same namespace appearing in the committed root conflicts
	// with the original absence stamp; entry admission cannot bypass publication.
	e.catalog.state = e.privateCatalogLocked(candidate).State()
	defer func() { e.catalog.state = before }()
	if err := e.catalogPreflightLocked(candidate); err == nil {
		t.Fatal("committed namespace defeated original absence fence")
	}
}

func TestDriverBatchUtilityComposition(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			before := e.catalog.state
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
			e.mu.Lock()
			catalog := e.privateCatalogLocked(s.transactions.catalog)
			ns := catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "bs"})
			table := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "u"})
			copyTable := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "c"})
			index := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "bi"})
			unpublished := e.catalog.state == before && e.storage.Relations[table] == nil && e.storage.Relations[copyTable] == nil && e.storage.IndexRoots[index] == nil
			e.mu.Unlock()
			if !unpublished {
				t.Fatal("composition published catalog, storage or index before message disposition")
			}
			if commit {
				if err := m.end(nil); err != nil {
					t.Fatal(err)
				}
				transactionRows(t, s, "SELECT i FROM bs.c ORDER BY i", 1, 2)
				transactionRows(t, s, "SELECT j FROM bs.u ORDER BY i", 4, 4)
				if e.storage.Relations[table] == nil || e.storage.Relations[copyTable] == nil || len(e.storage.Relations[table].Versions) != 2 || len(e.storage.Relations[copyTable].Versions) != 2 || e.storage.IndexRoots[index] == nil || len(e.storage.IndexRoots[index].Entries) != 2 {
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
				if e.catalog.state != before || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "bs"}) != (ids.ObjectAddress{}) {
					t.Fatal("failed composition published catalog prefix")
				}
			}
			if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}}) {
				t.Fatal("composition lost retained CTAS consumer output", got)
			}
		})
	}
}
