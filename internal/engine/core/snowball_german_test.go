package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"testing"
)

func germanUnitDictionary(t *testing.T, e interface{ CatalogRead() catalog.Read }) *catalog.TSDictionary {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "german_stem"})
	d, ok := v.TSDictionary(a)
	if !ok {
		t.Fatal("missing genuine German seed")
	}
	return d
}

func TestSnowballGermanSeedAuthority(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	d := germanUnitDictionary(t, e)
	template, ok := v.TSTemplate(d.Template)
	if !ok {
		t.Fatal("missing Snowball template")
	}
	if !d.Seeded() || d.SeedName() != "german_stem" || !template.Snowball() || !template.Seeded() || template.Name() != "snowball" || d.Runtime == nil || d.Address().ObjectID == 3765 {
		t.Fatal("seed identities", d, template)
	}
	defaults := d.SeedOptions()
	if len(defaults) != 2 || defaults[0] != (catalog.DictionaryOption{Name: "language", Value: "german", Supplied: true}) || defaults[1] != (catalog.DictionaryOption{Name: "stopwords", Value: "german", Supplied: true}) {
		t.Fatal("German canonical defaults", defaults)
	}
	cloned := *d
	cloned.ObjectName = "english_stem"
	cloned.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	cloned.ObjectAddress.ObjectID += 100
	if !cloned.Seeded() || cloned.SeedName() != "german_stem" {
		t.Fatal("rename/remap changed intrinsic German identity")
	}
	counterfeit := &catalog.TSDictionary{ObjectAddress: d.Address(), ObjectNamespace: d.Namespace(), ObjectRevision: d.Revision(), ObjectName: d.Name(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime, Implementation: d.Implementation}
	if counterfeit.Seeded() || counterfeit.SeedName() != "" || len(counterfeit.SeedOptions()) != 0 {
		t.Fatal("public dictionary fields forged seed identity")
	}
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, d); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{d.Address(), template.Address(), template.Init, template.Lexize} {
		if a.ObjectID == 0 || stored.DependPinned(v, nil, a) {
			t.Fatal("unallocated or incorrectly pinned seed", a)
		}
	}
	for _, a := range []ids.ObjectAddress{
		{ClassID: ids.TSTemplateClassID, ObjectID: 3727},
		{ClassID: ids.TSDictionaryClassID, ObjectID: 3765},
		{ClassID: ids.TSConfigurationClassID, ObjectID: 3748},
		{ClassID: ids.TSParserClassID, ObjectID: 3722},
	} {
		if !stored.DependPinned(v, nil, a) {
			t.Fatal("fixed bootstrap identity lost pin", a)
		}
	}
	for _, a := range []ids.ObjectAddress{template.Init, template.Lexize} {
		p := v.MustProcedure(a)
		if err := (stored.ProcedureProvider{}).Validate(v, p); err != nil {
			t.Fatal(err)
		}
		copy := *p
		if err := catalog.ValidateSnowballHelper(v, &copy); err == nil {
			t.Fatal("nonresident helper copy admitted")
		}
		if _, fixed := catalog.BuiltinRoutineIdentityByOID(a.ObjectID); fixed {
			t.Fatal("allocated helper in fixed identity table")
		}
		current := *p
		current.ObjectName = "current_" + p.Name()
		current.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		current.ObjectRevision++
		current.Owner = e.Roles().LookupRef("pg_monitor")
		candidate := catalog.CloneState(v.State())
		candidate.UnbindName(catalog.DescriptorNameKey(p))
		candidate.SetDescriptor(a, &current)
		candidate.BindName(catalog.DescriptorNameKey(&current), a)
		if err := (stored.ProcedureProvider{}).Validate(v.WithState(candidate), &current); err != nil {
			t.Fatal("current lawful placement/principal erased intrinsic authority", err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Result = 23 }, func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Display.Binary = "$libdir/unaccent" }, func(p *catalog.Procedure) {
			p.Builtin = v.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}).Builtin
		}, func(p *catalog.Procedure) { p.PLHandler = catalog.PLCallHandler }, func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.Display.Source = "dsnowball_counterfeit" }, func(p *catalog.Procedure) { p.Display.Language = catalog.InternalRoutineLanguage }, func(p *catalog.Procedure) { p.Owner = catalog.Procedure{}.Owner }, func(p *catalog.Procedure) { p.ObjectName = "unbound_helper" }, func(p *catalog.Procedure) { p.SnowballHelper = catalog.NoSnowballHelper }, func(p *catalog.Procedure) {
			p.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999999}
		}} {
			bad := *p
			mutate(&bad)
			state := catalog.CloneState(v.State())
			state.SetDescriptor(a, &bad)
			if err := (stored.ProcedureProvider{}).Validate(v.WithState(state), &bad); err == nil {
				t.Fatal("counterfeit helper admitted", bad)
			}
		}
	}
	for _, mutate := range []func(*catalog.TSDictionary){func(d *catalog.TSDictionary) { d.Runtime = nil }, func(d *catalog.TSDictionary) { d.Options = nil }, func(d *catalog.TSDictionary) {
		d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
	}, func(d *catalog.TSDictionary) {
		d.Options = []catalog.DictionaryOption{{Name: "language", Value: "GeRmAn", Supplied: true}, {Name: "stopwords", Value: "english", Supplied: true}}
	}} {
		bad := *d
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(d.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("counterfeit dictionary admitted", bad)
		}
	}
	for _, mutate := range []func(*catalog.TSTemplate){
		func(d *catalog.TSTemplate) { d.Init = d.Lexize },
		func(d *catalog.TSTemplate) { d.Lexize = ids.ObjectAddress{} },
		func(d *catalog.TSTemplate) { d.Implementation = extension.StockUnaccent },
		func(d *catalog.TSTemplate) {
			d.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 999999}
		},
	} {
		bad := *template
		mutate(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("counterfeit template or dangling current parent admitted", bad)
		}
	}
	forged := &catalog.TSTemplate{ObjectAddress: template.Address(), ObjectNamespace: template.Namespace(), ObjectRevision: template.Revision(), ObjectName: template.Name(), Init: template.Init, Lexize: template.Lexize}
	if err := catalog.ValidateSnowballTemplate(v, forged); err == nil {
		t.Fatal("public fields manufactured private Snowball template marker")
	}
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: d.Namespace(), Name: "german"})
	c := v.MustTSConfiguration(a)
	if !c.Seeded() || c.SeedName() != "german" || c.Parser.ObjectID != 3722 || len(c.Mappings) != 19 {
		t.Fatal("actual German config", c)
	}
	clonedConfig := *c
	clonedConfig.ObjectName = "english"
	clonedConfig.ObjectAddress.ObjectID += 100
	clonedConfig.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if !clonedConfig.Seeded() || clonedConfig.SeedName() != "german" {
		t.Fatal("configuration remap erased German identity")
	}
	counterfeitConfig := &catalog.TSConfiguration{ObjectAddress: c.Address(), ObjectNamespace: c.Namespace(), ObjectRevision: c.Revision(), ObjectName: c.Name(), Owner: c.Owner, Parser: c.Parser, Mappings: c.Mappings}
	if counterfeitConfig.Seeded() || counterfeitConfig.SeedName() != "" {
		t.Fatal("public configuration fields forged seed identity")
	}
	for _, m := range c.Mappings {
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID {
		case 1, 2, 10, 11, 16, 17:
			want = d.Address()
		}
		if len(m.Dictionaries) != 1 || m.Dictionaries[0] != want {
			t.Fatal("mapping", m, want)
		}
	}
}

func germanUnitCreate(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "snowball"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "language"}, Value: "german"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}, Value: "german"}, Supplied: true}}}
}

func TestSnowballGermanPublication(t *testing.T) {
	for _, fence := range []string{"context", "template", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)")
			enginetest.SessionSQL(t, s, "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball")
			enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY german_publication (TEMPLATE=snowball,Language=german,StopWords=german)")
			enginetest.SessionSQL(t, s, "CREATE USER german_other")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			var stmt parser.Statement = germanUnitCreate("german_pending")
			if fence == "original" {
				stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "german_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}}
			}
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			old := germanUnitDictionary(t, e).Runtime
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
				other, closeOther := enginetest.RoleOperation(t, e, "german_other")
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
			if e.CatalogRead().State() != incumbent.State() || p.Runtime == nil || germanUnitDictionary(t, e).Runtime != old {
				t.Fatal("rejected publication changed catalog/runtime")
			}
			value, err := p.Runtime.Lexize(t.Context(), "HÄUSER")
			if err != nil || !value.Recognized || len(value.Lexemes) != 1 {
				t.Fatal("rejected initializer lost its immutable runtime", value, err)
			}
			if value.Lexemes[0].Text != "hÄus" || value.Lexemes[0].Prefix || value.Lexemes[0].Variant != 0 {
				t.Fatal(value)
			}
			stopped, err := old.Lexize(t.Context(), "und")
			if err != nil || !stopped.Recognized || len(stopped.Lexemes) != 0 {
				t.Fatal("rejection mutated incumbent German stoplist", stopped, err)
			}
			candidate, err := p.Runtime.Lexize(t.Context(), "und")
			if err != nil || !candidate.Recognized {
				t.Fatal("rejected compiled runtime lost stop semantics", candidate, err)
			}
			if fence == "original" {
				if len(candidate.Lexemes) != 1 || candidate.Lexemes[0].Text != "und" {
					t.Fatal("removed candidate stoplist", candidate)
				}
			} else if len(candidate.Lexemes) != 0 {
				t.Fatal("compiled German stoplist", candidate)
			}
		})
	}
}

func TestSnowballGermanPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.german_fold (TEMPLATE=snowball,Language=german,StopWords=german)")
	enginetest.SessionSQL(t, s, "CREATE EXTENSION unaccent")
	enginetest.SessionSQL(t, s, "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.german_fold")
	const q = "SELECT ts_lexize('public.german_fold'::regdictionary,'und'::text) AS lexemes"
	ast, d := sequencePreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 {
		t.Fatal("description evaluated dictionary")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	entry := preparedFoldEntry(t, d, 1009, "{}")
	before := snapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "german_fold"})
	if _, ok := e.CatalogRead().LookupType(1009); !ok {
		t.Fatal("TEXT[] missing")
	}
	old := e.CatalogRead().MustTSDictionary(a)
	operand := dictionaryFoldDependency(t, d, a)
	analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.german_fold (stopwords)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current.Runtime == old.Runtime || current.Revision() == operand.Revision {
		t.Fatal("replacement runtime authority not published")
	}
	oldValue, err := old.Runtime.Lexize(t.Context(), "und")
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 0 {
		t.Fatal("retained old runtime", oldValue, err)
	}
	newValue, err := current.Runtime.Lexize(t.Context(), "und")
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 1 || newValue.Lexemes[0].Text != "und" {
		t.Fatal(newValue, err)
	}
	preparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current()
	refreshed := dictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{}")) {
		t.Fatal("accepted immutable provider fold lost backing/current analysis")
	}
	freshAST, fresh := sequencePreparedDescription(t, e, s, q)
	preparedFoldExecute(t, e, s, freshAST, &fresh)
	preparedFoldEntry(t, fresh, 1009, "{und}")
	runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT ts_lexize('public.german_fold'::regdictionary,$1::text) AS lexemes")
	// Existing owner helper supplies YeS, absent from the resource-free runtime.
	r, err := dictionaryFoldRuntimeExecute(e, s, runtimeAST, &runtime)
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
	preparedFoldEntry(t, before, 1009, "{}")
	failedAST, failed := sequencePreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := sequencePreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 {
		t.Fatal("later failure published Snowball prefix", err)
	}
	snowballRejectedFold(t, e, s, ast, d, "{}")
}
