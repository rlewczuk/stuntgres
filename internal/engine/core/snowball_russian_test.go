package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func russianUnitDictionary(t *testing.T, e interface{ CatalogRead() catalog.Read }) *catalog.TSDictionary {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "russian_stem"})
	d, ok := v.TSDictionary(a)
	if !ok {
		t.Fatal("missing genuine Russian seed")
	}
	return d
}

func TestSnowballRussianSeedAuthority(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	d := russianUnitDictionary(t, e)
	template, ok := v.TSTemplate(d.Template)
	if !ok {
		t.Fatal("missing shared Snowball template")
	}
	defaults := []catalog.DictionaryOption{{Name: "language", Value: "russian", Supplied: true}, {Name: "stopwords", Value: "russian", Supplied: true}}
	if !d.Seeded() || d.SeedName() != "russian_stem" || !template.Snowball() || !template.Seeded() || template.Name() != "snowball" || d.Runtime == nil || d.Address().ClassID != ids.TSDictionaryClassID || d.Address().ObjectID == 0 || d.Address().ObjectID == 3765 {
		t.Fatal("genuine allocated seed/template", d, template)
	}
	if !slices.Equal(d.SeedOptions(), defaults) || !slices.Equal(d.Options, defaults) || d.Namespace().ObjectID != 11 || d.Owner.ID() != 10 || d.Extension != (ids.ObjectAddress{}) {
		t.Fatal("stock options and placement", d)
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, d); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{d.Address(), template.Address(), template.Init, template.Lexize} {
		if a.ObjectID == 0 || stored.DependPinned(v, nil, a) {
			t.Fatal("allocated identity incorrectly pinned", a)
		}
	}
	for _, language := range []string{"english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese", "yiddish", "indonesian", "nepali", "romanian", "spanish", "swedish", "tamil", "turkish", "serbian", "finnish", "catalan", "french"} {
		a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: d.Namespace(), Name: language + "_stem"})
		other := v.MustTSDictionary(a)
		if a.ObjectID == 0 || a == d.Address() || other.Template != d.Template {
			t.Fatal("distinct dictionary/shared template", language, a)
		}
	}
	clone := *d
	clone.ObjectName = "english_stem"
	clone.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	clone.ObjectAddress.ObjectID += 100
	if clone.SeedName() != "russian_stem" || !slices.Equal(clone.SeedOptions(), defaults) {
		t.Fatal("rename/remap erased seed identity")
	}
	fake := &catalog.TSDictionary{ObjectAddress: d.Address(), ObjectNamespace: d.Namespace(), ObjectRevision: d.Revision(), ObjectName: d.Name(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if fake.Seeded() || fake.SeedName() != "" || len(fake.SeedOptions()) != 0 {
		t.Fatal("labels manufactured seed authority")
	}
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Options = nil },
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
		},
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "language", Value: "RuSsIaN", Supplied: true}, {Name: "stopwords", Value: "russian", Supplied: true}}
		},
	} {
		bad := *d
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(d.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("counterfeit runtime/options admitted", bad)
		}
	}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: d.Namespace(), Name: "russian"})
	c := v.MustTSConfiguration(a)
	if !c.Seeded() || c.SeedName() != "russian" || c.Address().ObjectID == 0 || c.Address().ObjectID == 3748 || c.Namespace() != d.Namespace() || c.Owner.ID() != 10 || c.Parser.ObjectID != 3722 || c.Extension != (ids.ObjectAddress{}) || len(c.Mappings) != 19 || stored.DependPinned(v, nil, a) {
		t.Fatal("genuine configuration", c)
	}
	copyConfig := *c
	copyConfig.ObjectName = "english"
	copyConfig.ObjectAddress.ObjectID += 100
	if copyConfig.SeedName() != "russian" {
		t.Fatal("configuration rename/remap erased identity")
	}
	fakeConfig := &catalog.TSConfiguration{ObjectAddress: a, ObjectNamespace: c.Namespace(), ObjectRevision: c.Revision(), ObjectName: c.Name(), Owner: c.Owner, Parser: c.Parser, Mappings: c.Mappings}
	if fakeConfig.Seeded() || fakeConfig.SeedName() != "" {
		t.Fatal("labels manufactured configuration authority")
	}
	english := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: d.Namespace(), Name: "english_stem"})
	seen := map[int32]bool{}
	for _, m := range c.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 11, 16:
			want = english
		case 2, 10, 17:
			want = d.Address()
		}
		if seen[m.TokenID] || len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("real split mapping", m, want)
		}
		seen[m.TokenID] = true
	}
	for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} {
		if !seen[token] {
			t.Fatal("missing released mapping", token)
		}
	}
	// Mutating the English label cannot retarget the already installed edge.
	renamed := *v.MustTSDictionary(english)
	renamed.ObjectName = "saved_english"
	renamed.ObjectRevision++
	state := catalog.CloneState(v.State())
	state.UnbindName(catalog.DescriptorNameKey(v.MustTSDictionary(english)))
	state.SetDescriptor(english, &renamed)
	state.BindName(catalog.DescriptorNameKey(&renamed), english)
	if state == v.State() || v.WithState(state).MustTSConfiguration(a).Mappings[0].Dictionaries[0] != english {
		t.Fatal("English backup lost allocated identity")
	}
}

func russianUnitCreate(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "snowball"}, Supplied: true},
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "language"}, Value: "russian"}, Supplied: true},
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}, Value: "russian"}, Supplied: true},
	}}
}

func TestSnowballRussianPublication(t *testing.T) {
	for _, fence := range []string{"context", "template", "init", "lexize", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY russian_publication (TEMPLATE=snowball,Language=russian,StopWords=russian)")
			enginetest.SessionSQL(t, s, "CREATE USER russian_other")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			var stmt parser.Statement = russianUnitCreate("russian_pending")
			if fence == "original" {
				stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "russian_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}}
			}
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			old := russianUnitDictionary(t, e).Runtime
			switch fence {
			case "context":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "role":
				other, closeOther := enginetest.RoleOperation(t, e, "russian_other")
				defer closeOther()
				op = other
			default:
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				var err error
				switch fence {
				case "template":
					bad := *p.Template
					bad.ObjectRevision++
					err = other.Replace(&bad)
				case "init", "lexize":
					a := p.Template.Init
					if fence == "lexize" {
						a = p.Template.Lexize
					}
					bad := *before.MustProcedure(a)
					bad.ObjectRevision = bad.Revision() + 1
					err = other.Replace(&bad)
				case "namespace":
					bad := *before.MustNamespace(p.Namespace.Address())
					bad.ObjectRevision++
					err = other.Replace(&bad)
				case "original":
					bad := *p.Original
					bad.ObjectRevision++
					err = other.Replace(&bad)
				}
				if err != nil {
					t.Fatal(err)
				}
				enginetest.PublishOperationStage(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock()
			published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if published != nil {
				e.DiscardCatalogStage(published)
				t.Fatal("rejected initializer published", err)
			}
			if fence == "context" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if fence == "role" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != incumbent.State() || p.Runtime == nil || russianUnitDictionary(t, e).Runtime != old {
				t.Fatal("rejected publication changed catalog/runtime")
			}
			if fence != "context" && fence != "role" && p.Current(incumbent) {
				t.Fatal("stale preparation retained authority")
			}
			if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: p.Namespace.Address(), Name: "russian_pending"}).ObjectID != 0 {
				t.Fatal("pending dictionary escaped")
			}
			for _, runtime := range []catalog.DictionaryRuntime{old, p.Runtime} {
				got, err := runtime.Lexize(t.Context(), "машины")
				if err != nil || !got.Recognized || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "машин" {
					t.Fatal("immutable real provider after rejection", got, err)
				}
			}
			got, err := old.Lexize(t.Context(), "все")
			if err != nil || !got.Recognized || len(got.Lexemes) != 0 {
				t.Fatal("old stopword state changed", got, err)
			}
		})
	}
}

func TestSnowballRussianPreparedOwnership(t *testing.T) {
	t.Run("protocol_resources", russianProtocolOwnership)
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.russian_fold (TEMPLATE=snowball,Language=russian,StopWords=russian)")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.russian_fold")
	const q = "SELECT ts_lexize('public.russian_fold'::regdictionary,'все'::text) AS lexemes"
	ast, d := enginetest.PreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("Describe evaluated provider")
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	entry := enginetest.PreparedFoldEntry(t, d, 1009, "{}")
	before := enginetest.SnapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "russian_fold"})
	old := e.CatalogRead().MustTSDictionary(a)
	operand := enginetest.DictionaryFoldDependency(t, d, a)
	analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.russian_fold (stopwords)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current.Runtime == old.Runtime || current.Revision() == operand.Revision {
		t.Fatal("replacement authority not published")
	}
	oldValue, err := old.Runtime.Lexize(t.Context(), "все")
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 0 {
		t.Fatal("retained old runtime", oldValue, err)
	}
	newValue, err := current.Runtime.Lexize(t.Context(), "все")
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 1 || newValue.Lexemes[0].Text != "все" {
		t.Fatal(newValue, err)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	refreshed := enginetest.DictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, 1009, "{}")) {
		t.Fatal("immutable accepted backing/generation and current analysis")
	}
	freshAST, fresh := enginetest.PreparedDescription(t, e, s, q)
	enginetest.PreparedFoldExecute(t, e, s, freshAST, &fresh)
	enginetest.PreparedFoldEntry(t, fresh, 1009, "{все}")
	runtimeAST, runtime := enginetest.PreparedDescription(t, e, s, "SELECT ts_lexize('public.russian_fold'::regdictionary,$1::text) AS lexemes")
	for _, tc := range []struct{ input, want string }{{"все", "{все}"}, {"машины", "{машин}"}, {"RATS", "{rats}"}} {
		func() {
			op, err := s.Acquire(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &runtime
			r, err := scope.Execute(runtimeAST, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
				value, err := info.Types[0].Type.InputWithEnvironment(ctx, tc.input, -1, input)
				frame[0] = value
				return err
			})
			if err = scope.Finish(err); err != nil {
				t.Fatal(err)
			}
			got := enginetest.Tuples(r)
			r.Rows.Close()
			if len(got) != 1 || got[0][0] != tc.want {
				t.Fatal("current parameter provider", tc.input, got)
			}
		}()
	}
	for _, dep := range before.Accepted.Dependencies {
		if dep.Ref.Address == a {
			t.Fatal("operand retained as executable authority")
		}
	}
	enginetest.PreparedFoldEntry(t, before, 1009, "{}")
	failedAST, failed := enginetest.PreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := enginetest.PreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("later failure published prefix", err)
	}
	enginetest.SnowballRejectedFold(t, e, s, ast, d, "{}")
}
