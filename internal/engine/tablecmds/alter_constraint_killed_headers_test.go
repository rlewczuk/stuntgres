package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Public reference cases own values and diagnostics. This composed invariant
// distinguishes ephemeral historical projection from mutating captured heap,
// repaired-payload and genuine retained-index backing during ALTER publication.
func TestAlterConstraintKilledHeaderOwnership(t *testing.T) {
	for _, shape := range []string{"plain", "add", "drop"} {
		for _, terminal := range []string{"success", "cancel"} {
			t.Run(shape+"/"+terminal, func(t *testing.T) {
				e := engine.New()
				s := sessionStart(t, e, nil)
				for _, q := range []string{
					"CREATE EXTENSION pg_surgery",
					"CREATE TABLE t(k INT,v TEXT,scratch INT DEFAULT 9,CONSTRAINT keep_positive CHECK(k>0))",
					"INSERT INTO t(k,v) VALUES(1,NULL),(2,'survivor')",
					"CREATE INDEX killed_header_idx ON t(k) WHERE k>0",
				} {
					sessionSQL(t, s, q)
				}
				old := alterConstraintDescriptor(t, e, "t")
				store := e.Storage.Relations[old.Address()]
				if len(store.Versions) != 2 || !store.Versions[0].Values[1].IsNull() {
					t.Fatal("missing genuine violating source")
				}
				preSurgery := *store
				block, offset, err := storage.LogicalTID(store.Versions[0].Version)
				if err != nil {
					t.Fatal(err)
				}
				tids := fmt.Sprintf(`ARRAY['(%d,%d)'::tid]`, block, offset)
				sessionSQL(t, s, "SELECT heap_force_freeze('t'::regclass,"+tids+")")
				sessionSQL(t, s, "SELECT heap_force_kill('t'::regclass,"+tids+")")
				headers, versions, ends, generation := store.Headers, store.Versions, store.Ends, store.Generation
				if headers == nil || len(headers.Additional) != 1 || !headers.Apply(versions[0]).HeapKilled || versions[0].HeapKilled || preSurgery.Headers.Apply(preSurgery.Versions[0]).HeapKilled {
					t.Fatal("surgery did not retain independent immutable header/data captures")
				}
				additional := headers.Additional
				indexRef := catalogRef(t, e, e.CatalogRead(), "killed_header_idx")
				root := e.Storage.IndexRoots[indexRef.Address]
				if root == nil || len(root.Entries) != 2 || root.MembershipLen() != 2 || !root.IsMember(versions[0].Row, versions[0].Version) {
					t.Fatal("missing genuine retained killed-target membership")
				}
				entries, key := root.Entries, root.Entries[0].Keys[0]
				catalogRoot := e.Catalog.State
				q := "ALTER TABLE t DROP CONSTRAINT keep_positive,ALTER v SET NOT NULL"
				if shape == "add" {
					q += ",ADD COLUMN extra INT DEFAULT 7"
				} else if shape == "drop" {
					q += ",DROP COLUMN scratch"
				}
				scope, err := alterConstraintExecuteScope(t, e, s, q)
				if err != nil {
					t.Fatal(err)
				}
				if e.Catalog.State != catalogRoot || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[indexRef.Address] != root {
					t.Fatal("detached physical work published before Finish")
				}
				assertCaptured := func() {
					t.Helper()
					if store.Headers != headers || store.Ends != ends || store.Generation != generation || !storage.SameVersionRoot(store.Versions, versions) || versions[0].HeapKilled || &versions[0].Values[0] != &preSurgery.Versions[0].Values[0] || len(versions[0].Values) != 3 {
						t.Fatal("ALTER mutated captured source storage or payload layout")
					}
					if &headers.Additional[0] != &additional[0] || additional[0].HeapKilled || !additional[0].Values[1].IsNull() || !headers.Apply(additional[0]).HeapKilled || headers.Header(versions[0].Version).Slot != storage.HeapSlotDead {
						t.Fatal("ALTER materialized death into immutable repaired backing")
					}
					if &root.Entries[0] != &entries[0] || root.MembershipLen() != 2 || !root.IsMember(versions[0].Row, versions[0].Version) || !sqltype.SameDatum(root.Entries[0].Keys[0], key) {
						t.Fatal("ALTER mutated captured genuine index backing")
					}
				}
				assertCaptured()
				var finishErr error
				if terminal == "cancel" {
					finishErr = scope.Finish(context.Canceled)
					if !errors.Is(finishErr, context.Canceled) || e.Catalog.State != catalogRoot || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[indexRef.Address] != root {
						t.Fatal("rejected Finish changed committed publication roots", finishErr)
					}
				} else {
					finishErr = scope.Finish(nil)
					if finishErr != nil || e.Catalog.State == catalogRoot || alterConstraintDescriptor(t, e, "t") == old {
						t.Fatal("successful Finish did not publish successor metadata", finishErr)
					}
				}
				scope.Op.Done()
				assertCaptured()
				if old.Columns[1].Nullable != true || len(old.Constraints.NotNull) != 0 || len(old.Constraints.Check) != 1 {
					t.Fatal("publication mutated the original descriptor")
				}
				lockEmpty(t, e)
			})
		}
	}
}
