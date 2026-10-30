package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ispellUnitEngine() *core.Engine {
	e := engine.New()
	e.TextSearchFiles = functions.NewDictionaryFiles(map[string]string{
		"unit.dict":  "root/A\nrooting/B\n",
		"unit.affix": "SFX A Y 1\nSFX A 0 ing .\n",
		"unit.stop":  "root\n",
	})
	return e
}
func ispellUnitCreate(name string) *parser.CreateDictionary {
	stmt := dictionaryUnitCreate(name)
	stmt.Options = []parser.DictionaryOption{
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "template"}, Value: "ispell"}, Supplied: true},
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "dictfile"}, Value: "unit"}, Supplied: true},
		{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: "afffile"}, Value: "unit"}, Supplied: true},
	}
	return stmt
}
func TestIspellPublicationFences(t *testing.T) {
	for _, fence := range []string{"cancel", "template", "namespace", "name"} {
		t.Run(fence, func(t *testing.T) {
			e := ispellUnitEngine()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			stmt := ispellUnitCreate("ispell_fenced")
			stage, p := dictionaryUnitPrepare(t, e, op, stmt)
			defer e.DiscardCatalogStage(stage)
			if p.Files != e.TextSearchFiles {
				t.Fatal("detached compiler lost immutable capability")
			}
			if _, ok := p.Runtime.(*functions.IspellDictionary); !ok {
				t.Fatal("nongenuine runtime", p.Runtime)
			}
			switch fence {
			case "cancel":
				ctx, cancel := context.WithCancel(op.Ctx)
				cancel()
				op.Ctx = ctx
			case "name":
				other, err := schemacmds.StageDictionaryDetached(e, op, stmt)
				if err != nil {
					t.Fatal(err)
				}
				defer e.DiscardCatalogStage(other)
				extensionUnitPublish(t, e, op, other)
			case "template":
				d := *p.Template
				d.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&d); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			case "namespace":
				d := *e.CatalogRead().MustNamespace(p.Namespace.Address())
				d.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				defer e.DiscardCatalogStage(other)
				if err := other.Replace(&d); err != nil {
					t.Fatal(err)
				}
				extensionUnitPublish(t, e, op, other)
			}
			incumbent := e.CatalogRead()
			e.Mu.Lock()
			published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
			e.Mu.Unlock()
			if published != nil || err == nil || e.CatalogRead().State() != incumbent.State() {
				t.Fatal("failed completion published", published, err)
			}
			if fence == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if fence == "name" {
				extensionUnitError(t, err, "23505")
			} else if fence != "cancel" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
		})
	}
}
func TestIspellResourcePropagation(t *testing.T) {
	e := ispellUnitEngine()
	files := e.TextSearchFiles
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE DATABASE ispell_unit_database TEMPLATE template1")
	target, err := e.SelectDatabase(context.Background(), "ispell_unit_database")
	if err != nil || target.TextSearchFiles != files || e.Cluster.Seed.TextSearchFiles != files {
		t.Fatal("logical database lost resources", err)
	}
	stage := target.RestoreStage()
	if stage.TextSearchFiles != files {
		t.Fatal("restore compiler lost destination files")
	}
	if err := engine.Reset(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if target.TextSearchFiles != files {
		t.Fatal("Reset lost startup capability")
	}
	template, ok := target.CatalogRead().TSTemplate(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3733})
	if !ok {
		t.Fatal("Reset lost genuine ispell template")
	}
	options := []catalog.DictionaryOption{{Name: "dictfile", Value: "unit", Supplied: true}, {Name: "afffile", Value: "unit", Supplied: true}}
	// Compile through the actual detached provider against each derived engine.
	for _, derived := range []*core.Engine{target, stage} {
		derivedTemplate, _ := derived.CatalogRead().TSTemplate(template.Address())
		runtime, err := extension.CompileDictionary(context.Background(), derived.CatalogRead(), derivedTemplate, options, derived.TextSearchFiles)
		if err != nil || runtime == nil {
			t.Fatal("derived compiler failed", err)
		}
	}
}

func TestIspellPreparedFoldOwnership(t *testing.T) {
	e := ispellUnitEngine()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.ispell_fold (TEMPLATE=ispell,DictFile=unit,AffFile=unit)")
	q := "SELECT ts_lexize('public.ispell_fold'::regdictionary,'rooting'::text) AS lexemes"
	ast, d := sequencePreparedDescription(t, e, s, q)
	preparedFoldExecute(t, e, s, ast, &d)
	entry := preparedFoldEntry(t, d, 1009, "{rooting,root}")
	if entry.Value.ArrayLen() != 2 || entry.Value.ArrayElement(0).Type() != entry.Result.Type.Element() || entry.Value.ArrayElement(1).Type() != entry.Result.Type.Element() {
		t.Fatal("multi-lexeme owned payload")
	}
	before := snapshotFoldDescription(d)
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ispell_fold"})
	operand := dictionaryFoldDependency(t, d, address)
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH DICTIONARY public.ispell_fold (StopWords=unit)")
	if e.CatalogRead().Get(address).Revision() == operand.Revision {
		t.Fatal("ALTER failed to advance operand revision")
	}
	preparedFoldExecute(t, e, s, ast, &d)
	accepted := d.Folds.Current()
	if accepted.Generation != before.Accepted.Generation || &accepted.Entries[0] != &before.Accepted.Entries[0] || !samePublishedFold(entry, preparedFoldEntry(t, d, 1009, "{rooting,root}")) {
		t.Fatal("multi-lexeme accepted owner replaced")
	}
	freshAST, fresh := sequencePreparedDescription(t, e, s, q)
	preparedFoldExecute(t, e, s, freshAST, &fresh)
	preparedFoldEntry(t, fresh, 1009, "{rooting}")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	local := cloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	cancel()
	err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	_ = scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
		t.Fatal("canceled multi-lexeme candidate published", err)
	}
	preparedFoldEntry(t, before, 1009, "{rooting,root}")
}

func TestIspellProviderValidation(t *testing.T) {
	e := ispellUnitEngine()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := schemacmds.StageDictionaryDetached(e, op, ispellUnitCreate("ispell_validation"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ispell_validation"})
	original := view.MustTSDictionary(address)
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(view, original); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) {
			d.Options = append([]catalog.DictionaryOption(nil), d.Options...)
			d.Options[0].Value = "other"
		},
		func(d *catalog.TSDictionary) { d.Template.ObjectID = 3727 },
		func(d *catalog.TSDictionary) { d.Owner = (catalog.TSDictionary{}).Owner },
		func(d *catalog.TSDictionary) {
			d.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 90000}
		},
	} {
		candidate := *original
		change(&candidate)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(candidate.Address(), &candidate)
		if err := provider.Validate(view.WithState(state), &candidate); err == nil {
			t.Fatal("nongenuine ispell state admitted", candidate)
		}
	}
	template, _ := view.TSTemplate(original.Template)
	bad := *template
	bad.Init.ObjectID = 3725
	bad.Lexize.ObjectID = 3726
	if extension.SimpleTemplate(&bad) || extension.IspellTemplate(&bad) {
		t.Fatal("Seeded widened simple provider identity")
	}
	state := catalog.CloneState(view.State())
	state.SetDescriptor(bad.Address(), &bad)
	if err := (extension.CatalogProvider{Class: ids.TSTemplateClassID}).Validate(view.WithState(state), &bad); err == nil {
		t.Fatal("wrong fixed helper pair admitted")
	}
}
