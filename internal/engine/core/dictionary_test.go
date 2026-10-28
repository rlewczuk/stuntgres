package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func dictionaryUnitCreate(name string) *parser.CreateDictionary {
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Options: []parser.DictionaryOption{{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "simple"}, Supplied: true}, {CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "stopwords"}, Value: "english"}, Supplied: true}}}
}

func TestDictionaryOptionsMerge(t *testing.T) {
	old := []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}, {Name: "accept", Value: "false", Supplied: true}}
	before := append([]catalog.DictionaryOption(nil), old...)
	changes := []catalog.DictionaryOption{{Name: "accept", Value: "true", Supplied: true}, {Name: "accept", Value: "off", Supplied: true}, {Name: "dummy"}, {Name: "stopwords"}}
	merged := extension.MergeDictionaryOptions(old, changes)
	want := []catalog.DictionaryOption{{Name: "accept", Value: "off", Supplied: true}}
	if !reflect.DeepEqual(merged, want) || !reflect.DeepEqual(old, before) {
		t.Fatal("ordered last-wins isolated merge", old, merged)
	}
	merged[0].Value = "on"
	if !reflect.DeepEqual(old, before) {
		t.Fatal("candidate modified old options")
	}
	serialized := catalog.DictionaryOptionsText([]catalog.DictionaryOption{{Name: "stopwords", Value: `a\b'c`, Supplied: true}, {Name: "accept", Value: "0", Supplied: true, Numeric: true}})
	if serialized != `stopwords = E'a\\b''c', accept = 0` {
		t.Fatal("typed ordered option serialization", serialized)
	}
}

func TestDictionaryProviderValidation(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	template, _ := view.TSTemplate(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727})
	dictionary := view.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	if template == nil || !template.Seeded() || !dictionary.Seeded() || dictionary.Template != template.Address() || dictionary.Runtime == nil {
		t.Fatal("genuine builtin seeds", template, dictionary)
	}
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(view, dictionary); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Owner = (&authorization.Domain{}).Principal(d.Owner.ID()) },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "accept", Value: "false", Supplied: true}}
		},
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 123456}
		},
		func(d *catalog.TSDictionary) {
			d.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 123456}
		},
	} {
		candidate := *dictionary
		change(&candidate)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(candidate.Address(), &candidate)
		if err := provider.Validate(view.WithState(state), &candidate); err == nil {
			t.Fatal("invalid dictionary admitted", candidate)
		}
	}
	bad := *template
	bad.Lexize.ObjectID++
	if _, err := extension.CompileDictionary(context.Background(), view, &bad, nil); err == nil {
		t.Fatal("unsupported provider fell back to simple")
	}
	for _, tc := range []struct {
		options []catalog.DictionaryOption
		code    string
	}{
		{[]catalog.DictionaryOption{{Name: "accept", Value: "true", Supplied: true}, {Name: "accept", Value: "false", Supplied: true}}, "22023"},
		{[]catalog.DictionaryOption{{Name: "stopwords"}}, "42601"},
		{[]catalog.DictionaryOption{{Name: "Accept", Value: "true", Supplied: true}}, "22023"},
	} {
		_, err := extension.CompileDictionary(context.Background(), view, template, tc.options)
		extensionUnitError(t, err, tc.code)
	}
	for _, id := range []ids.OID{3723, 3725, 3726} {
		d, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id})
		if !ok || !d.Strict || d.Builtin == nil || d.Builtin.Parallel != 's' {
			t.Fatal("dictionary routine seed", id, d)
		}
	}
}

func dictionaryUnitPrepare(t *testing.T, e *core.Engine, op *session.Operation, stmt parser.Statement) (*core.CatalogStage, extension.DictionaryPreparation) {
	t.Helper()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	p, err := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, stmt)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Compile(op.Context()); err != nil {
		t.Fatal(err)
	}
	return stage, p
}

func TestDictionaryPublicationFences(t *testing.T) {
	for _, fence := range []string{"cancel", "revision", "name", "owner"} {
		t.Run(fence, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			stmt := dictionaryUnitCreate("dictionary_unit")
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			before := e.CatalogRead()
			switch fence {
			case "cancel":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "revision":
				d := *p.Template
				d.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&d); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			case "name":
				other, err := schemacmds.StageDictionaryDetached(e, op, stmt)
				if err != nil {
					t.Fatal(err)
				}
				defer e.DiscardCatalogStage(other)
				extensionUnitPublish(t, e, op, other)
			case "owner":
				// Replacing the actual namespace authority fences a detached initializer.
				ns := *before.MustNamespace(p.Namespace.Address())
				ns.ObjectRevision++
				other := e.BeginCatalogStage(before)
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&ns); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock()
			published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if published != nil || err == nil {
				t.Fatal("stale dictionary initialization published", published, err)
			}
			if fence == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if fence == "name" {
				extensionUnitError(t, err, "23505")
			} else if fence != "cancel" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if e.CatalogRead().State() != incumbent.State() {
				t.Fatal("failed dictionary completion modified catalog")
			}
			if p.Runtime == nil || p.Template != before.Get(p.Template.Address()) {
				t.Fatal("initializer not retained")
			}
		})
	}
}

func TestDictionaryRemap(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d := *view.MustTSDictionary(ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 3765})
	d.ObjectAddress.ObjectID = 90001
	d.ObjectNamespace.ObjectID = 90002
	d.Template.ObjectID = 90003
	d.Options = []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}}
	runtime, err := functions.CompileSimpleDictionary(context.Background(), true, "english")
	if err != nil {
		t.Fatal(err)
	}
	d.Runtime = runtime
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): {ClassID: ids.TSDictionaryClassID, ObjectID: 91001}, d.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 91002}, d.Template: {ClassID: ids.TSTemplateClassID, ObjectID: 91003}}
	mapped := extension.RemapDescriptor(&d, mapping).(*catalog.TSDictionary)
	if mapped == &d || mapped.Address() != mapping[d.Address()] || mapped.Namespace() != mapping[d.Namespace()] || mapped.Template != mapping[d.Template] || mapped.Runtime != runtime || !reflect.DeepEqual(mapped.Options, d.Options) || d.Address().ObjectID != 90001 {
		t.Fatal("dictionary remap identity/options/provider", mapped, d)
	}
}
