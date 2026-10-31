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

func indonesianUnitDictionary(t *testing.T, e interface{ CatalogRead() catalog.Read }) *catalog.TSDictionary {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "indonesian_stem"})
	d, ok := v.TSDictionary(a)
	if !ok {
		t.Fatal("missing genuine Indonesian seed")
	}
	return d
}

func indonesianUnitCreate(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "snowball"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "language"}, Value: "indonesian"}, Supplied: true}}}
}

func TestSnowballIndonesianPublication(t *testing.T) {
	for _, fence := range []string{"context", "template", "init", "lexize", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY indonesian_publication (TEMPLATE=snowball,Language=indonesian)")
			enginetest.SessionSQL(t, s, "CREATE USER indonesian_other")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			var stmt parser.Statement = indonesianUnitCreate("indonesian_pending")
			if fence == "original" {
				stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "indonesian_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}}
			}
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			old := indonesianUnitDictionary(t, e).Runtime
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
				extensionUnitPublish(t, e, op, other)
			case "init", "lexize":
				address := p.Template.Init
				if fence == "lexize" {
					address = p.Template.Lexize
				}
				bad := *before.MustProcedure(address)
				bad.ObjectRevision = bad.Revision() + 1
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			case "namespace":
				bad := *before.MustNamespace(p.Namespace.Address())
				bad.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			case "original":
				bad := *p.Original
				bad.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&bad); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			case "role":
				// Completion must use a newly authenticated actor, not prepared authority.
				other, closeOther := enginetest.RoleOperation(t, e, "indonesian_other")
				defer closeOther()
				op = other
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock()
			published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if published != nil || err == nil {
				if published != nil {
					e.DiscardCatalogStage(published)
				}
				t.Fatal("stale initializer published", err)
			}
			if fence == "context" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if fence == "role" {
				extensionUnitError(t, err, "42501")
			} else if fence != "context" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != incumbent.State() || p.Runtime == nil || indonesianUnitDictionary(t, e).Runtime != old {
				t.Fatal("rejected publication changed catalog/runtime")
			}
			if fence != "context" && fence != "role" && p.Current(incumbent) {
				t.Fatal("stale preparation retained current authority")
			}
			if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: p.Namespace.Address(), Name: "indonesian_pending"}).ObjectID != 0 {
				t.Fatal("rejected initializer published pending dictionary")
			}
			for _, input := range []string{"RATS", "ONLY"} {
				value, err := old.Lexize(t.Context(), input)
				want := "rats"
				if input == "ONLY" {
					want = "only"
				}
				if err != nil || !value.Recognized || len(value.Lexemes) != 1 || value.Lexemes[0].Text != want {
					t.Fatal("rejection changed language-only incumbent", input, value, err)
				}
			}
			value, err := p.Runtime.Lexize(t.Context(), "MENYAPU")
			if err != nil || !value.Recognized || len(value.Lexemes) != 1 {
				t.Fatal("rejected initializer lost its immutable runtime", value, err)
			}
			if value.Lexemes[0].Text != "sapu" || value.Lexemes[0].Prefix || value.Lexemes[0].Variant != 0 {
				t.Fatal(value)
			}
		})
	}
}
