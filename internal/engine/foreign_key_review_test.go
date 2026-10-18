package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// These assertions concern borrowed roots, physical locators and authority
// snapshots, not public SQL goldens (which live in the shared reference tests).
func TestForeignKeyIndexProbeVisitBounds(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id BIGINT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(pid BIGINT REFERENCES p)")
	var insert strings.Builder
	insert.WriteString("INSERT INTO p VALUES")
	for i := range 4096 {
		if i != 0 {
			insert.WriteByte(',')
		}
		fmt.Fprintf(&insert, "(%d)", i)
	}
	sessionSQL(t, s, insert.String())
	sessionSQL(t, s, "CREATE TABLE probe_input(value BIGINT)")
	sessionSQL(t, s, "INSERT INTO probe_input VALUES(4096)")
	view := e.CatalogRead()
	parent := view.state.descriptors[catalogRef(t, e, view, "p").Address].(*relationDescriptor)
	child := view.state.descriptors[catalogRef(t, e, view, "c").Address].(*relationDescriptor)
	f := child.foreignKeys[0]
	index := view.state.descriptors[f.Index].(*relationDescriptor)
	comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
	if err != nil {
		t.Fatal(err)
	}
	root := e.indexRoots[f.Index]
	original := slices.Clone(root.entries)
	rows := foreignKeyRows{committed: e.storage[parent.address].versions, ends: e.storage[parent.address].ends, frontier: e.commitSeq, root: root, index: f.Index}
	missing := e.storage[catalogRef(t, e, view, "probe_input").Address].versions[0].values
	for _, target := range []int{0, 2048, 4095, 4096} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			values := missing
			if target < 4096 {
				values = root.entries[target].keys
			}
			key := make([]sqltype.Value, len(comparisons))
			if err := prepareForeignKeyChild(context.Background(), comparisons, values, key, sqltype.InputEnvironment{}); err != nil {
				t.Fatal(err)
			}
			compared, visited := 0, 0
			var located logicalIndexEntry
			err := probeForeignKeyIndex(root, func(entry logicalIndexEntry) (int, error) {
				compared++
				return foreignKeyIndexOrder(context.Background(), index, comparisons, entry, key, sqltype.InputEnvironment{})
			}, func(entry logicalIndexEntry) (bool, error) {
				visited++
				located = entry
				return true, nil
			})
			wantVisits := 1
			if target == 4096 {
				wantVisits = 0
			}
			if err != nil || compared > 15 || visited != wantVisits {
				t.Fatal("ordered lower bound/equal run", target, compared, visited, err)
			}
			matching, locator, err := findForeignKeyParent(context.Background(), index, comparisons, rows, key, sqltype.InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			if target == 4096 {
				if matching.row != 0 {
					t.Fatal("missing input matched", matching)
				}
			} else if matching.row != located.row || locator.position != located.position || matching.version != located.version {
				t.Fatal("probe did not return the exact source version locator", matching, locator, located)
			}
		})
	}
	if !reflect.DeepEqual(root.entries, original) {
		t.Fatal("probe modified the source index")
	}
	visited := 0
	if err := rows.each(func(rowVersion) (bool, error) { visited++; return true, nil }); err != nil || visited != 1 {
		t.Fatal("successful iterator stop walked trailing versions", visited, err)
	}
}

func TestForeignKeySelectedFamilyAuthority(t *testing.T) {
	e, _, _, s, parentRef, _ := foreignKeyLockFixture(t)
	view := e.CatalogRead()
	parent := view.state.descriptors[parentRef.Address].(*relationDescriptor)
	child := view.state.descriptors[catalogRef(t, e, view, "c").Address].(*relationDescriptor)
	f := child.foreignKeys[0]
	index := view.state.descriptors[f.Index].(*relationDescriptor)
	numeric, ok := view.LookupType(sqltype.Numeric)
	if !ok {
		t.Fatal("numeric provider missing")
	}
	candidate := *child
	candidate.columns = slices.Clone(child.columns)
	candidate.columns[0].Usage = numeric.Usage()
	if _, err := bindForeignKeyComparison(view, &candidate, parent, index, f); err == nil {
		t.Fatal("predicate numeric/int comparability overrode integer index family")
	} else {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" {
			t.Fatal(err)
		}
	}
	// A reader lacking the exact selected input generation cannot borrow it.
	state := cloneCatalog(view.state)
	delete(state.descriptors, ids.ObjectAddress{ClassID: TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	if _, err := bindForeignKeyComparison(CatalogRead{e, state}, child, parent, index, f); err == nil {
		t.Fatal("missing actual type generation admitted")
	}
	if child.columns[0].Usage.Type.ID() != sqltype.Int4 || child.foreignKeys[0].Index != index.address {
		t.Fatal("binding changed the source attachment")
	}
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
}

func TestForeignKeyTypeFailedSnapshotAuthority(t *testing.T) {
	e, _, _, s, parentRef, _ := foreignKeyLockFixture(t)
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
	view := e.CatalogRead()
	childRef := catalogRef(t, e, view, "c")
	child := view.state.descriptors[childRef.Address].(*relationDescriptor)
	parent := view.state.descriptors[parentRef.Address].(*relationDescriptor)
	f := child.foreignKeys[0]
	index := view.state.descriptors[f.Index].(*relationDescriptor)
	parentStore, childStore := e.storage[parentRef.Address], e.storage[childRef.Address]
	parentRows, childRows := slices.Clone(parentStore.versions), slices.Clone(childStore.versions)
	root := e.indexRoots[f.Index]
	entries := slices.Clone(root.entries)
	for _, trial := range []struct{ sql, code string }{
		{"ALTER TABLE p ALTER id TYPE BIGINT USING id+10", "23503"},
		{"ALTER TABLE c ALTER pid TYPE BIGINT USING pid+10", "23503"},
		{"ALTER TABLE p ALTER id TYPE TEXT", "42804"},
		{"ALTER TABLE c ALTER pid TYPE NUMERIC", "42804"},
	} {
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, trial.sql), 0, nil)
		op.Done()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != trial.code {
			t.Fatal(trial.sql, err)
		}
		if e.CatalogRead().state != view.state || e.storage[parentRef.Address] != parentStore || e.storage[childRef.Address] != childStore || e.indexRoots[f.Index] != root {
			t.Fatal("failed TYPE published metadata/storage/index authority", trial.sql)
		}
		if !reflect.DeepEqual(parentStore.versions, parentRows) || !reflect.DeepEqual(childStore.versions, childRows) || !reflect.DeepEqual(root.entries, entries) || !reflect.DeepEqual(child.foreignKeys[0], f) {
			t.Fatal("failed TYPE mutated borrowed data or attachment", trial.sql)
		}
	}
	sessionSQL(t, s, "ALTER TABLE c ALTER pid TYPE BIGINT")
	next := e.CatalogRead().state.descriptors[childRef.Address].(*relationDescriptor)
	if next == child || next.foreignKeys[0].OID == f.OID || next.foreignKeys[0].Index != f.Index || next.foreignKeys[0].Target != parent.address || e.CatalogRead().state.descriptors[index.address] != index {
		t.Fatal("successful child TYPE lost stable supporting/target identities", next.foreignKeys[0], f)
	}
	if view.state.descriptors[childRef.Address] != child || view.state.descriptors[parentRef.Address] != parent || !reflect.DeepEqual(child.foreignKeys[0], f) || !reflect.DeepEqual(childStore.versions, childRows) || !reflect.DeepEqual(root.entries, entries) {
		t.Fatal("successful TYPE changed retained snapshot authority")
	}
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
}

func TestForeignKeyTrustedReplayNamespacePrincipal(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE ROLE fk_namespace_alpha")
	sessionSQL(t, s, "CREATE SCHEMA fk_namespace_alpha AUTHORIZATION fk_namespace_alpha")
	sessionSQL(t, s, "SET ROLE fk_namespace_alpha")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.restoreInsertAuthority = true
	e.mu.Lock()
	originalTargets := e.importTargets
	e.importTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	ordinary, ordinaryErr := e.authorizationLocked(op)
	trusted, trustedErr := e.statementAuthorizationLocked(op, statement(t, "INSERT INTO nowhere VALUES(1)"))
	var path []ids.ObjectAddress
	var pathErr error
	if trustedErr == nil {
		path, _, pathErr = e.explicitPath(trusted)
	}
	e.importTargets = originalTargets
	e.mu.Unlock()
	if ordinaryErr != nil || trustedErr != nil || pathErr != nil {
		t.Fatal(ordinaryErr, trustedErr, pathErr)
	}
	alpha := ordinary.catalog.state.names[ids.NameKey{ClassID: NamespaceClassID, Name: "fk_namespace_alpha"}]
	if ordinary.state.CurrentUser == ordinary.state.Authenticated || trusted.state.CurrentUser != ordinary.state.Authenticated || trusted.namespaceUser != ordinary.state.CurrentUser || !slices.Contains(path, alpha) || trusted.sessionRoleState != ordinary.sessionRoleState {
		t.Fatal("permission elevation replaced semantic namespace principal", ordinary.state, trusted.state, trusted.namespaceUser, path)
	}
	after, err := op.Authorization()
	if err != nil || after.state != ordinary.state || after.namespaceUser != (authorization.PrincipalRef{}) {
		t.Fatal("transient replay authority changed public session identity", after.state, err)
	}
}

func TestForeignKeyCreateOutcomeIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	created := sessionSQL(t, s, "CREATE TABLE outcome(pid INT)").created
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "outcome")
	if created != ref {
		t.Fatal("creator did not return its exact minted reference", created, ref)
	}
	skipped := sessionSQL(t, s, "CREATE TABLE IF NOT EXISTS outcome(pid INT REFERENCES missing_schema.missing_parent)").created
	if skipped != (ids.ObjectRef{}) || e.CatalogRead().state != view.state {
		t.Fatal("no-op masqueraded as a successful creation", skipped)
	}
	if len(view.state.descriptors[ref.Address].(*relationDescriptor).foreignKeys) != 0 {
		t.Fatal("no-op attached ignored foreign-key metadata")
	}
}
