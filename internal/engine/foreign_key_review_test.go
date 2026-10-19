package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
	parent := view.MustRelation(catalogRef(t, e, view, "p").Address)
	child := view.MustRelation(catalogRef(t, e, view, "c").Address)
	f := child.Constraints.ForeignKeys[0]
	index := view.MustRelation(f.Index)
	comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
	if err != nil {
		t.Fatal(err)
	}
	root := e.storage.IndexRoots[f.Index]
	original := slices.Clone(root.Entries)
	rows := foreignKeyRows{committed: e.storage.Relations[parent.Address()].Versions, ends: e.storage.Relations[parent.Address()].Ends, frontier: e.commitSeq, root: root, index: f.Index}
	missing := e.storage.Relations[catalogRef(t, e, view, "probe_input").Address].Versions[0].Values
	for _, target := range []int{0, 2048, 4095, 4096} {
		t.Run(fmt.Sprint(target), func(t *testing.T) {
			values := missing
			if target < 4096 {
				values = root.Entries[target].Keys
			}
			key := make([]sqltype.Value, len(comparisons))
			if err := prepareForeignKeyChild(context.Background(), comparisons, values, key, sqltype.InputEnvironment{}); err != nil {
				t.Fatal(err)
			}
			compared, visited := 0, 0
			var located storage.IndexEntry
			err := probeForeignKeyIndex(root, func(entry storage.IndexEntry) (int, error) {
				compared++
				return foreignKeyIndexOrder(context.Background(), index, comparisons, entry, key, sqltype.InputEnvironment{})
			}, func(entry storage.IndexEntry) (bool, error) {
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
				if matching.Row != 0 {
					t.Fatal("missing input matched", matching)
				}
			} else if matching.Row != located.Row || locator.Position != located.Position || matching.Version != located.Version {
				t.Fatal("probe did not return the exact source version locator", matching, locator, located)
			}
		})
	}
	if !reflect.DeepEqual(root.Entries, original) {
		t.Fatal("probe modified the source index")
	}
	visited := 0
	if err := rows.each(func(storage.RowVersion) (bool, error) { visited++; return true, nil }); err != nil || visited != 1 {
		t.Fatal("successful iterator stop walked trailing versions", visited, err)
	}
}

func TestForeignKeySelectedFamilyAuthority(t *testing.T) {
	e, _, _, s, parentRef, _ := foreignKeyLockFixture(t)
	view := e.CatalogRead()
	parent := view.MustRelation(parentRef.Address)
	child := view.MustRelation(catalogRef(t, e, view, "c").Address)
	f := child.Constraints.ForeignKeys[0]
	index := view.MustRelation(f.Index)
	numeric, ok := view.LookupType(sqltype.Numeric)
	if !ok {
		t.Fatal("numeric provider missing")
	}
	candidate := *child
	candidate.Columns = slices.Clone(child.Columns)
	candidate.Columns[0].Usage = numeric.Usage()
	if _, err := bindForeignKeyComparison(view, &candidate, parent, index, f); err == nil {
		t.Fatal("predicate numeric/int comparability overrode integer index family")
	} else {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" {
			t.Fatal(err)
		}
	}
	// A reader lacking the exact selected input generation cannot borrow it.
	state := catalog.CloneState(view.State())
	state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)})
	if _, err := bindForeignKeyComparison(e.catalog.read(state), child, parent, index, f); err == nil {
		t.Fatal("missing actual type generation admitted")
	}
	if child.Columns[0].Usage.Type.ID() != sqltype.Int4 || child.Constraints.ForeignKeys[0].Index != index.Address() {
		t.Fatal("binding changed the source attachment")
	}
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
}

func TestForeignKeyTypeFailedSnapshotAuthority(t *testing.T) {
	e, _, _, s, parentRef, _ := foreignKeyLockFixture(t)
	sessionSQL(t, s, "INSERT INTO c VALUES(1)")
	view := e.CatalogRead()
	childRef := catalogRef(t, e, view, "c")
	child := view.MustRelation(childRef.Address)
	parent := view.MustRelation(parentRef.Address)
	f := child.Constraints.ForeignKeys[0]
	index := view.MustRelation(f.Index)
	parentStore, childStore := e.storage.Relations[parentRef.Address], e.storage.Relations[childRef.Address]
	parentRows, childRows := slices.Clone(parentStore.Versions), slices.Clone(childStore.Versions)
	root := e.storage.IndexRoots[f.Index]
	entries := slices.Clone(root.Entries)
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
		if e.CatalogRead().State() != view.State() || e.storage.Relations[parentRef.Address] != parentStore || e.storage.Relations[childRef.Address] != childStore || e.storage.IndexRoots[f.Index] != root {
			t.Fatal("failed TYPE published metadata/storage/index authority", trial.sql)
		}
		if !reflect.DeepEqual(parentStore.Versions, parentRows) || !reflect.DeepEqual(childStore.Versions, childRows) || !reflect.DeepEqual(root.Entries, entries) || !reflect.DeepEqual(child.Constraints.ForeignKeys[0], f) {
			t.Fatal("failed TYPE mutated borrowed data or attachment", trial.sql)
		}
	}
	sessionSQL(t, s, "ALTER TABLE c ALTER pid TYPE BIGINT")
	next := e.CatalogRead().MustRelation(childRef.Address)
	if next == child || next.Constraints.ForeignKeys[0].OID == f.OID || next.Constraints.ForeignKeys[0].Index != f.Index || next.Constraints.ForeignKeys[0].Target != parent.Address() || e.CatalogRead().Get(index.Address()) != index {
		t.Fatal("successful child TYPE lost stable supporting/target identities", next.Constraints.ForeignKeys[0], f)
	}
	if view.Get(childRef.Address) != child || view.Get(parentRef.Address) != parent || !reflect.DeepEqual(child.Constraints.ForeignKeys[0], f) || !reflect.DeepEqual(childStore.Versions, childRows) || !reflect.DeepEqual(root.Entries, entries) {
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
	alpha := ordinary.catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "fk_namespace_alpha"})
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
	if skipped != (ids.ObjectRef{}) || e.CatalogRead().State() != view.State() {
		t.Fatal("no-op masqueraded as a successful creation", skipped)
	}
	if len(view.MustRelation(ref.Address).Constraints.ForeignKeys) != 0 {
		t.Fatal("no-op attached ignored foreign-key metadata")
	}
}
