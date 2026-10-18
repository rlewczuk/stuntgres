package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestDriverBatchCatalogInitialTablesAndProviders(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			before, frontier := e.catalog.state, e.commitSeq
			original := e.catalog.providers[ids.RelationClassID]
			defer func() { e.catalog.providers[ids.RelationClassID] = original }()
			calls := 0
			e.catalog.providers[ids.RelationClassID] = alterSchemaCandidateProvider{original, func(candidate CatalogRead) {
				calls++
				if !e.mu.TryLock() {
					t.Error("catalog provider under Engine.mu")
				} else {
					if e.catalog.state != before || e.commitSeq != frontier {
						t.Error("utility escaped before final publication")
					}
					e.mu.Unlock()
				}
				if !s.mu.TryLock() {
					t.Error("catalog provider under Session.mu")
				} else {
					s.mu.Unlock()
				}
				if !s.transactions.mu.TryLock() {
					t.Error("catalog provider under transaction owner mutex")
				} else {
					s.transactions.mu.Unlock()
				}
				if candidate.state == before {
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
			s.transactions.mu.Lock()
			c := s.transactions.catalog
			writes := s.transactions.writes
			s.transactions.mu.Unlock()
			e.mu.Lock()
			private := e.privateCatalogLocked(c)
			e.mu.Unlock()
			ns := private.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"}]
			ct := private.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ct"}]
			mv := private.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "mv"}]
			seq := private.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "batch_s"}]
			index := private.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: "ci"}]
			if c == nil || ct == (ids.ObjectAddress{}) || mv == (ids.ObjectAddress{}) || seq == (ids.ObjectAddress{}) || index == (ids.ObjectAddress{}) || c.generators[seq] == nil || c.storage[ct].original != nil || c.storage[mv].original != nil || len(c.storage[ct].replacement.versions) != 0 || len(c.storage[mv].replacement.versions) != 0 || len(writes[ct]) != 1 || len(writes[mv]) != 1 || len(c.uniqueRoots[index].entries) != 0 || calls == 0 {
				t.Fatalf("catalog stage fields or initial rows omitted: ct=%v mv=%v sequence=%v index=%v generator=%v ctShape=%+v mvShape=%+v ctWrites=%d mvWrites=%d indexRoot=%v calls=%d", ct, mv, seq, index, c.generators[seq], c.storage[ct], c.storage[mv], len(writes[ct]), len(writes[mv]), c.uniqueRoots[index], calls)
			}
			// CREATE of one index on a private parent captures committed storage
			// absence, not REINDEX TABLE's committed membership-set authority.
			absence := false
			for _, f := range c.readFences {
				if f.address == ct && f.storage == nil {
					absence = true
				}
			}
			if !absence {
				t.Fatal("new-index committed source-absence fence omitted")
			}
			if e.catalog.state != before || e.commitSeq != frontier || e.storage[ct] != nil || e.storage[mv] != nil || e.sequences[seq] != nil {
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
			e.catalog.providers[ids.RelationClassID] = original
			if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(5), int64(4)}}) {
				t.Fatal("private initial rows and row-shape result", got)
			}
			if commit {
				if e.commitSeq != frontier+1 || len(e.storage[ct].versions) != 1 || len(e.storage[mv].versions) != 1 || e.sequences[seq] == nil || len(e.indexRoots[index].entries) != 1 {
					t.Fatal("joint initial rows/shape/sequence/index installation")
				}
				transactionRows(t, s, "SELECT x FROM ct", 5)
				transactionRows(t, s, "SELECT x FROM mv", 5)
			} else {
				if e.catalog.state != before || e.commitSeq != frontier || e.storage[ct] != nil || e.storage[mv] != nil || e.sequences[seq] != nil || e.indexRoots[index] != nil {
					t.Fatal("initial table or provider result escaped rollback")
				}
			}
		})
	}
}
