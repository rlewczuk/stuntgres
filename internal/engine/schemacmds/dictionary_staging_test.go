package schemacmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
)

// A dictionary's first TEMP namespace remains candidate-owned throughout
// detached compilation, including recapture after an unrelated publication.
func TestDictionaryStagingTemporaryAuthority(t *testing.T) {
	for _, recapture := range []bool{false, true} {
		name := "detached"
		if recapture {
			name = "recaptured"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.CatalogRead()
			journal := op.Session.Transactions.Catalog.Clone()
			stmt := enginetest.Statement(t, "CREATE TEXT SEARCH DICTIONARY pg_temp.dictionary_candidate (TEMPLATE=pg_catalog.simple,accept=false)")
			var stage *core.CatalogStage
			var preparedNamespace catalog.Descriptor
			var err error
			if !recapture {
				stage, err = schemacmds.StageDictionaryDetached(e, op, stmt)
			} else {
				e.Mu.Lock()
				auth, authErr := e.AuthorizationLocked(op)
				if authErr != nil {
					e.Mu.Unlock()
					t.Fatal(authErr)
				}
				stage = e.BeginCatalogStage(auth.Catalog)
				stage.AuthorizeLocked(auth, op.Session)
				preparation, prepareErr := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(stage)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				preparedNamespace = preparation.Namespace
				if err := preparation.Compile(context.Background()); err != nil {
					t.Fatal(err)
				}
				// Publish a real unrelated descriptor revision while the first
				// TEMP namespace exists only in the dictionary candidate.
				e.Mu.Lock()
				other := e.BeginCatalogStage(e.Catalog.Current())
				public := *before.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
				public.ObjectRevision, err = e.Catalog.RevisionLocked()
				if err == nil {
					err = other.Replace(&public)
				}
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(other)
				if err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
				e.Mu.Lock()
				stage, err = schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &preparation)
				e.Mu.Unlock()
			}
			if err != nil || stage == nil {
				t.Fatal("first TEMP dictionary staging", err)
			}
			defer e.DiscardCatalogStage(stage)
			if stage.Base.State() != e.CatalogRead().State() || (!recapture && e.CatalogRead().State() != before.State()) {
				t.Fatal("completion did not retain the current publication predecessor")
			}
			if op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || !reflect.DeepEqual(op.Session.Transactions.Catalog.Clone(), journal) {
				t.Fatal("detached staging published session TEMP authority or journal")
			}
			view := e.StageRead(stage)
			var namespace, toast *catalog.Namespace
			var dictionary *catalog.TSDictionary
			descriptorCount := 0
			for address, descriptor := range stage.Descriptors() {
				descriptorCount++
				if before.Get(address) != nil || view.Get(address) != descriptor {
					t.Fatal("lost candidate-only identity", address)
				}
				switch d := descriptor.(type) {
				case *catalog.Namespace:
					if d.TemporaryToast {
						toast = d
					} else {
						namespace = d
					}
				case *catalog.TSDictionary:
					dictionary = d
				}
			}
			if descriptorCount != 3 || namespace == nil || toast == nil || dictionary == nil {
				t.Fatal("candidate must own exactly one namespace pair and dictionary")
			}
			if preparedNamespace != nil && namespace != preparedNamespace {
				t.Fatal("recapture replaced the prepared namespace identity")
			}
			for _, ns := range []*catalog.Namespace{namespace, toast} {
				if ns.Temporary != op.Session.SessionToken || ns.Owner != op.RoleState().CurrentUser || view.NameAddress(catalog.DescriptorNameKey(ns)) != ns.Address() {
					t.Fatal("candidate namespace lost actual session/owner authority", ns)
				}
			}
			if dictionary.Namespace() != namespace.Address() || dictionary.Owner != op.RoleState().CurrentUser || view.NameAddress(catalog.DescriptorNameKey(dictionary)) != dictionary.Address() {
				t.Fatal("dictionary lost candidate namespace or creator ownership", dictionary)
			}
			if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
				t.Fatal("candidate provider validation", err)
			}
		})
	}
}
