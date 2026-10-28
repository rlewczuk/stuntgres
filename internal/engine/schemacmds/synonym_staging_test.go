package schemacmds_test

import (
	"context"
	"errors"
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

func TestSynonymPublicationFences(t *testing.T) {
	for _, fence := range []string{"cancel", "template", "helper", "namespace", "dictionary", "authority", "unrelated"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			admin, adminDone := enginetest.RoleOperation(t, e, "postgres")
			defer adminDone()
			actor := "postgres"
			if fence == "authority" {
				enginetest.RoleExecute(t, e, admin, "CREATE ROLE synonym_stage_actor LOGIN SUPERUSER")
				actor = "synonym_stage_actor"
			}
			op, done := enginetest.RoleOperation(t, e, actor)
			defer done()
			name := "pg_temp.synonym_candidate"
			if fence == "namespace" || fence == "authority" || fence == "dictionary" {
				name = "public.synonym_candidate"
			}
			query := "CREATE TEXT SEARCH DICTIONARY " + name + " (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)"
			if fence == "dictionary" {
				enginetest.RoleExecute(t, e, admin, query)
				query = "ALTER TEXT SEARCH DICTIONARY " + name + " (casesensitive=true)"
			}
			stmt := enginetest.Statement(t, query)
			journal := op.Session.Transactions.Catalog.Clone()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, op.Session)
			preparation, err := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
			e.Mu.Unlock()
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				t.Fatal(err)
			}
			if err := preparation.Compile(op.Context()); err != nil {
				t.Fatal(err)
			}
			compiled := preparation.Runtime
			if compiled == nil {
				t.Fatal("real initializer omitted runtime")
			}
			lexical, err := compiled.Lexize(context.Background(), "indices")
			if err != nil || !lexical.Recognized || len(lexical.Lexemes) != 1 || lexical.Lexemes[0].Text != "index" || !lexical.Lexemes[0].Prefix {
				t.Fatal("fence fixture did not actually compile prefix data", lexical, err)
			}
			switch fence {
			case "cancel":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "authority":
				enginetest.RoleExecute(t, e, admin, "ALTER ROLE synonym_stage_actor NOSUPERUSER")
			default:
				e.Mu.Lock()
				other := e.BeginCatalogStage(e.Catalog.Current())
				revision, revisionErr := e.Catalog.RevisionLocked()
				if revisionErr != nil {
					e.Mu.Unlock()
					t.Fatal(revisionErr)
				}
				var replace catalog.Descriptor
				switch fence {
				case "template":
					d := *preparation.Template
					d.ObjectRevision = revision
					replace = &d
				case "helper":
					original, _ := e.Catalog.Current().Procedure(preparation.Template.Init)
					d := *original
					d.ObjectRevision = revision
					replace = &d
				case "dictionary":
					d := *preparation.Original
					d.ObjectRevision = revision
					replace = &d
				default:
					a := preparation.Namespace.Address()
					if fence == "unrelated" {
						a = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
					}
					d := *e.Catalog.Current().MustNamespace(a)
					d.ObjectRevision = revision
					replace = &d
				}
				err = other.Replace(replace)
				e.Mu.Unlock()
				defer e.DiscardCatalogStage(other)
				if err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, admin, other)
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock()
			completed, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &preparation)
			e.Mu.Unlock()
			if fence == "unrelated" {
				if err != nil || completed == nil {
					t.Fatal("unrelated publication prevented recapture", err)
				}
				defer e.DiscardCatalogStage(completed)
				if completed.Base.State() != incumbent.State() {
					t.Fatal("recapture lost current predecessor")
				}
				view := e.StageRead(completed)
				var dictionary *catalog.TSDictionary
				count := 0
				for _, descriptor := range completed.Descriptors() {
					count++
					if d, ok := descriptor.(*catalog.TSDictionary); ok {
						dictionary = d
					}
				}
				if count != 3 || dictionary == nil || dictionary.Runtime != compiled || dictionary.Namespace() != preparation.Namespace.Address() || view.Get(preparation.Namespace.Address()) != preparation.Namespace {
					t.Fatal("recapture replaced initializer or candidate TEMP identities")
				}
				if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(view, dictionary); err != nil {
					t.Fatal(err)
				}
			} else {
				if completed != nil || err == nil {
					t.Fatal("rejected candidate completed", fence, completed, err)
				}
				if fence == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				} else if fence == "authority" {
					enginetest.State(t, err, "42501")
				} else if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("stale semantic input not fenced", err)
				}
			}
			if e.CatalogRead().State() != incumbent.State() || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || !reflect.DeepEqual(journal, op.Session.Transactions.Catalog.Clone()) {
				t.Fatal("completion leaked catalog, TEMP authority or journal")
			}
			if preparation.Runtime != compiled {
				t.Fatal("completion mutated detached runtime")
			}
		})
	}
}
