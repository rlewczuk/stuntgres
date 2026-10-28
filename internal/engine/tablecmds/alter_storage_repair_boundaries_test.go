package tablecmds_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
)

func storageBoundaryRepair(t *testing.T, assignments string) *parser.Update {
	t.Helper()
	return enginetest.Statement(t, "UPDATE pg_catalog.pg_attribute SET "+assignments+" WHERE attrelid='public.storage_boundary_idx'::regclass AND attnum=1").(*parser.Update)
}

func TestAlterStorageRepairNameByteBoundaries(t *testing.T) {
	for _, assignments := range []string{"attname='valid'", "attstorage='e',attcompression='p',attname='valid'"} {
		for _, tc := range []struct {
			name, value string
			valid       bool
		}{
			{"empty", "", false},
			{"ascii63", strings.Repeat("a", 63), true},
			{"ascii64", strings.Repeat("a", 64), false},
			{"utf8_63_bytes", strings.Repeat("é", 31) + "a", true},
			{"utf8_64_bytes", strings.Repeat("é", 32), false},
			{"nul", "left\x00right", false},
			{"nul_only", "\x00", false},
			{"invalid_lead", string([]byte{0xff}), false},
			{"truncated_utf8", string([]byte{0xe2, 0x82}), false},
			{"overlong_nul", string([]byte{0xc0, 0x80}), false},
			{"quoted_bytes", "Mixed ' \\ name", true},
		} {
			t.Run(assignments+"/"+tc.name, func(t *testing.T) {
				raw := storageBoundaryRepair(t, assignments)
				// Inject decoded bytes at the AST seam: a lexer rejection would
				// not establish validation of an internally supplied repair AST.
				i := len(raw.Assignments) - 1
				literal := raw.Assignments[i].Value.(parser.LiteralExpr)
				literal.Value.Text = tc.value
				raw.Assignments[i].Value = literal
				_, _, value, err := table.RestoreIndexStorageSyntax(raw)
				if tc.valid {
					if err != nil || !value.Rename || value.Name != tc.value {
						t.Fatal("valid name bytes changed or rejected", value, err)
					}
				} else {
					enginetest.State(t, err, "0A000")
					if err.Error() != "stuntgres: "+table.RestoreIndexStorageBoundary {
						t.Fatal("unexpected boundary diagnostic", err)
					}
				}
			})
		}
	}
}

func TestAlterStorageRepairAuthorityRejections(t *testing.T) {
	for _, kind := range []string{
		"foreign_operation", "nonsuperuser_actor", "nonsuperuser_authenticator",
		"cancel_before", "cancel_during_validation", "role_invalidation",
		"session_invalidation", "session_end", "stale_catalog", "revision_exhaustion",
	} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New().RestoreStage()
			enginetest.Run(t, e, "CREATE TABLE storage_boundary(a text,b text)")
			enginetest.Run(t, e, "INSERT INTO storage_boundary VALUES('left','right')")
			enginetest.Run(t, e, "CREATE INDEX storage_boundary_idx ON storage_boundary(a,b)")
			enginetest.Run(t, e, "CREATE USER storage_boundary_low")
			tableRef := enginetest.RoleTableRef(t, e, "storage_boundary")
			indexRef := enginetest.RoleTableRef(t, e, "storage_boundary_idx")
			if kind == "revision_exhaustion" {
				exhausted := *e.Catalog.State.MustRelation(indexRef.Address)
				exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
				e.Catalog.State.SetDescriptor(indexRef.Address, &exhausted)
			}
			read := e.CatalogRead()
			old := read.MustRelation(indexRef.Address)
			parent := read.MustRelation(tableRef.Address)
			columns, lookup := slices.Clone(old.Columns), maps.Clone(old.Index)
			policies := maps.Clone(old.ColumnMeta.Storage)
			revision, parentRevision := old.Revision(), parent.Revision()
			state := e.Catalog.State
			expectedIndex := old
			expectedParent := parent
			expectedIndexRevision := old.Revision()
			store, root := e.Storage.Relations[tableRef.Address], e.Storage.IndexRoots[indexRef.Address]
			ends, generation := store.Ends, store.Generation
			op, done := roleOperation(t, e, "postgres")
			defer done()
			switch kind {
			case "foreign_operation":
				foreign := engine.New().RestoreStage()
				foreignOp, closeForeign := roleOperation(t, foreign, "postgres")
				defer closeForeign()
				op = foreignOp
			case "nonsuperuser_actor", "nonsuperuser_authenticator":
				low, err := e.ResolveLogin("storage_boundary_low")
				if err != nil {
					t.Fatal(err)
				}
				op.Session.Mu.Lock()
				if kind == "nonsuperuser_actor" {
					op.Session.Authorization.State.CurrentUser = low
				} else {
					op.Session.Authorization.State.Authenticated = low
				}
				op.Session.Authorization.Revision++
				op.Session.Mu.Unlock()
			case "cancel_before":
				op.Cancel(context.Canceled)
			}
			provider := e.Catalog.Providers[ids.RelationClassID]
			switch kind {
			case "cancel_during_validation", "role_invalidation", "session_invalidation", "session_end", "stale_catalog":
				// The real provider validates the actual candidate. Its existing
				// wrapper opens a deterministic publication frontier, not a mock
				// replacement for repair semantics or catalog publication.
				e.Catalog.Providers[ids.RelationClassID] = alterStorageVetoProvider{
					Provider: provider,
					run: func() {
						e.Catalog.Providers[ids.RelationClassID] = provider
						switch kind {
						case "cancel_during_validation":
							op.Cancel(context.Canceled)
						case "role_invalidation":
							enginetest.Run(t, e, "CREATE USER storage_boundary_changed")
						case "session_invalidation":
							op.Session.Mu.Lock()
							op.Session.Authorization.Revision++
							op.Session.Mu.Unlock()
						case "session_end":
							op.Session.RequestEnd(session.CleanClose)
						case "stale_catalog":
							// Replace this exact predecessor while validation is
							// unlocked; unrelated catalog changes are not conflicts.
							enginetest.Run(t, e, "ALTER TABLE storage_boundary ALTER b SET STORAGE MAIN")
							expectedIndex = e.Catalog.State.MustRelation(indexRef.Address)
							expectedParent = e.Catalog.State.MustRelation(tableRef.Address)
							expectedIndexRevision = expectedIndex.Revision()
						}
					},
					failure: func() error { return nil },
				}
			}
			err := tablecmds.RestoreIndexStorage(e, op, storageBoundaryRepair(t, "attstorage='e',attcompression='p',attname='repaired'"))
			e.Catalog.Providers[ids.RelationClassID] = provider
			if err == nil {
				t.Fatal("rejected repair published", kind)
			}
			if strings.HasPrefix(kind, "cancel_") && !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation", err)
			}
			if kind == "nonsuperuser_actor" {
				enginetest.State(t, err, "42501")
				if err.Error() != "stuntgres: permission denied for table pg_attribute" {
					t.Fatal(err)
				}
			}
			if kind == "revision_exhaustion" && err.Error() != "stuntgres: definition revision exhausted" {
				t.Fatal(err)
			}
			if kind != "stale_catalog" && kind != "role_invalidation" && e.Catalog.State != state {
				t.Fatal("rejected repair replaced catalog state", err)
			}
			if e.Catalog.State.MustRelation(indexRef.Address) != expectedIndex || e.Catalog.State.MustRelation(tableRef.Address) != expectedParent || expectedIndex.Revision() != expectedIndexRevision || read.MustRelation(indexRef.Address) != old || old.Revision() != revision || parent.Revision() != parentRevision || !reflect.DeepEqual(old.Columns, columns) || !reflect.DeepEqual(old.Index, lookup) || !reflect.DeepEqual(old.ColumnMeta.Storage, policies) {
				t.Fatal("rejected repair mutated predecessors, names, policies or revisions", err)
			}
			if old.Index["a"] != 0 || old.Index["b"] != 1 {
				t.Fatal("predecessor lookup no longer resolves original slots")
			}
			if _, exists := old.Index["repaired"]; exists {
				t.Fatal("rejected name leaked into predecessor lookup")
			}
			if e.Storage.IndexRoots[indexRef.Address] != root || e.Storage.Relations[tableRef.Address] != store || store.Ends != ends || store.Generation != generation || len(store.Versions) != 1 {
				t.Fatal("rejected repair rewrote executable roots or row backing", err)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

// Captured authority must not replace a revised predecessor at the same
// address. Exercise the exact publication tail used by RestoreIndexStorage.
func TestAlterStorageRepairStaleCapturedAuthority(t *testing.T) {
	e := engine.New().RestoreStage()
	enginetest.Run(t, e, "CREATE TABLE storage_boundary(a text)")
	enginetest.Run(t, e, "CREATE INDEX storage_boundary_idx ON storage_boundary(a)")
	ref := enginetest.RoleTableRef(t, e, "storage_boundary_idx")
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	root := e.Storage.IndexRoots[ref.Address]
	next := old.WithColumnStorage(0, catalog.ColumnStorage{Storage: "e", Compression: "p"})
	if err := table.BumpAlterRevision(old, next); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "ALTER TABLE storage_boundary ALTER a SET STORAGE MAIN")
	current := e.Catalog.State.MustRelation(ref.Address)
	currentRevision := current.Revision()
	state := e.Catalog.State
	e.Mu.Lock()
	err = tablecmds.PublishRestoreRepairLocked(e, op, auth, next, ref.Epoch)
	if err == nil || e.Catalog.State != state || e.Catalog.State.MustRelation(ref.Address) != current || current.Revision() != currentRevision || current.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "m"}) || current.Index["a"] != 0 || auth.Catalog.MustRelation(ref.Address) != old || old.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "x"}) || e.Storage.IndexRoots[ref.Address] != root {
		t.Fatal("stale captured repair authority published or changed predecessor", err)
	}
	enginetest.LockEmpty(t, e)
}
