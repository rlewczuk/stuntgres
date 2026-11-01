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
	"stuntgres/internal/engine/session"
)

func dictIntUnitInstalled(t *testing.T) (*core.Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dict_int")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dict_int"}))
	return e, op, ext
}

func TestDictIntProviderIdentity(t *testing.T) {
	e, op, ext := dictIntUnitInstalled(t)
	v := e.CatalogRead()
	control := extension.StockDictInt.Control()
	if control.Version != "1.0" || !control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "text search dictionary template for integers" || len(ext.Members) != 4 || len(ext.Installation.Declarations) != 4 {
		t.Fatal("released provider recipe", control, ext)
	}
	template, ok := v.TSTemplate(ext.Members[2])
	if !ok || template.Init != ext.Members[0] || template.Lexize != ext.Members[1] || extension.DictIntTemplateProviderOf(template) != extension.StockDictInt {
		t.Fatal("released callback links", template)
	}
	dictionary := v.MustTSDictionary(ext.Members[3])
	if dictionary.Template != template.Address() || len(dictionary.Options) != 0 || extension.DictIntDictionaryProviderOf(dictionary) != extension.StockDictInt || !dictionary.Runtime.(*functions.IntDictionary).Configured(6, false, false) {
		t.Fatal("released default dictionary", dictionary)
	}
	for slot, name := range []string{"dintdict_init", "dintdict_lexize"} {
		p := v.MustProcedure(ext.Members[slot])
		if symbol, err := extension.ProcedureSymbol(v, p); err != nil || symbol != name || !p.Strict || p.Volatility != expr.Volatile || p.Installed.Parallel != 'u' || extension.DictIntProviderOf(p) != extension.StockDictInt {
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
		func(d *catalog.TSDictionary) { d.Runtime = functions.NewIntDictionary(3, false, false) },
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "maxlen", Value: "3", Numeric: true, Supplied: true}}
		},
		func(d *catalog.TSDictionary) { d.Implementation = new(extension.DictIntProvider) },
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
		if name != "dict_int" {
			owner.Extensions[name] = implementation
		}
	}
	if _, err := extension.CompileDictionary(t.Context(), catalog.NewRead(&owner, v.State()), template, nil, e.TextSearchFiles); err == nil {
		t.Fatal("stale provider registration admitted")
	}
	// A real option ALTER replaces only the dictionary generation. Earlier
	// immutable descriptors, option backing and runtime must remain untouched.
	enginetest.RoleExecute(t, e, op, "ALTER TEXT SEARCH DICTIONARY intdict (maxlen=3,absval=true)")
	next := e.CatalogRead().MustTSDictionary(dictionary.Address())
	if next == dictionary || next.Revision() == dictionary.Revision() || next.Runtime == dictionary.Runtime || next.Origin != dictionary.Origin || dictionary.Options != nil || !dictionary.Runtime.(*functions.IntDictionary).Configured(6, false, false) || !next.Runtime.(*functions.IntDictionary).Configured(3, false, true) {
		t.Fatal("option publication lost immutable provider generations", dictionary, next)
	}
	enginetest.RoleExecute(t, e, op, "ALTER EXTENSION dict_int DROP TEXT SEARCH DICTIONARY intdict")
	detached := e.CatalogRead().MustTSDictionary(next.Address())
	if detached.Runtime != next.Runtime || &detached.Options[0] != &next.Options[0] || detached.Origin != next.Origin {
		t.Fatal("membership copied or replaced immutable dictionary state")
	}
	t.Run("mixed-cohort", testDictIntMixedCohort)
}

func testDictIntMixedCohort(t *testing.T) {
	e, op, ext := dictIntUnitInstalled(t)
	enginetest.RoleExecute(t, e, op, "CREATE TEXT SEARCH TEMPLATE dict_int_ordinary (INIT=dintdict_init,LEXIZE=dintdict_lexize)")
	v := e.CatalogRead()
	template, ok := v.TSTemplate(v.NameAddress(ids.NameKey{ClassID: ids.TSTemplateClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "dict_int_ordinary"}))
	if !ok {
		t.Fatal("ordinary authentic-callback template missing")
	}
	other, _, foreign := dictIntUnitInstalled(t)
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
