package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

func TestDriverBatchMaterializedReplacementRoots(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "existing_", true: "private_"}[private]+map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE INDEX refresh_source_idx ON refresh_source(v)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
					sessionSQL(t, s, q)
				}
				base, frontier := e.Catalog.State, e.Visibility.CommitSeq()
				source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
				index := catalogRef(t, e, e.CatalogRead(), "refresh_source_idx")
				indexRoot := e.Storage.IndexRoots[index.Address]
				m := batchUnitStart(t, s, context.Background())
				defer m.end(context.Canceled)
				name := "refresh_target"
				if private {
					name = "refresh_private"
					if _, err := m.step(statement(t, "CREATE MATERIALIZED VIEW "+name+" AS SELECT v FROM refresh_source WITH DATA")); err != nil {
						t.Fatal(err)
					}
				}
				e.Mu.Lock()
				view := e.PrivateCatalogLocked(s.Transactions.Catalog)
				e.Mu.Unlock()
				target := catalogRef(t, e, view, name)
				original := e.Storage.Relations[target.Address]
				originalDescriptor := e.Catalog.State.Get(target.Address)
				var root []storage.RowVersion
				var ends *storage.DeletionRoot
				var generation uint64
				if original != nil {
					root, ends, generation = original.Versions, original.Ends, original.Generation
				}
				if _, err := m.step(statement(t, "INSERT INTO refresh_source VALUES(2)")); err != nil {
					t.Fatal(err)
				}
				var previous *storage.Relation
				for _, suffix := range []string{"WITH DATA", "WITH NO DATA", "WITH DATA"} {
					e.Mu.Lock()
					entry := e.PrivateStorageLocked(s.Transactions.Catalog, target.Address)
					e.Mu.Unlock()
					if _, err := m.step(statement(t, "REFRESH MATERIALIZED VIEW "+name+" "+suffix)); err != nil {
						t.Fatal(err)
					}
					shape := s.Transactions.Catalog.Storage[target.Address]
					if shape.Original != original || !storage.SameVersionRoot(shape.Root, root) || shape.Ends != ends || shape.Generation != generation || shape.Replacement == entry || shape.Replacement == previous || shape.Replacement.Generation != entry.Generation+1 || !shape.Rewritten || shape.HasSnapshot || shape.Relation != s.Transactions.Catalog.Descriptors[target.Address] {
						t.Fatal("replacement lost first committed or immediate private predecessor", shape)
					}
					if len(s.Transactions.Writes[target.Address]) != 0 {
						t.Fatal("private CREATE initial rows appended to replacement")
					}
					want := 2
					if suffix == "WITH NO DATA" {
						want = 0
					}
					if len(shape.Replacement.Versions) != want || shape.Relation.Populated != (want != 0) {
						t.Fatal("incomplete private population", shape)
					}
					for _, row := range shape.Replacement.Versions {
						if row.Born != 0 {
							t.Fatal("replacement row born before true end", row)
						}
					}
					if e.Catalog.State != base || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address] != original || e.Catalog.State.Get(target.Address) != originalDescriptor || e.Storage.IndexRoots[index.Address] != indexRoot {
						t.Fatal("replacement/source index escaped before message end")
					}
					previous = shape.Replacement
				}
				latest := previous
				if commit {
					if err := m.end(nil); err != nil {
						t.Fatal(err)
					}
					if e.Storage.Relations[target.Address] != latest || e.Visibility.CommitSeq() != frontier+1 || len(e.Storage.Relations[source.Address].Versions) != 2 || len(e.Storage.IndexRoots[index.Address].Entries) != 2 || len(indexRoot.Entries) != 1 {
						t.Fatal("source rows/index and latest replacement did not publish together")
					}
					for _, row := range latest.Versions {
						if row.Born != frontier+1 {
							t.Fatal("replacement row birth is not true-end frontier", row)
						}
					}
				} else {
					_, err := m.step(statement(t, "SELECT missing"))
					if err == nil || m.end(err) != err {
						t.Fatal("late failure disposition", err)
					}
					if e.Catalog.State != base || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address] != original || e.Storage.IndexRoots[index.Address] != indexRoot || len(e.Storage.Relations[source.Address].Versions) != 1 {
						t.Fatal("late failure published replacement/source/index")
					}
					for _, row := range latest.Versions {
						if row.Born != 0 {
							t.Fatal("discarded private replacement acquired birth", row)
						}
					}
				}
				if original != nil && (!storage.SameVersionRoot(original.Versions, root) || original.Ends != ends || original.Generation != generation) {
					t.Fatal("refresh mutated retained predecessor")
				}
			})
		}
	}
}

func TestDriverBatchMaterializedPublicationGuards(t *testing.T) {
	for _, fence := range []string{"cancel", "epoch", "ref", "generation", "ends", "store", "descriptor", "roles", "source_index_generation"} {
		t.Run(fence, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE INDEX refresh_source_idx ON refresh_source(v)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
				sessionSQL(t, s, q)
			}
			target := catalogRef(t, e, e.CatalogRead(), "refresh_target")
			source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
			index := catalogRef(t, e, e.CatalogRead(), "refresh_source_idx")
			original, indexRoot := e.Storage.Relations[target.Address], e.Storage.IndexRoots[index.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := batchUnitStart(t, s, ctx)
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE INDEX refresh_pending_idx ON refresh_source(v)", "INSERT INTO refresh_source VALUES(2)", "REFRESH MATERIALIZED VIEW refresh_target"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(err)
				}
			}
			e.Mu.Lock()
			view := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			pending := catalogRef(t, e, view, "refresh_pending_idx")
			replacement := s.Transactions.Catalog.Storage[target.Address].Replacement
			var err error
			switch fence {
			case "cancel":
				cancel()
			case "epoch":
				next := catalog.CloneState(e.Catalog.State)
				next.SetEpoch(next.Epoch() + 1)
				e.Catalog.PublishLocked(next)
			case "ref":
				stale := s.Transactions.Ref
				stale = withTxID(stale, stale.ID()+1)
				err = e.publishWrites(m.op, stale, s.Transactions.Writes, s.Transactions.Deletes, nil)
				if err == nil {
					t.Fatal("foreign incarnation admitted materialized replacement")
				}
			case "generation":
				original.Generation++
			case "ends":
				original.Ends = &storage.DeletionRoot{}
			case "store":
				copy := *original
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(target.Address, &copy)
				e.Mu.Unlock()
			case "descriptor":
				next := catalog.CloneState(e.Catalog.State)
				copy := *next.MustRelation(target.Address)
				copy.ObjectRevision++
				next.SetDescriptor(target.Address, &copy)
				e.Catalog.PublishLocked(next)
			case "roles":
				copy := *e.RoleStore.Published
				copy.Revision++
				e.RoleStore.PublishLocked(&copy)
			case "source_index_generation":
				e.Storage.Relations[source.Address].Generation++
			}
			state, store, frontier := e.Catalog.State, e.Storage.Relations[target.Address], e.Visibility.CommitSeq()
			err = m.end(err)
			if err == nil || e.Catalog.State != state || e.Storage.Relations[target.Address] != store || e.Visibility.CommitSeq() != frontier || e.Storage.IndexRoots[index.Address] != indexRoot || e.Storage.IndexRoots[pending.Address] != nil || len(e.Storage.Relations[source.Address].Versions) != 1 {
				t.Fatal("rejected replacement published rows/catalog/index", err)
			}
			if fence == "cancel" && !errors.Is(err, context.Canceled) || fence == "epoch" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("publication primary changed", err)
			}
			for _, row := range replacement.Versions {
				if row.Born != 0 {
					t.Fatal("failed final fence stamped private rows", row)
				}
			}
		})
	}
}

func TestDriverBatchMaterializedPromotionSavepoint(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
		sessionSQL(t, s, q)
	}
	target := catalogRef(t, e, e.CatalogRead(), "refresh_target")
	source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
	original, frontier := e.Storage.Relations[target.Address], e.Visibility.CommitSeq()
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"INSERT INTO refresh_source VALUES(2)", "REFRESH MATERIALIZED VIEW refresh_target"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	shape := s.Transactions.Catalog.Storage[target.Address]
	ref, ticket, start := s.Transactions.Ref, s.Transactions.Ticket, s.Transactions.Start
	for _, q := range []string{"BEGIN", "SAVEPOINT kept", "INSERT INTO refresh_source VALUES(3)"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.step(statement(t, "SELECT missing"))
	if err == nil || m.end(err) != err || s.Transactions.State != session.TxAborted {
		t.Fatal("promoted mark did not preserve failed-block recovery", err)
	}
	sessionSQL(t, s, "ROLLBACK TO kept")
	restored := s.Transactions.Catalog.Storage[target.Address]
	if s.Transactions.State != session.TxActive || s.Transactions.Ref != ref || s.Transactions.Ticket != ticket || s.Transactions.Start != start || restored.Original != shape.Original || restored.Replacement != shape.Replacement || restored.Generation != shape.Generation || restored.Ends != shape.Ends || !restored.Rewritten || len(s.Transactions.Writes[source.Address]) != 1 || e.Storage.Relations[target.Address] != original || e.Visibility.CommitSeq() != frontier {
		t.Fatal("savepoint recovery rebuilt or published retained replacement authority")
	}
	sessionSQL(t, s, "COMMIT")
	if e.Storage.Relations[target.Address] != shape.Replacement || len(shape.Replacement.Versions) != 2 || e.Visibility.CommitSeq() != frontier+1 {
		t.Fatal("promoted replacement did not survive savepoint publication")
	}
	for _, row := range shape.Replacement.Versions {
		if row.Born != frontier+1 {
			t.Fatal("promoted replacement row birth preceded commit", row)
		}
	}
}

func TestDriverBatchProducerRoleAuthorityLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		producer    bool
	}{
		{"table", "CREATE TABLE produced AS SELECT v FROM refresh_source", true},
		{"empty_table", "CREATE TABLE produced AS SELECT v FROM refresh_source WHERE false", true},
		{"table_no_data", "CREATE TABLE produced AS SELECT v FROM refresh_source WITH NO DATA", false},
		{"materialized", "CREATE MATERIALIZED VIEW produced AS SELECT v FROM refresh_source", true},
		{"empty_materialized", "CREATE MATERIALIZED VIEW produced AS SELECT v FROM refresh_source WHERE false", true},
		{"materialized_no_data", "CREATE MATERIALIZED VIEW produced AS SELECT v FROM refresh_source WITH NO DATA", false},
		{"refresh", "REFRESH MATERIALIZED VIEW refresh_target", true},
		{"refresh_no_data", "REFRESH MATERIALIZED VIEW refresh_target WITH NO DATA", true},
	} {
		for _, own := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/peer", true: "/own"}[own], func(t *testing.T) {
				e := New()
				s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
					sessionSQL(t, s, q)
				}
				base, roles, frontier := e.Catalog.State, e.RoleStore.Published, e.Visibility.CommitSeq()
				target := catalogRef(t, e, e.CatalogRead(), "refresh_target")
				original := e.Storage.Relations[target.Address]
				m := batchUnitStart(t, s, context.Background())
				defer m.end(context.Canceled)
				if _, err := m.step(statement(t, tc.query)); err != nil {
					t.Fatal(err)
				}
				journal := s.Transactions.Catalog
				if journal == nil || journal.RolesBase != nil || journal.Roles != nil {
					t.Fatal("producer read acquired private role-write authority")
				}
				fenced := false
				for _, fence := range journal.Fences {
					if read, ok := fence.(catalogRoleReadFence); ok {
						fenced = true
						if read.roles != roles {
							t.Fatal("producer recaptured its role predecessor")
						}
					}
				}
				if fenced != tc.producer {
					t.Fatal("role read lifetime does not match pending producer", fenced)
				}
				if own {
					if _, err := m.step(statement(t, "CREATE ROLE producer_own")); err != nil {
						t.Fatal(err)
					}
					if journal.RolesBase != nil || s.Transactions.Catalog.RolesBase != roles || s.Transactions.Catalog.Roles == nil {
						t.Fatal("real role write lost baseline or mutated retained producer journal")
					}
				} else {
					sessionSQL(t, peer, "CREATE ROLE producer_peer")
					roles, frontier = e.RoleStore.Published, e.Visibility.CommitSeq()
				}
				err := m.end(nil)
				if !own && tc.producer {
					if err == nil || e.Catalog.State != base || e.RoleStore.Published != roles || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address] != original {
						t.Fatal("stale producer role read published or overwrote peer", err)
					}
					if _, ok := e.Roles().Lookup("producer_peer"); !ok {
						t.Fatal("producer conflict lost peer role")
					}
					return
				}
				if err != nil {
					t.Fatal("compatible publication rejected", err)
				}
				role := map[bool]string{false: "producer_peer", true: "producer_own"}[own]
				if _, ok := e.Roles().Lookup(role); !ok {
					t.Fatal("successful publication lost role", role)
				}
				if e.Catalog.State == base || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal("producer catalog did not publish once")
				}
				name := "produced"
				if tc.name == "refresh" || tc.name == "refresh_no_data" {
					name = "refresh_target"
				}
				want := []int64{1}
				if !tc.producer || tc.name == "empty_table" || tc.name == "empty_materialized" || tc.name == "refresh_no_data" {
					want = nil
				}
				if tc.name == "materialized_no_data" || tc.name == "refresh_no_data" {
					if e.CatalogRead().MustRelation(catalogRef(t, e, e.CatalogRead(), name).Address).Populated {
						t.Fatal("NO DATA published a populated materialized view")
					}
				} else {
					transactionRows(t, peer, "SELECT v FROM "+name, want...)
				}
			})
		}
	}
}

func TestDriverBatchProducerRoleAuthoritySavepoint(t *testing.T) {
	e := New()
	s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE source(v INT)", "INSERT INTO source VALUES(1)", "CREATE INDEX source_idx ON source(v)", "BEGIN", "ALTER INDEX source_idx SET(fillfactor=80)", "SAVEPOINT before_producer"} {
		sessionSQL(t, s, q)
	}
	metadata := s.Transactions.Catalog
	sessionSQL(t, s, "CREATE TABLE discarded AS SELECT v FROM source")
	producer := s.Transactions.Catalog
	if producer == metadata {
		t.Fatal("producer mutated metadata journal")
	}
	sessionSQL(t, s, "ROLLBACK TO before_producer")
	if s.Transactions.Catalog != metadata {
		t.Fatal("savepoint did not restore producer-free journal")
	}
	// The discarded immutable journal still owns its producer read; it must
	// neither leak into restored metadata nor be cleared by rollback.
	found := false
	for _, fence := range producer.Fences {
		if fence.Kind() == fenceCatalogRoleRead {
			found = true
		}
	}
	if !found || producer.RolesBase != nil {
		t.Fatal("discarded producer lost read authority or acquired a role write")
	}
	for _, fence := range metadata.Fences {
		if fence.Kind() == fenceCatalogRoleRead {
			t.Fatal("producer read escaped its savepoint lifetime")
		}
	}
	sessionSQL(t, peer, "INSERT INTO source VALUES(2)")
	sessionSQL(t, peer, "CREATE ROLE after_discard")
	sessionSQL(t, s, "INSERT INTO source VALUES(3)")
	sessionSQL(t, s, "COMMIT")
	transactionRows(t, peer, "SELECT v FROM source ORDER BY v", 1, 2, 3)
	view := e.CatalogRead()
	idx := view.MustRelation(catalogRef(t, e, view, "source_idx").Address)
	if len(idx.StorageOptions.Base) != 1 || idx.StorageOptions.Base[0].Text != "80" {
		t.Fatal("producer rollback discarded earlier maintenance")
	}
	if _, ok := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: idx.Namespace(), Name: "discarded"}); ok {
		t.Fatal("discarded producer table published")
	}
	if _, ok := e.Roles().Lookup("after_discard"); !ok {
		t.Fatal("metadata commit overwrote peer role")
	}
}
