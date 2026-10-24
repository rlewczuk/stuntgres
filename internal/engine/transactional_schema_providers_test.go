package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTransactionalSchemaPrivateProviders(t *testing.T) {
	for _, stale := range []string{"none", "type", "relation", "foreign"} {
		t.Run(stale, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			ref := transactionBegin(t, owner)
			op := transactionOperation(t, owner)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.gate(&parser.CreateTable{}); err != nil {
				t.Fatal(err)
			}
			defer func() {
				scope.Finish(errors.New("discard private provider unit"))
				op.Done()
				transactionEnd(t, owner, ref, false)
			}()
			before := e.CatalogRead()
			usage := testUsage(e, sqltype.Int4)
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			provider := e.BeginCatalogStage(auth.Catalog)
			source, err := table.NewRelation(ddlHost{e: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "private_composite", auth.State.CurrentUser, []catalog.Column{{Name: "v", Usage: usage, Nullable: true, NullabilityKnown: true}}, map[string]int{"v": 0})
			if err == nil {
				source.RelationKind = catalog.CompositeRelation
				err = e.stageRowTypeLocked(provider, source)
			}
			if err == nil {
				err = provider.Create(source)
			}
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			journal := scope.owner.Catalog.Clone()
			for address, descriptor := range provider.Descriptors() {
				e.catalogChangeLocked(journal, address, descriptor)
			}
			scope.owner.Catalog = journal
			private := e.privateCatalogLocked(journal)
			consumer := e.BeginCatalogStage(private)
			target, err := table.NewRelation(ddlHost{e: e}, source.Namespace(), "private_typed", source.Owner, nil, nil)
			if err == nil {
				target.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
				err = consumer.Create(target)
			}
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			captured := private.MustType(target.OfType)
			if stale == "type" || stale == "relation" {
				next := journal.Clone()
				if stale == "type" {
					changed := *captured
					changed.ObjectRevision++
					e.catalogChangeLocked(next, changed.Address(), &changed)
				} else {
					changed := *source
					changed.ObjectRevision++
					e.catalogChangeLocked(next, changed.Address(), &changed)
				}
				scope.owner.Catalog = next
			}
			e.mu.Unlock()
			if stale == "foreign" {
				consumer.Base = New().CatalogRead()
			}
			defer e.DiscardCatalogStage(provider)
			defer e.DiscardCatalogStage(consumer)
			err = scope.acquireTypedSources(consumer, false)
			if stale == "none" && err != nil {
				t.Fatal("private reciprocal provider rejected", err)
			}
			if stale == "type" || stale == "relation" {
				if _, ok := err.(catalog.Conflict); !ok {
					t.Fatal("stale provider admitted", err)
				}
			}
			if stale == "foreign" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("foreign private provider admitted", err)
			}
			if e.CatalogRead().State() != before.State() || before.Get(source.Address()) != nil || before.Get(target.OfType) != nil {
				t.Fatal("provider admission published private definitions")
			}
			if stale != "foreign" && consumer.Base.Get(target.OfType) != captured {
				t.Fatal("captured private provider was mutated")
			}
		})
	}
}
