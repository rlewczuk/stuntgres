package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func yiddishUnitDictionary(t *testing.T, e interface{ CatalogRead() catalog.Read }) *catalog.TSDictionary {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "yiddish_stem"})
	d, ok := v.TSDictionary(a)
	if !ok { t.Fatal("missing genuine Yiddish seed") }
	return d
}

func TestSnowballYiddishSeedAuthority(t *testing.T) {
	e := engine.New()
	v := e.CatalogRead()
	d := yiddishUnitDictionary(t, e)
	template, ok := v.TSTemplate(d.Template)
	if !ok { t.Fatal("missing shared Snowball template") }
	defaults := d.SeedOptions()
	if !d.Seeded() || d.SeedName() != "yiddish_stem" || len(defaults) != 1 || defaults[0] != (catalog.DictionaryOption{Name: "language", Value: "yiddish", Supplied: true}) || len(d.Options) != 1 || d.Options[0] != defaults[0] || d.Owner.ID() != 10 || d.Namespace().ObjectID != 11 || d.Extension != (ids.ObjectAddress{}) { t.Fatal("actual seed/options", d, defaults) }
	if !template.Seeded() || !template.Snowball() || template.Name() != "snowball" { t.Fatal(template) }
	for _, address := range []ids.ObjectAddress{d.Address(), template.Address(), template.Init, template.Lexize} { if address.ObjectID == 0 || stored.DependPinned(v, nil, address) { t.Fatal("allocated authority incorrectly pinned", address) } }
	for _, address := range []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}, {ClassID: ids.TSTemplateClassID, ObjectID: 3727}, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}, {ClassID: ids.TSParserClassID, ObjectID: 3722}} { if !stored.DependPinned(v, nil, address) { t.Fatal("fixed identity lost pin", address) } }
	for _, language := range []string{"english", "arabic", "armenian", "basque", "danish", "dutch", "estonian", "german", "greek", "hindi", "hungarian", "irish", "italian", "lithuanian", "norwegian", "portuguese"} {
		a := v.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: d.Namespace(), Name: language+"_stem"})
		other := v.MustTSDictionary(a)
		if a.ObjectID == 0 || a == d.Address() || other.Template != d.Template { t.Fatal("distinct shared-template seed", language, a) }
	}
	counterfeit := &catalog.TSDictionary{ObjectAddress: d.Address(), ObjectNamespace: d.Namespace(), ObjectRevision: d.Revision(), ObjectName: d.Name(), Owner: d.Owner, Template: d.Template, Options: d.Options, Runtime: d.Runtime}
	if counterfeit.Seeded() || counterfeit.SeedName() != "" || len(counterfeit.SeedOptions()) != 0 { t.Fatal("labels forged seed") }
	copy := *d
	copy.ObjectName = "english_stem"
	copy.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	copy.ObjectAddress.ObjectID += 100
	if copy.SeedName() != "yiddish_stem" { t.Fatal("placement erased creation identity") }
	if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v, d); err != nil { t.Fatal(err) }
	english, err := functions.DefaultSnowballDictionary()
	if err != nil { t.Fatal(err) }
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil }, func(d *catalog.TSDictionary) { d.Runtime = english },
		func(d *catalog.TSDictionary) { d.Options = nil },
		func(d *catalog.TSDictionary) { d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727} },
		func(d *catalog.TSDictionary) { d.Options = []catalog.DictionaryOption{{Name: "language", Value: "YiDdIsH", Supplied: true}} },
	} {
		bad := *d; mutate(&bad)
		state := catalog.CloneState(v.State()); state.SetDescriptor(d.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil { t.Fatal("counterfeit runtime/template/options admitted", bad) }
	}
	for _, a := range []ids.ObjectAddress{template.Init, template.Lexize} {
		p := v.MustProcedure(a)
		if err := catalog.ValidateSnowballHelper(v, p); err != nil { t.Fatal(err) }
		copy := *p
		if err := catalog.ValidateSnowballHelper(v, &copy); err == nil { t.Fatal("nonresident helper admitted") }
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Result = 23 }, func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.SnowballHelper = catalog.NoSnowballHelper },
			func(p *catalog.Procedure) { p.Display.Binary = "$libdir/unaccent" }, func(p *catalog.Procedure) { p.Display.Source = "fake" },
		} {
			bad := *p; mutate(&bad)
			state := catalog.CloneState(v.State()); state.SetDescriptor(a, &bad)
			if err := (stored.ProcedureProvider{}).Validate(v.WithState(state), &bad); err == nil { t.Fatal("counterfeit helper admitted") }
		}
	}
	for _, mutate := range []func(*catalog.TSTemplate){func(t *catalog.TSTemplate) { t.Init = t.Lexize }, func(t *catalog.TSTemplate) { t.Lexize = ids.ObjectAddress{} }, func(t *catalog.TSTemplate) { t.Implementation = extension.StockUnaccent }} {
		bad := *template; mutate(&bad)
		state := catalog.CloneState(v.State()); state.SetDescriptor(bad.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(v.WithState(state), &bad); err == nil { t.Fatal("counterfeit template admitted") }
	}
	forged := &catalog.TSTemplate{ObjectAddress: template.Address(), ObjectNamespace: template.Namespace(), ObjectRevision: template.Revision(), ObjectName: template.Name(), Init: template.Init, Lexize: template.Lexize}
	if err := catalog.ValidateSnowballTemplate(v, forged); err == nil { t.Fatal("labels manufactured template authority") }
	a := v.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: d.Namespace(), Name: "yiddish"})
	c := v.MustTSConfiguration(a)
	if !c.Seeded() || c.SeedName() != "yiddish" || c.Owner.ID() != 10 || c.Namespace() != d.Namespace() || c.Parser.ObjectID != 3722 || len(c.Mappings) != 19 || stored.DependPinned(v, nil, a) { t.Fatal("actual configuration", c) }
	counterfeitConfig := &catalog.TSConfiguration{ObjectAddress: c.Address(), ObjectNamespace: c.Namespace(), ObjectName: c.Name(), Owner: c.Owner, Parser: c.Parser, Mappings: c.Mappings}
	if counterfeitConfig.Seeded() || counterfeitConfig.SeedName() != "" { t.Fatal("labels forged configuration") }
	seen := map[int32]bool{}
	for _, m := range c.Mappings {
		if seen[m.TokenID] { t.Fatal("duplicate mapping", m) }; seen[m.TokenID] = true
		want := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}
		switch m.TokenID { case 1, 2, 10, 11, 16, 17: want = d.Address() }
		if len(m.Dictionaries) != 1 || m.Dictionaries[0] != want { t.Fatal("no ASCII backup", m, want) }
	}
	for _, token := range []int32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 15, 16, 17, 18, 19, 20, 21, 22} { if !seen[token] { t.Fatal("missing token", token) } }
}

func TestSnowballYiddishPublication(t *testing.T) {
	for _, fence := range []string{"context", "template", "init", "lexize", "namespace", "original", "role"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New(); s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE EXTENSION unaccent", "ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)", "ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball", "CREATE TEXT SEARCH DICTIONARY yiddish_publication (TEMPLATE=snowball,Language=yiddish)", "CREATE USER yiddish_other"} { enginetest.SessionSQL(t, s, q) }
			op, done := enginetest.RoleOperation(t, e, "postgres"); defer done()
			var stmt parser.Statement = &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "yiddish_pending"}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "snowball"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "language"}, Value: "yiddish"}, Supplied: true}}}
			if fence == "original" { stmt = &parser.AlterDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "yiddish_publication"}}}, Action: "options", Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}}, Supplied: false}}} }
			stage, p := dictionaryUnitPrepare(t, e, op, stmt); defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead(); old := yiddishUnitDictionary(t, e).Runtime
			if fence == "context" { ctx, cancel := context.WithCancel(op.Ctx); cancel(); op.Ctx = ctx
			} else if fence == "role" { other, closeOther := enginetest.RoleOperation(t, e, "yiddish_other"); defer closeOther(); op = other
			} else {
				other := e.BeginCatalogStage(before); defer e.DiscardCatalogStage(other)
				switch fence {
				case "template": bad := *p.Template; bad.ObjectRevision = bad.Revision()+1; if err := other.Replace(&bad); err != nil { t.Fatal(err) }
				case "init", "lexize":
					a := p.Template.Init; if fence == "lexize" { a = p.Template.Lexize }
					bad := *before.MustProcedure(a); bad.ObjectRevision = bad.Revision()+1; if err := other.Replace(&bad); err != nil { t.Fatal(err) }
				case "namespace": bad := *before.MustNamespace(p.Namespace.Address()); bad.ObjectRevision = bad.Revision()+1; if err := other.Replace(&bad); err != nil { t.Fatal(err) }
				case "original": bad := *p.Original; bad.ObjectRevision = bad.Revision()+1; if err := other.Replace(&bad); err != nil { t.Fatal(err) }
				}
				extensionUnitPublish(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock(); published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p); e.Mu.Unlock()
			if published != nil || err == nil { if published != nil { e.DiscardCatalogStage(published) }; t.Fatal("stale initializer published", err) }
			if fence == "context" { if !errors.Is(err, context.Canceled) { t.Fatal(err) }
			} else if fence == "role" { extensionUnitError(t, err, "42501")
			} else if !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal(err) }
			if e.CatalogRead().State() != incumbent.State() || p.Runtime == nil || yiddishUnitDictionary(t, e).Runtime != old { t.Fatal("rejected publication changed authority") }
			if fence != "context" && fence != "role" && p.Current(incumbent) { t.Fatal("stale preparation remained current") }
			if e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: p.Namespace.Address(), Name: "yiddish_pending"}).ObjectID != 0 { t.Fatal("pending descriptor leaked") }
			for _, tc := range [][2]string{{"קינדער", "קינד"}, {"ONLY", "only"}} {
				value, err := old.Lexize(t.Context(), tc[0]); if err != nil || !value.Recognized || len(value.Lexemes) != 1 || value.Lexemes[0].Text != tc[1] { t.Fatal(value, err) }
			}
			value, err := p.Runtime.Lexize(t.Context(), "קינדער")
			if err != nil || !value.Recognized || len(value.Lexemes) != 1 || value.Lexemes[0].Text != "קינד" || value.Lexemes[0].Prefix || value.Lexemes[0].Variant != 0 { t.Fatal("immutable rejected runtime", value, err) }
		})
	}
}

func TestSnowballYiddishPreparedOwnership(t *testing.T) {
	e := engine.New(); s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TEXT SEARCH DICTIONARY public.yiddish_fold (TEMPLATE=snowball,Language=yiddish)", "CREATE EXTENSION unaccent", "ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.yiddish_fold"} { enginetest.SessionSQL(t, s, q) }
	const q = "SELECT ts_lexize('public.yiddish_fold'::regdictionary,'ONLY'::text) AS lexemes"
	ast, d := sequencePreparedDescription(t, e, s, q)
	if len(d.Folds.Current().Entries) != 0 { t.Fatal("description evaluated dictionary") }
	preparedFoldExecute(t, e, s, ast, &d)
	entry := preparedFoldEntry(t, d, 1009, "{only}"); before := snapshotFoldDescription(d)
	a := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "yiddish_fold"})
	old := e.CatalogRead().MustTSDictionary(a); operand := dictionaryFoldDependency(t, d, a); analysis := d.Analysis
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.yiddish_fold (stopwords=english)")
	current := e.CatalogRead().MustTSDictionary(a)
	if current.Runtime == old.Runtime || current.Revision() == operand.Revision { t.Fatal("replacement runtime not published") }
	oldValue, err := old.Runtime.Lexize(t.Context(), "ONLY")
	if err != nil || !oldValue.Recognized || len(oldValue.Lexemes) != 1 || oldValue.Lexemes[0].Text != "only" { t.Fatal("old runtime lost ownership", oldValue, err) }
	newValue, err := current.Runtime.Lexize(t.Context(), "ONLY")
	if err != nil || !newValue.Recognized || len(newValue.Lexemes) != 0 { t.Fatal(newValue, err) }
	preparedFoldExecute(t, e, s, ast, &d)
	after := d.Folds.Current(); refreshed := dictionaryFoldDependency(t, d, a)
	if d.Analysis == analysis || refreshed.Revision != current.Revision() || after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{only}")) { t.Fatal("accepted fold lost backing/current analysis") }
	freshAST, fresh := sequencePreparedDescription(t, e, s, q); preparedFoldExecute(t, e, s, freshAST, &fresh); preparedFoldEntry(t, fresh, 1009, "{}")
	runtimeAST, runtime := sequencePreparedDescription(t, e, s, "SELECT ts_lexize('public.yiddish_fold'::regdictionary,$1::text) AS lexemes")
	for _, tc := range [][2]string{{"ONLY", "{}"}, {"קינדער", "{קינד}"}, {"RATS", "{rats}"}} {
		func() {
			op, err := s.Acquire(t.Context()); if err != nil { t.Fatal(err) }; defer op.Done()
			scope, err := e.StartStatement(op); if err != nil { t.Fatal(err) }; scope.Expected = &runtime
			r, err := scope.Execute(runtimeAST, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error { value, err := info.Types[0].Type.InputWithEnvironment(ctx, tc[0], -1, input); frame[0] = value; return err })
			if err = scope.Finish(err); err != nil { t.Fatal(err) }
			got := enginetest.Tuples(r); r.Rows.Close()
			if len(got) != 1 || got[0][0] != tc[1] { t.Fatal("current parameter authority", tc, got) }
		}()
	}
	for _, dep := range before.Accepted.Dependencies { if dep.Ref.Address == a { t.Fatal("fold operand retained executable authority") } }
	preparedFoldEntry(t, before, 1009, "{only}")
	failedAST, failed := sequencePreparedDescription(t, e, s, q+",(1/0)::bigint")
	if _, err := sequencePreparedExecute(e, s, failedAST, &failed); err == nil || len(failed.Folds.Current().Entries) != 0 || failed.Folds.Current().Generation != 0 { t.Fatal("later expression published prefix", err) }
	snowballRejectedFold(t, e, s, ast, d, "{only}")
}
