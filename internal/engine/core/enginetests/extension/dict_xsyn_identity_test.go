package extension_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
)

func dictXsynUnitInstalled(t *testing.T) (*core.Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dict_xsyn")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dict_xsyn"}))
	return e, op, ext
}

func TestDictXsynInstalledIdentity(t *testing.T) {
	e, op, ext := dictXsynUnitInstalled(t)
	v := e.CatalogRead()
	control := extension.StockDictXsyn.Control()
	if control.Version != "1.0" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "text search dictionary template for extended synonym processing" || len(ext.Members) != 4 || len(ext.Installation.Declarations) != 4 {
		t.Fatal("released provider recipe", control, ext)
	}
	template, ok := v.TSTemplate(ext.Members[2])
	if !ok || template.Init != ext.Members[0] || template.Lexize != ext.Members[1] || extension.DictXsynTemplateProviderOf(template) != extension.StockDictXsyn {
		t.Fatal("released callback links", template)
	}
	dictionary := v.MustTSDictionary(ext.Members[3])
	if dictionary.Template != template.Address() || dictionary.Comment != "eXtended synonym dictionary" || len(dictionary.Options) != 0 || extension.DictXsynDictionaryProviderOf(dictionary) != extension.StockDictXsyn || !dictionary.Runtime.(*functions.XsynDictionary).Configured(false, "", true, false, true, true) {
		t.Fatal("released default dictionary", dictionary)
	}
	if pgcatalog.DescriptorComment(dictionary, 0) != "eXtended synonym dictionary" || pgcatalog.DescriptorComment(dictionary, 1) != "" || pgcatalog.DescriptorComment(nil, 0) != "" {
		t.Fatal("dictionary comment projection invented a subobject or unrelated comment")
	}
	for slot, name := range []string{"dxsyn_init", "dxsyn_lexize"} {
		p := v.MustProcedure(ext.Members[slot])
		if symbol, err := extension.ProcedureSymbol(v, p); err != nil || symbol != name || !p.Strict || p.Volatility != expr.Volatile || p.Installed.Parallel != 'u' || extension.DictXsynProviderOf(p) != extension.StockDictXsyn {
			t.Fatal("released helper", symbol, p, err)
		}
		for _, tc := range []struct {
			name   string
			change func(*catalog.Procedure)
		}{
			{"unsealed", func(p *catalog.Procedure) { p.Origin = nil }},
			{"copied-origin", func(p *catalog.Procedure) {
				origin := *p.Origin
				recipe := *origin.Installation
				origin.Installation = &recipe
				p.Origin = &origin
			}},
			{"slot", func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1 - slot; p.Origin = &origin }},
			{"signature", func(p *catalog.Procedure) { p.Signature = "23" }},
			{"result", func(p *catalog.Procedure) { p.Result = 25 }},
			{"strict", func(p *catalog.Procedure) { p.Strict = false }},
			{"volatility", func(p *catalog.Procedure) { p.Volatility = expr.Immutable }},
			{"parallel", func(p *catalog.Procedure) { p.Installed.Parallel = 's' }},
			{"source", func(p *catalog.Procedure) { p.Display.Source = "lookalike" }},
			{"library", func(p *catalog.Procedure) { p.Display.Binary = "$libdir/unaccent" }},
			{"provider", func(p *catalog.Procedure) { p.Implementation = extension.StockUnaccent }},
			{"principal", func(p *catalog.Procedure) { p.Owner = (&authorization.Domain{}).Principal(p.Owner.ID()) }},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				bad := *p
				tc.change(&bad)
				state := catalog.CloneState(v.State())
				state.SetDescriptor(bad.Address(), &bad)
				view := v.WithState(state)
				if _, err := extension.ProcedureSymbol(view, &bad); err == nil {
					t.Fatal("forged helper acquired released body")
				}
				if runtime, err := extension.CompileDictionary(t.Context(), view, template, nil, e.TextSearchFiles); err == nil || runtime != nil {
					t.Fatal("forged helper acquired dictionary capability", runtime, err)
				}
			})
		}
	}
	for _, change := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) {
			d.Runtime = xsynUnitRuntime(t, true, "xsyn_sample", true, false, true, true)
		},
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "rules", Value: "xsyn_sample", Supplied: true}}
		},
		func(d *catalog.TSDictionary) { d.Implementation = new(extension.DictXsynProvider) },
		func(d *catalog.TSDictionary) { d.Origin = nil },
		func(d *catalog.TSDictionary) { origin := *d.Origin; origin.Declaration = 2; d.Origin = &origin },
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
		},
	} {
		bad := *dictionary
		change(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSDictionaryClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("forged installed runtime admitted", bad)
		}
	}
	owner := *v.Owner()
	owner.Extensions = make(map[string]catalog.ExtensionImplementation, len(v.Owner().Extensions))
	for name, implementation := range v.Owner().Extensions {
		if name != "dict_xsyn" {
			owner.Extensions[name] = implementation
		}
	}
	if _, err := extension.CompileDictionary(t.Context(), catalog.NewRead(&owner, v.State()), template, nil, e.TextSearchFiles); err == nil {
		t.Fatal("stale provider registration admitted")
	}
	for _, change := range []func(*catalog.TSTemplate){
		func(d *catalog.TSTemplate) { d.Implementation = new(extension.DictXsynProvider) },
		func(d *catalog.TSTemplate) { d.Origin = nil },
		func(d *catalog.TSTemplate) { origin := *d.Origin; origin.Declaration = 3; d.Origin = &origin },
		func(d *catalog.TSTemplate) { d.Init = d.Lexize },
	} {
		bad := *template
		change(&bad)
		state := catalog.CloneState(v.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(v.WithState(state), &bad); err == nil {
			t.Fatal("forged installed template acquired genuine callback authority", bad)
		}
	}
	// A real option ALTER replaces only the dictionary generation. Earlier
	// immutable descriptors, option backing and runtime must remain untouched.
	enginetest.RoleExecute(t, e, op, "ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample,matchsynonyms=true,keeporig=false)")
	next := e.CatalogRead().MustTSDictionary(dictionary.Address())
	if next == dictionary || next.Revision() == dictionary.Revision() || next.Runtime == dictionary.Runtime || next.Origin != dictionary.Origin || dictionary.Options != nil || !dictionary.Runtime.(*functions.XsynDictionary).Configured(false, "", true, false, true, true) || !next.Runtime.(*functions.XsynDictionary).Configured(true, "xsyn_sample", true, true, false, true) {
		t.Fatal("option publication lost immutable provider generations", dictionary, next)
	}
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION dict_xsyn DROP TEXT SEARCH DICTIONARY xsyn")
	detached := e.CatalogRead().MustTSDictionary(next.Address())
	if detached.Runtime != next.Runtime || &detached.Options[0] != &next.Options[0] || detached.Origin != next.Origin {
		t.Fatal("membership copied or replaced immutable dictionary state")
	}
	enginetest.RoleExecute(t, e, op, "COMMENT ON TEXT SEARCH DICTIONARY xsyn IS 'owned comment'")
	commented := e.CatalogRead().MustTSDictionary(next.Address())
	if commented == detached || commented.Revision() == detached.Revision() || commented.Comment != "owned comment" || detached.Comment != "eXtended synonym dictionary" || commented.Runtime != detached.Runtime || commented.Origin != detached.Origin || &commented.Options[0] != &detached.Options[0] {
		t.Fatal("comment publication mutated immutable execution state or old descriptor")
	}
	enginetest.RoleExecute(t, e, op, "COMMENT ON TEXT SEARCH DICTIONARY xsyn IS NULL")
	cleared := e.CatalogRead().MustTSDictionary(next.Address())
	if cleared.Comment != "" || commented.Comment != "owned comment" || cleared.Runtime != commented.Runtime || cleared.Origin != commented.Origin {
		t.Fatal("comment removal changed runtime or retained comment")
	}
	t.Run("mixed-cohort", testDictXsynMixedCohort)
}

func testDictXsynMixedCohort(t *testing.T) {
	e, op, ext := dictXsynUnitInstalled(t)
	enginetest.RoleExecute(t, e, op, "CREATE TEXT SEARCH TEMPLATE dict_xsyn_ordinary (INIT=dxsyn_init,LEXIZE=dxsyn_lexize)")
	v := e.CatalogRead()
	template, ok := v.TSTemplate(v.NameAddress(ids.NameKey{ClassID: ids.TSTemplateClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "dict_xsyn_ordinary"}))
	if !ok {
		t.Fatal("ordinary authentic-callback template missing")
	}
	runtime, err := extension.CompileDictionary(t.Context(), v, template, nil, e.TextSearchFiles)
	if err != nil || !runtime.(*functions.XsynDictionary).Configured(false, "", true, false, true, true) || template.Implementation != nil || template.Origin != nil {
		t.Fatal("ordinary genuine-pair template did not compile independently", runtime, err)
	}
	other, _, foreign := dictXsynUnitInstalled(t)
	cohort := foreign.Address()
	distinct := cohort
	distinct.ObjectID += 1000000
	remapped := extension.RemapDescriptor(other.CatalogRead().MustProcedure(foreign.Members[1]), map[ids.ObjectAddress]ids.ObjectAddress{cohort: distinct}).(*catalog.Procedure)
	lexize := *remapped
	lexize.Owner = v.MustProcedure(ext.Members[1]).Owner
	lexize.Extension = ext.Address()
	state := catalog.CloneState(v.State())
	state.SetDescriptor(lexize.Address(), &lexize)
	mixed := v.WithState(state)
	if _, err := extension.ProcedureSymbol(mixed, &lexize); err != nil {
		t.Fatal("second genuine cohort helper was not independently authentic", err)
	}
	if runtime, err := extension.CompileDictionary(t.Context(), mixed, template, nil, e.TextSearchFiles); err == nil || runtime != nil {
		t.Fatal("two independently genuine cohorts acquired a combined dictionary capability", runtime, err)
	} else {
		enginetest.State(t, err, "0A000")
	}
}

func xsynUnitRuntime(t *testing.T, present bool, name string, m, s, k, y bool) *functions.XsynDictionary {
	t.Helper()
	d, err := functions.CompileXsynDictionary(t.Context(), present, name, m, s, k, y)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
