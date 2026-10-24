package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

func TestDriverBatchCatalogInitialTablesAndProviders(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			before, frontier := e.catalog.State, e.visibility.CommitSeq()
			original := e.catalog.Providers[ids.RelationClassID]
			defer func() { e.catalog.Providers[ids.RelationClassID] = original }()
			calls := 0
			e.catalog.Providers[ids.RelationClassID] = alterSchemaCandidateProvider{original, func(candidate catalog.Read) {
				calls++
				if !e.mu.TryLock() {
					t.Error("catalog provider under Engine.mu")
				} else {
					if e.catalog.State != before || e.visibility.CommitSeq() != frontier {
						t.Error("utility escaped before final publication")
					}
					e.mu.Unlock()
				}
				if !s.Mu.TryLock() {
					t.Error("catalog provider under Session.mu")
				} else {
					s.Mu.Unlock()
				}
				if !s.Transactions.Mu.TryLock() {
					t.Error("catalog provider under transaction owner mutex")
				} else {
					s.Transactions.Mu.Unlock()
				}
				if candidate.State() == before {
					t.Error("provider received committed root instead of full private candidate")
				}
			}}
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE SEQUENCE batch_s START 5", "CREATE TABLE ct AS SELECT nextval('batch_s') AS x", "CREATE MATERIALIZED VIEW mv AS SELECT x FROM ct", "ALTER TABLE ct ADD y INT DEFAULT 4", "CREATE UNIQUE INDEX ci ON ct(x)", "COMMENT ON COLUMN ct.y IS 'pending'"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(q, err)
				}
			}
			result, err := m.step(statement(t, "SELECT x,y FROM ct"))
			if err != nil {
				t.Fatal(err)
			}
			defer result.Rows.Close()
			s.Transactions.Mu.Lock()
			c := s.Transactions.Catalog
			writes := s.Transactions.Writes
			s.Transactions.Mu.Unlock()
			e.mu.Lock()
			private := e.privateCatalogLocked(c)
			e.mu.Unlock()
			ns := private.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
			ct := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ct"})
			mv := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "mv"})
			seq := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "batch_s"})
			idx := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ci"})
			if c == nil || ct == (ids.ObjectAddress{}) || mv == (ids.ObjectAddress{}) || seq == (ids.ObjectAddress{}) || idx == (ids.ObjectAddress{}) || c.Generators[seq] == nil || c.Storage[ct].Original != nil || c.Storage[mv].Original != nil || len(c.Storage[ct].Replacement.Versions) != 0 || len(c.Storage[mv].Replacement.Versions) != 0 || len(writes[ct]) != 1 || len(writes[mv]) != 1 || len(index.JournalIndexesOrZero(c).UniqueRoots[idx].Entries) != 0 || calls == 0 {
				t.Fatalf("catalog stage fields or initial rows omitted: ct=%v mv=%v sequence=%v index=%v generator=%v ctShape=%+v mvShape=%+v ctWrites=%d mvWrites=%d indexRoot=%v calls=%d", ct, mv, seq, idx, c.Generators[seq], c.Storage[ct], c.Storage[mv], len(writes[ct]), len(writes[mv]), index.JournalIndexesOrZero(c).UniqueRoots[idx], calls)
			}
			// CREATE of one index on a private parent captures committed storage
			// absence, not REINDEX TABLE's committed membership-set authority.
			absence := false
			for _, f := range catalogFencesOf[session.StorageReadFence](c) {
				if f.Address() == ct && f.Matches(nil) {
					absence = true
				}
			}
			if !absence {
				t.Fatal("new-index committed source-absence fence omitted")
			}
			if e.catalog.State != before || e.visibility.CommitSeq() != frontier || e.storage.Relations[ct] != nil || e.storage.Relations[mv] != nil || e.storage.Sequences[seq] != nil {
				t.Fatal("initial data or generators published before end")
			}
			if commit {
				if err = m.end(nil); err != nil {
					t.Fatal(err)
				}
			} else {
				_, err = m.step(statement(t, "SELECT missing"))
				if err == nil {
					t.Fatal("missing suffix failure")
				}
				if end := m.end(err); end != err {
					t.Fatal(end)
				}
			}
			e.catalog.Providers[ids.RelationClassID] = original
			if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(5), int64(4)}}) {
				t.Fatal("private initial rows and row-shape result", got)
			}
			if commit {
				if e.visibility.CommitSeq() != frontier+1 || len(e.storage.Relations[ct].Versions) != 1 || len(e.storage.Relations[mv].Versions) != 1 || e.storage.Sequences[seq] == nil || len(e.storage.IndexRoots[idx].Entries) != 1 {
					t.Fatal("joint initial rows/shape/sequence/index installation")
				}
				transactionRows(t, s, "SELECT x FROM ct", 5)
				transactionRows(t, s, "SELECT x FROM mv", 5)
			} else {
				if e.catalog.State != before || e.visibility.CommitSeq() != frontier || e.storage.Relations[ct] != nil || e.storage.Relations[mv] != nil || e.storage.Sequences[seq] != nil || e.storage.IndexRoots[idx] != nil {
					t.Fatal("initial table or provider result escaped rollback")
				}
			}
		})
	}
}
