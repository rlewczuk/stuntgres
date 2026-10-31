package functions_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

func TestSnowballSpanishProvider(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: "spanish_stem"})
	d := v.MustTSDictionary(a)
	other := engine.New().CatalogRead().MustTSDictionary(a)
	defaults := []catalog.DictionaryOption{{Name: "language", Value: "spanish", Supplied: true}, {Name: "stopwords", Value: "spanish", Supplied: true}}
	if !d.Seeded() || d.SeedName() != "spanish_stem" || d.Owner.ID() != 10 || !slices.Equal(d.Options, defaults) || !slices.Equal(d.SeedOptions(), defaults) || d.Runtime == nil || d.Runtime != other.Runtime {
		t.Fatal("genuine singleton/defaults", d)
	}
	template, ok := v.TSTemplate(d.Template)
	if !ok {
		t.Fatal("missing Snowball template")
	}
	if !template.Snowball() || !template.Seeded() || template.Name() != "snowball" {
		t.Fatal(template)
	}
	if err := catalog.ValidateSnowballTemplate(v, template); err != nil {
		t.Fatal(err)
	}
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(v, d); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese"} {
		address := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: language + "_stem"})
		if address == a || address.ObjectID == 0 || v.MustTSDictionary(address).Template != d.Template {
			t.Fatal("distinct shared-template seed", language)
		}
	}
	renamed := *d
	renamed.ObjectName = "english_stem"
	renamed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if renamed.SeedName() != "spanish_stem" {
		t.Fatal("mutable label granted authority")
	}
	counterfeit := &catalog.TSDictionary{ObjectAddress: a, ObjectNamespace: ns, ObjectName: d.Name(), ObjectRevision: d.Revision(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if counterfeit.Seeded() || counterfeit.SeedName() != "" || len(counterfeit.SeedOptions()) != 0 {
		t.Fatal("public fields forged seed")
	}
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Options = nil },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "language", Value: "english", Supplied: true}}
		},
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
		},
	} {
		bad := *d
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(a, &bad)
		if err := provider.Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("counterfeit runtime admitted", bad)
		}
	}
	for _, tc := range []struct {
		input, want string
		count       int
	}{{"CASAS", "cas", 1}, {"niños", "niñ", 1}, {"más", "", 0}, {"educacion", "educacion", 1}} {
		got, err := d.Runtime.Lexize(t.Context(), tc.input)
		if err != nil || !got.Recognized || len(got.Lexemes) != tc.count || tc.count == 1 && got.Lexemes[0].Text != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}

func TestSnowballSpanishPreparedOwnership(t *testing.T) {
	// The composed helper retains real accepted artifacts, their generation,
	// immutable backing and operand provenance across stoplist replacement,
	// fresh binding, live parameter execution and rejected fold publication.
	snowballPreparedOwnership(t, "spanish", "más", "mas")
}

func TestSnowballSpanishPublication(t *testing.T) {
	// Exercise actual compile/stage/complete rejection, not a fake runtime.
	snowballPublication(t, "spanish", "CASAS", "cas")
}

func snowballPreparedOwnership(t *testing.T, language, input, want string) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.snowball_fold (TEMPLATE=snowball,Language="+language+",StopWords="+language+")")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.snowball_fold")
	q := "SELECT ts_lexize('public.snowball_fold'::regdictionary,'" + input + "'::text) AS lexemes"
	ast, d := enginetest.PreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("description evaluated dictionary")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 1009, "{}")
	before := enginetest.SnapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "snowball_fold"})
	if _, ok := e.CatalogRead().LookupType(1009); !ok {
		t.Fatal("TEXT[] missing")
	}
	old := e.CatalogRead().MustTSDictionary(a)
	operand := enginetest.DictionaryFoldDependency(t, d, a)
	analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.snowball_fold (stopwords)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current.Runtime == old.Runtime || current.Revision() == operand.Revision {
		t.Fatal("replacement runtime authority not published")
	}
	oldValue, err := old.Runtime.Lexize(t.Context(), input)
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 0 {
		t.Fatal("retained old runtime", oldValue, err)
	}
	newValue, err := current.Runtime.Lexize(t.Context(), input)
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 1 {
		t.Fatal(newValue, err)
	}
	if newValue.Lexemes[0].Text != want || newValue.Lexemes[0].Prefix || newValue.Lexemes[0].Variant != 0 {
		t.Fatal(newValue)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	refreshed := enginetest.DictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, 1009, "{}")) {
		t.Fatal("accepted immutable provider fold lost backing/current analysis")
	}
	freshAST, fresh := enginetest.PreparedDescription(t, e, s, q)
	enginetest.PreparedFoldExecute(t, e, s, freshAST, &fresh)
	enginetest.PreparedFoldEntry(t, fresh, 1009, "{"+want+"}")
	runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT ts_lexize('public.snowball_fold'::regdictionary,$1::text) AS lexemes")
	// Existing owner helper supplies YeS: non-stopword remains a genuine stem.
	r, err := enginetest.DictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
	if err != nil {
		t.Fatal(err)
	}
	got := enginetest.Tuples(r)
	r.Rows.Close()
	if len(got) != 1 || got[0][0] != "{yes}" {
		t.Fatal(got)
	}
	for _, dep := range before.Accepted.Dependencies {
		if dep.Ref.Address == a {
			t.Fatal("operand retained as executable authority")
		}
	}
	enginetest.PreparedFoldEntry(t, before, 1009, "{}")
	failedAST, failed := enginetest.PreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := enginetest.PreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("later failure published Snowball prefix", err)
	}
	enginetest.SnowballRejectedFold(t, e, s, ast, d, "{}")
}

func snowballPublication(t *testing.T, language, input, want string) {
	for _, fence := range []string{"context", "template", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY snowball_publication (TEMPLATE=snowball,Language="+language+",StopWords="+language+")")
			enginetest.SessionSQL(t, s, "CREATE USER snowball_other")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			create := enginetest.SnowballDictionary("snowball_pending")
			create.Options[1].Value = language
			create.Options[2].Value = language
			var stmt parser.Statement = create
			if fence == "original" {
				stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "snowball_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}}
			}
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			seedAddress := before.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: language + "_stem"})
			old := before.MustTSDictionary(seedAddress).Runtime
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
				// Completion must use a newly authenticated actor, not prepared authority.
				other, closeOther := enginetest.RoleOperation(t, e, "snowball_other")
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
				enginetest.State(t, err, "42501")
			} else if fence != "context" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != incumbent.State() || p.Runtime == nil || e.CatalogRead().MustTSDictionary(seedAddress).Runtime != old {
				t.Fatal("rejected publication changed catalog/runtime")
			}
			value, err := p.Runtime.Lexize(t.Context(), input)
			if err != nil || !value.Recognized || len(value.Lexemes) != 1 {
				t.Fatal("rejected initializer lost its immutable runtime", value, err)
			}
			if value.Lexemes[0].Text != want || value.Lexemes[0].Prefix || value.Lexemes[0].Variant != 0 {
				t.Fatal(value)
			}
			retained, err := old.Lexize(t.Context(), input)
			if err != nil || !retained.Recognized || len(retained.Lexemes) != 1 || retained.Lexemes[0].Text != want || retained.Lexemes[0].Prefix || retained.Lexemes[0].Variant != 0 {
				t.Fatal("rejected publication changed incumbent execution", retained, err)
			}
		})
	}
}
