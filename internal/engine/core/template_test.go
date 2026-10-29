package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func templateUnitName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
func templateUnitOption(method, name string) parser.DictionaryOption {
	source := parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: name}}}
	return parser.DictionaryOption{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: method}, Source: &source}, Supplied: true}
}
func templateUnitCreate(name string) *parser.CreateTemplate {
	return &parser.CreateTemplate{Name: templateUnitName(name), Options: []parser.DictionaryOption{templateUnitOption("init", "dsimple_init"), templateUnitOption("lexize", "dsimple_lexize")}}
}

func templateUnitStandalone(t *testing.T, e *core.Engine, op *session.Operation, name string) *catalog.TSTemplate {
	t.Helper()
	s := templateUnitCreate(name)
	for i, callback := range []string{"unaccent_init", "unaccent_lexize"} {
		s.Options[i].Source = &parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: callback}}}
	}
	if err := extensionUnitExecute(t, e, op, s); err != nil {
		t.Fatal(err)
	}
	v := e.CatalogRead()
	return v.Get(v.NameAddress(ids.NameKey{ClassID: ids.TSTemplateClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name})).(*catalog.TSTemplate)
}

func TestTemplateCallbackValidation(t *testing.T) {
	for _, tc := range []struct{ method, callback, code string }{{"init", "dsimple_lexize", "42883"}, {"lexize", "dsimple_init", "42883"}, {"init", "bthandler", "42P17"}, {"lexize", "brin_minmax_add_value", "42P17"}} {
		t.Run(tc.method+tc.callback, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			s := templateUnitCreate("callback_invalid")
			i := 0
			if tc.method == "lexize" {
				i = 1
			}
			s.Options[i] = templateUnitOption(tc.method, tc.callback)
			before := e.CatalogRead()
			extensionUnitError(t, extensionUnitExecute(t, e, op, s), tc.code)
			if e.CatalogRead().State() != before.State() {
				t.Fatal("invalid callback published catalog state")
			}
		})
	}
}

func TestTemplateCapabilityValidation(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	seed := view.Get(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}).(*catalog.TSTemplate)
	standalone := *seed
	standalone.ObjectAddress.ObjectID = 90001
	standalone.ObjectName = "new_simple"
	state := catalog.CloneState(view.State())
	state.SetDescriptor(standalone.Address(), &standalone)
	state.BindName(catalog.DescriptorNameKey(&standalone), standalone.Address())
	view = view.WithState(state)
	runtime, err := extension.CompileDictionary(t.Context(), view, &standalone, nil)
	if err != nil || runtime == nil {
		t.Fatal("genuine pair on new template", runtime, err)
	}
	if _, ok := runtime.(*functions.SimpleDictionary); !ok {
		t.Fatal("wrong native compiler", runtime)
	}
	zero := standalone
	zero.Init = ids.ObjectAddress{}
	if runtime, err := extension.CompileDictionary(t.Context(), view, &zero, nil); runtime != nil || err == nil {
		t.Fatal("invented no-INIT state", runtime, err)
	} else {
		extensionUnitError(t, err, "0A000")
	}
	if _, err := extension.CompileDictionary(t.Context(), view, &zero, []catalog.DictionaryOption{{Name: "stopwords", Value: "english", Supplied: true}}); err == nil {
		t.Fatal("no-INIT options admitted")
	} else {
		extensionUnitError(t, err, "42601")
	}
	original := view.MustProcedure(seed.Init)
	for _, change := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Builtin = nil }, func(p *catalog.Procedure) { p.Result = 16 }, func(p *catalog.Procedure) { p.Signature = catalog.UnaccentLexizeSignature }} {
		p := *original
		change(&p)
		bad := catalog.CloneState(state)
		bad.SetDescriptor(p.Address(), &p)
		if runtime, err := extension.CompileDictionary(t.Context(), view.WithState(bad), &standalone, nil); runtime != nil || err == nil {
			t.Fatal("forged callback capability", runtime, err)
		}
	}
}

func TestTemplateMemberIdentity(t *testing.T) {
	e, op, installation := extensionUnitInstalled(t)
	standalone := templateUnitStandalone(t, e, op, "genuine_helper_pair")
	view := e.CatalogRead()
	template := view.Get(installation.Members[4]).(*catalog.TSTemplate)
	moved := *template
	moved.ObjectName = "moved_template"
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	state := catalog.CloneState(view.State())
	state.UnbindName(catalog.DescriptorNameKey(template))
	state.SetDescriptor(moved.Address(), &moved)
	state.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	view = view.WithState(state)
	provider := extension.CatalogProvider{Class: ids.TSTemplateClassID}
	if err := provider.Validate(view, &moved); err != nil {
		t.Fatal("genuine relabel rejected", err)
	}
	if runtime, err := extension.CompileDictionary(t.Context(), view, standalone, []catalog.DictionaryOption{{Name: "rules", Value: "unaccent", Supplied: true}}); runtime != functions.StockUnaccent || err != nil {
		t.Fatal("standalone genuine installed helpers", runtime, err)
	}
	mixed := *standalone
	mixed.Init = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}
	if runtime, err := extension.CompileDictionary(t.Context(), view, &mixed, nil); runtime != nil || err == nil {
		t.Fatal("mixed genuine callbacks acquired execution", runtime, err)
	} else {
		extensionUnitError(t, err, "0A000")
	}
	for _, change := range []func(*catalog.TSTemplate){func(d *catalog.TSTemplate) { d.Init = d.Lexize }, func(d *catalog.TSTemplate) { d.Extension.ObjectID++ }, func(d *catalog.TSTemplate) { d.Implementation = new(extension.UnaccentProvider) }} {
		bad := moved
		change(&bad)
		candidate := catalog.CloneState(state)
		candidate.SetDescriptor(bad.Address(), &bad)
		if err := provider.Validate(view.WithState(candidate), &bad); err == nil {
			t.Fatal("forged member accepted", bad)
		}
	}
	for _, corruption := range []string{"duplicate", "missing", "foreign", "sibling"} {
		candidate := catalog.CloneState(state)
		parent := *installation
		parent.Members = append([]ids.ObjectAddress(nil), installation.Members...)
		switch corruption {
		case "duplicate":
			parent.Members[3] = parent.Members[2]
		case "missing":
			candidate.RemoveDescriptor(parent.Members[2])
		case "foreign":
			p := *view.MustProcedure(parent.Members[2])
			p.Extension.ObjectID++
			candidate.SetDescriptor(p.Address(), &p)
		case "sibling":
			d := *view.MustTSDictionary(parent.Members[5])
			d.Template = standalone.Address()
			candidate.SetDescriptor(d.Address(), &d)
		}
		candidate.SetDescriptor(parent.Address(), &parent)
		if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view.WithState(candidate), &parent); err == nil {
			t.Fatal("corrupt manifest accepted", corruption)
		}
	}
}

func TestTemplateRemap(t *testing.T) {
	address := func(class, id ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: class, ObjectID: id} }
	for _, hasInit := range []bool{false, true} {
		d := catalog.TSTemplate{ObjectAddress: address(ids.TSTemplateClassID, 90001), ObjectNamespace: address(ids.NamespaceClassID, 90002), Extension: address(ids.ExtensionClassID, 90003), Lexize: address(ids.ProcedureClassID, 90005), ObjectName: "remap", ObjectRevision: 1}
		if hasInit {
			d.Init = address(ids.ProcedureClassID, 90004)
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
		for _, a := range []ids.ObjectAddress{d.Address(), d.Namespace(), d.Extension, d.Init, d.Lexize} {
			if a != (ids.ObjectAddress{}) {
				b := a
				b.ObjectID += 100
				mapping[a] = b
			}
		}
		n := extension.RemapDescriptor(&d, mapping).(*catalog.TSTemplate)
		if n.Address() != mapping[d.Address()] || n.Namespace() != mapping[d.Namespace()] || n.Extension != mapping[d.Extension] || n.Lexize != mapping[d.Lexize] || n.Init != mapping[d.Init] || d.Address().ObjectID != 90001 {
			t.Fatal("template remap links", d, n)
		}
	}
}

func TestTemplateDumpPredecessors(t *testing.T) {
	e, op, installation := extensionUnitInstalled(t)
	standalone := templateUnitStandalone(t, e, op, "standalone_predecessor")
	view := e.CatalogRead()
	// Released declarations are created in the provider replay phase before
	// ordinary dump events; they do not wait for a current extension event.
	for _, tc := range []struct {
		address ids.ObjectAddress
		want    ids.OID
	}{{ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}, 0}, {ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}, 0}, {installation.Members[4], 0}, {installation.Members[2], 0}} {
		got, err := (control.Resolver{}).Predecessor(view, tc.address, ids.ObjectAddress{})
		if err != nil || got != tc.want {
			t.Fatal("actual predecessor", tc.address, got, tc.want, err)
		}
	}
	if got, err := (control.Resolver{}).Predecessor(view, standalone.Address(), ids.ObjectAddress{}); err != nil || got != standalone.Address().ObjectID {
		t.Fatal(got, err)
	}
}

func TestTemplateDependencyClosure(t *testing.T) {
	e, op, installation := extensionUnitInstalled(t)
	s := templateUnitCreate("helper_dependent")
	for i, name := range []string{"unaccent_init", "unaccent_lexize"} {
		s.Options[i].Source = &parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}}
	}
	if err := extensionUnitExecute(t, e, op, s); err != nil {
		t.Fatal(err)
	}
	base := e.CatalogRead()
	template := base.NameAddress(ids.NameKey{ClassID: ids.TSTemplateClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "helper_dependent"})
	// Genuine members cannot be dropped independently. Inspect the normal
	// callback closure only after removing both reciprocal membership links.
	state := catalog.CloneState(base.State())
	parent := *installation
	parent.Members = []ids.ObjectAddress{installation.Members[0], installation.Members[1], installation.Members[4], installation.Members[5]}
	state.SetDescriptor(parent.Address(), &parent)
	for _, helper := range installation.Members[2:4] {
		drop := &core.DropProvider{Base: base, Owner: e, Ctx: context.Background()}
		_, err := drop.DependencyClosure([]ids.ObjectAddress{helper})
		extensionUnitError(t, err, "2BP01")
		procedure := *base.MustProcedure(helper)
		procedure.Extension = ids.ObjectAddress{}
		state.SetDescriptor(helper, &procedure)
	}
	base = base.WithState(state)
	for _, helper := range installation.Members[2:4] {
		drop := &core.DropProvider{Base: base, Owner: e, Ctx: context.Background()}
		edges, err := drop.DependencyClosure([]ids.ObjectAddress{helper})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, edge := range edges {
			if edge.Address == template {
				if !edge.Normal || edge.Target != helper {
					t.Fatal("wrong callback dependency", edge)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing callback edge", helper, edges)
		}
	}
}

// Compilation captures both real callbacks. Completion must reject replacement
// of either helper even when the numeric signature and result are unchanged.
func TestTemplatePublicationFences(t *testing.T) {
	for _, fence := range []string{"init", "lexize", "cancel"} {
		t.Run("definition_"+fence, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(auth.Catalog)
			stage.AuthorizeLocked(auth, op.Session)
			err = extension.StageCreateTemplate(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, templateUnitCreate("definition_fenced"))
			e.Mu.Unlock()
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(op.Context(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if fence == "cancel" {
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			} else {
				id := ids.OID(3725)
				if fence == "lexize" {
					id = 3726
				}
				procedure := *e.CatalogRead().MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id})
				procedure.ObjectRevision = procedure.Revision() + 1
				other := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&procedure); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			if _, err := e.PublishCatalogOperation(op, nil, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
				t.Fatal("stale/canceled template definition published", fence)
			}
			if e.CatalogRead().State() != incumbent.State() {
				t.Fatal("failed template publication changed catalog")
			}
		})
	}
	for _, method := range []string{"init", "lexize", "cancel"} {
		t.Run(method, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			stmt := dictionaryUnitCreate("template_fenced_dict")
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			if method == "cancel" {
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			} else {
				a := p.Template.Init
				if method == "lexize" {
					a = p.Template.Lexize
				}
				proc := *e.CatalogRead().MustProcedure(a)
				proc.ObjectRevision = proc.Revision() + 1
				other := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&proc); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			}
			before := e.CatalogRead()
			if p.Current(before) && method != "cancel" {
				t.Fatal("stale callbacks remained current")
			}
			e.Mu.Lock()
			completed, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if completed != nil || err == nil || e.CatalogRead().State() != before.State() {
				t.Fatal("rejected completion published", completed, err)
			}
		})
	}
}
