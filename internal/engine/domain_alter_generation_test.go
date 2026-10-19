package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Setup uses the existing private engine fixture; assertions concern actual
// immutable produced roots/outputs and the package's generation transport seam.
func domainAlterGenerationIndexes(t *testing.T) {
	for _, definition := range []struct{ name, table, index string }{
		{"btree_expression", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject(((id::public.d)::integer))"},
		{"hash_expression", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject USING hash(((id::public.d)::integer))"},
		{"predicate", "CREATE TABLE public.subject(id integer)", "CREATE INDEX checked_idx ON public.subject(id) WHERE (id::public.d)::integer>0"},
		{"domain_key", "CREATE TABLE public.subject(id public.d)", "CREATE INDEX checked_idx ON public.subject(id)"},
	} {
		t.Run(definition.name, func(t *testing.T) {
			e := New()
			for _, q := range []string{"CREATE SEQUENCE public.steps", "CREATE DOMAIN public.d AS integer CONSTRAINT counted CHECK(nextval('public.steps')>0)", definition.table, definition.index, "INSERT INTO public.subject VALUES(1),(2)", "DELETE FROM public.subject WHERE id=1"} {
				run(t, e, q)
			}
			op, done := roleOperation(t, e, "postgres")
			defer done()
			view := e.CatalogRead()
			d := domainUnitType(t, view, "d")
			sequence := roleTableRef(t, e, "steps").Address
			parent := roleTableRef(t, e, "subject").Address
			index := roleTableRef(t, e, "checked_idx").Address
			store, root := e.storage.Relations[parent], e.storage.IndexRoots[index]
			beforeRows := slices.Clone(store.Versions)
			beforeCount := e.storage.Sequences[sequence].Last
			if beforeCount != 2 {
				t.Fatal("setup did not produce exactly two CHECK events", beforeCount)
			}
			raw := statement(t, "CREATE DOMAIN public.unused AS integer CONSTRAINT never_replay CHECK(FALSE)").(*parser.CreateDomain)
			raw.Constraints[0].Check.NotValid = true
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			stage := e.BeginCatalogStage(auth.Catalog)
			next := *d
			next.DomainChecks = slices.Clone(d.DomainChecks)
			if err == nil {
				err = e.bindDomainChecks(op, auth, stage, &next, raw.Constraints, sqltype.InputEnvironment{})
			}
			if err == nil {
				next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), true)
			}
			if err == nil {
				next.ObjectRevision, err = e.catalog.revisionLocked()
			}
			if err == nil {
				err = stage.Replace(&next)
			}
			stage.domainAlter = context.Background()
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			domainUnitPublish(t, e, stage)
			if e.storage.Sequences[sequence].Last != beforeCount {
				t.Fatal("generation remap replayed SQL truth")
			}
			current := e.storage.Relations[parent]
			if len(current.Versions) != len(beforeRows) || current.Ends != store.Ends || current.Generation != store.Generation {
				t.Fatal("transport lost all-version storage lineage")
			}
			if definition.name == "domain_key" && current == store {
				t.Fatal("changed domain datums retained their old storage generation")
			}
			// Committed rows intentionally drop pending indexOutputs at commit.
			// An expression-only integer parent can share its unchanged immutable
			// row storage; forcing a wrapper allocation would prove nothing.
			for i, row := range beforeRows {
				if row.Row != store.Versions[i].Row || row.Version != store.Versions[i].Version || row.Values[0] != store.Versions[i].Values[0] {
					t.Fatal("source row mutated")
				}
				if current.Versions[i].Row != row.Row || current.Versions[i].Version != row.Version || current.Versions[i].Born != row.Born {
					t.Fatal("physical version identity/order changed")
				}
			}
			currentIndex := e.catalog.state.MustRelation(index)
			currentParent := e.catalog.state.MustRelation(parent)
			sameKey := func(old, current sqltype.Value, position int) {
				t.Helper()
				before, err := old.BaseValue()
				if err != nil {
					t.Fatal(err)
				}
				after, err := current.BaseValue()
				if err != nil || before != after {
					t.Fatal("index scalar payload changed", before, after, err)
				}
				usage, err := indexKeyUsage(currentParent, currentIndex.SQLIndex.Keys[position])
				if err != nil || current.Type() != usage.Type {
					t.Fatal("index key retained stale logical conversion authority", err)
				}
			}
			newRoot := e.storage.IndexRoots[index]
			if root.Hash == nil {
				if len(newRoot.Entries) != len(root.Entries) {
					t.Fatal("B-tree membership changed")
				}
				for i, entry := range root.Entries {
					next := newRoot.Entries[i]
					if entry.Row != next.Row || entry.Version != next.Version || entry.Position != next.Position || len(entry.Keys) != len(next.Keys) {
						t.Fatal("B-tree row identities changed")
					}
					for key, value := range entry.Keys {
						sameKey(value, next.Keys[key], key)
					}
				}
			} else {
				if newRoot.Hash.Storage != current {
					t.Fatal("hash storage identity changed")
				}
				entries := make(map[indexRowIdentity]storage.HashEntry)
				for _, bucket := range newRoot.Hash.Buckets {
					for _, entry := range bucket {
						entries[indexRowIdentity{row: entry.Row, version: entry.Version}] = entry
					}
				}
				count := 0
				for _, bucket := range root.Hash.Buckets {
					for _, entry := range bucket {
						next, ok := entries[indexRowIdentity{row: entry.Row, version: entry.Version}]
						if !ok || next.Position != entry.Position {
							t.Fatal("hash row membership changed")
						}
						sameKey(entry.Key, next.Key, 0)
						count++
					}
				}
				if len(entries) != count {
					t.Fatal("hash gained unexpected membership")
				}
			}
		})
	}
}
