package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

func TestSnowballPorterPublication(t *testing.T) {
	for _, fence := range []string{"context", "template", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY snowball_publication (TEMPLATE=snowball,Language=porter,StopWords=english)")
			enginetest.SessionSQL(t, s, "CREATE USER snowball_other")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			create := enginetest.SnowballDictionary("snowball_pending")
			create.Options[1].Value = "porter" // English is only the explicitly selected stoplist.
			var stmt parser.Statement = create
			if fence == "original" {
				stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "snowball_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}}
			}
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			a := before.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "snowball_publication"})
			old := before.MustTSDictionary(a)
			initialized := p.Runtime
			switch fence {
			case "context":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "template":
				bad := *p.Template
				bad.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				enginetest.PublishOperationStage(t, e, op, other)
			case "namespace":
				bad := *before.MustNamespace(p.Namespace.Address())
				bad.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				enginetest.PublishOperationStage(t, e, op, other)
			case "original":
				bad := *p.Original
				bad.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				enginetest.PublishOperationStage(t, e, op, other)
			case "role":
				other, closeOther := enginetest.RoleOperation(t, e, "snowball_other")
				defer closeOther()
				op = other
			}
			incumbent := e.CatalogRead()
			incumbentDictionary := incumbent.MustTSDictionary(a)
			e.Mu.Lock()
			published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if published != nil || err == nil {
				if published != nil {
					e.DiscardCatalogStage(published)
				}
				t.Fatal("stale Porter initializer published", err)
			}
			if fence == "context" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if fence == "role" {
				enginetest.State(t, err, "42501")
			} else if fence != "context" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != incumbent.State() || e.CatalogRead().MustTSDictionary(a) != incumbentDictionary || incumbentDictionary.Runtime != old.Runtime || p.Runtime != initialized || initialized == nil {
				t.Fatal("rejected publication changed resident descriptor or private runtime")
			}
			pending := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: old.Namespace(), Name: "snowball_pending"})
			if pending.ObjectID != 0 {
				t.Fatal("rejected CREATE published name", pending)
			}
			for _, runtime := range []catalog.DictionaryRuntime{initialized, old.Runtime} {
				got, err := runtime.Lexize(t.Context(), "SKIES")
				if err != nil || !got.Recognized || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "ski" || got.Lexemes[0].Prefix || got.Lexemes[0].Variant != 0 {
					t.Fatal(got, err)
				}
			}
			got, err := old.Runtime.Lexize(t.Context(), "ONLY")
			if err != nil || !got.Recognized || len(got.Lexemes) != 0 {
				t.Fatal("incumbent stoplist changed", got, err)
			}
		})
	}
}
