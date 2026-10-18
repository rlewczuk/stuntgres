package engine

import (
	"context"
	"slices"
	"testing"

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
			store, root := e.storage[parent], e.indexRoots[index]
			beforeRows := slices.Clone(store.versions)
			beforeCount := e.sequences[sequence].last
			if beforeCount != 2 {
				t.Fatal("setup did not produce exactly two CHECK events", beforeCount)
			}
			raw := statement(t, "CREATE DOMAIN public.unused AS integer CONSTRAINT never_replay CHECK(FALSE)").(*parser.CreateDomain)
			raw.Constraints[0].Check.NotValid = true
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			stage := e.BeginCatalogStage(auth.catalog)
			next := *d
			next.domainChecks = slices.Clone(d.domainChecks)
			if err == nil {
				err = e.bindDomainChecks(op, auth, stage, &next, raw.Constraints, sqltype.InputEnvironment{})
			}
			if err == nil {
				next.typ, err = sqltype.Domain(d.typ.ID(), d.Name(), d.typ.SQLName(), d.typ.DomainBase(), true)
			}
			if err == nil {
				next.revision, err = e.catalog.revisionLocked()
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
			if e.sequences[sequence].last != beforeCount {
				t.Fatal("generation remap replayed SQL truth")
			}
			current := e.storage[parent]
			if len(current.versions) != len(beforeRows) || current.ends != store.ends || current.generation != store.generation {
				t.Fatal("transport lost all-version storage lineage")
			}
			if definition.name == "domain_key" && current == store {
				t.Fatal("changed domain datums retained their old storage generation")
			}
			// Committed rows intentionally drop pending indexOutputs at commit.
			// An expression-only integer parent can share its unchanged immutable
			// row storage; forcing a wrapper allocation would prove nothing.
			for i, row := range beforeRows {
				if row.row != store.versions[i].row || row.version != store.versions[i].version || row.values[0] != store.versions[i].values[0] {
					t.Fatal("source row mutated")
				}
				if current.versions[i].row != row.row || current.versions[i].version != row.version || current.versions[i].born != row.born {
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
				usage, err := indexKeyUsage(currentParent, currentIndex.SQLIndex.keys[position])
				if err != nil || current.Type() != usage.Type {
					t.Fatal("index key retained stale logical conversion authority", err)
				}
			}
			newRoot := e.indexRoots[index]
			if root.hash == nil {
				if len(newRoot.entries) != len(root.entries) {
					t.Fatal("B-tree membership changed")
				}
				for i, entry := range root.entries {
					next := newRoot.entries[i]
					if entry.row != next.row || entry.version != next.version || entry.position != next.position || len(entry.keys) != len(next.keys) {
						t.Fatal("B-tree row identities changed")
					}
					for key, value := range entry.keys {
						sameKey(value, next.keys[key], key)
					}
				}
			} else {
				if newRoot.hash.storage != current {
					t.Fatal("hash storage identity changed")
				}
				entries := make(map[indexRowIdentity]hashIndexEntry)
				for _, bucket := range newRoot.hash.buckets {
					for _, entry := range bucket {
						entries[indexRowIdentity{row: entry.row, version: entry.version}] = entry
					}
				}
				count := 0
				for _, bucket := range root.hash.buckets {
					for _, entry := range bucket {
						next, ok := entries[indexRowIdentity{row: entry.row, version: entry.version}]
						if !ok || next.position != entry.position {
							t.Fatal("hash row membership changed")
						}
						sameKey(entry.key, next.key, 0)
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
