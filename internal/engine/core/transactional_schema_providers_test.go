package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTransactionalSchemaPrivateProviders(t *testing.T) {
	for _, stale := range []string{"none", "type", "relation", "foreign"} {
		t.Run(stale, func(t *testing.T) {
			e := engine.New()
			owner := enginetest.StartSession(t, e, nil)
			ref := enginetest.TransactionBegin(t, owner)
			op := enginetest.TransactionOperation(t, owner)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.Gate(&parser.CreateTable{}); err != nil {
				t.Fatal(err)
			}
			defer func() {
				scope.Finish(errors.New("discard private provider unit"))
				op.Done()
				enginetest.TransactionEnd(t, owner, ref, false)
			}()
			before := e.CatalogRead()
			usage := enginetest.Usage(e, sqltype.Int4)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			provider := e.BeginCatalogStage(auth.Catalog)
			source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "private_composite", auth.State.CurrentUser, []catalog.Column{{Name: "v", Usage: usage, Nullable: true, NullabilityKnown: true}}, map[string]int{"v": 0})
			if err == nil {
				source.RelationKind = catalog.CompositeRelation
				err = e.StageRowTypeLocked(provider, source)
			}
			if err == nil {
				err = provider.Create(source)
			}
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			journal := scope.Owner.Catalog.Clone()
			for address, descriptor := range provider.Descriptors() {
				e.CatalogChangeLocked(journal, address, descriptor)
			}
			scope.Owner.Catalog = journal
			private := e.PrivateCatalogLocked(journal)
			consumer := e.BeginCatalogStage(private)
			target, err := table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "private_typed", source.Owner, nil, nil)
			if err == nil {
				target.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
				err = consumer.Create(target)
			}
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			captured := private.MustType(target.OfType)
			if stale == "type" || stale == "relation" {
				next := journal.Clone()
				if stale == "type" {
					changed := *captured
					changed.ObjectRevision++
					e.CatalogChangeLocked(next, changed.Address(), &changed)
				} else {
					changed := *source
					changed.ObjectRevision++
					e.CatalogChangeLocked(next, changed.Address(), &changed)
				}
				scope.Owner.Catalog = next
			}
			e.Mu.Unlock()
			if stale == "foreign" {
				consumer.Base = engine.New().CatalogRead()
			}
			defer e.DiscardCatalogStage(provider)
			defer e.DiscardCatalogStage(consumer)
			err = tablecmds.AcquireTypedSources(scope, consumer, false)
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
