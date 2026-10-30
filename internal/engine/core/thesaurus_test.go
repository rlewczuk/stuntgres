package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func thesaurusCoreSetup(t *testing.T) (*core.Engine, *session.Session) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.thes_unit_sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)",
		"CREATE TEXT SEARCH DICTIONARY public.thes_unit (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.thes_unit_sub')",
		"CREATE TEXT SEARCH CONFIGURATION public.thes_unit_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION public.thes_unit_cfg ALTER MAPPING FOR asciiword WITH public.thes_unit,public.thes_unit_sub",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s
}

func thesaurusCreateAST(name string) *parser.CreateDictionary {
	option := func(name, value string) parser.DictionaryOption {
		return parser.DictionaryOption{CollationOption: parser.CollationOption{Name: parser.Identifier{Text: name}, Value: value}, Supplied: true}
	}
	return &parser.CreateDictionary{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: name}}}, Options: []parser.DictionaryOption{option("template", "thesaurus"), option("dictfile", "thesaurus_sample"), option("dictionary", "public.thes_unit_sub")}}
}

func TestThesaurusCallbackCapability(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	view := e.CatalogRead()
	template := view.Get(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3742}).(*catalog.TSTemplate)
	if template.Name() != "thesaurus" || template.Init.ObjectID != 3740 || template.Lexize.ObjectID != 3741 {
		t.Fatal("genuine template", template)
	}
	for _, address := range []ids.ObjectAddress{template.Init, template.Lexize} {
		routine, ok := view.Procedure(address)
		if !ok || routine.Builtin == nil || routine.Builtin.Identity.ID != address.ObjectID || routine.Result != 2281 {
			t.Fatal("genuine callback", routine)
		}
	}
	// A renamed/moved genuine template keeps execution capability.
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH TEMPLATE public.thes_callbacks (INIT=pg_catalog.thesaurus_init,LEXIZE=pg_catalog.thesaurus_lexize)")
	enginetest.SessionSQL(t, s, "ALTER TEXT SEARCH TEMPLATE public.thes_callbacks RENAME TO thes_callbacks_renamed")
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.thes_other_unit (TEMPLATE=public.thes_callbacks_renamed,DictFile=thesaurus_sample,Dictionary='public.thes_unit_sub')")
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
	p, err := extension.PrepareDictionary(schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, thesaurusCreateAST("thes_forged"))
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	state := catalog.CloneState(p.View.State())
	helper := *p.View.Get(template.Init).(*catalog.Procedure)
	builtin := *helper.Builtin
	builtin.Identity.ID = 3725
	helper.Builtin = &builtin
	state.SetDescriptor(helper.Address(), &helper)
	p.View = p.View.WithState(state)
	if runtime, err := extension.CompileDictionary(context.Background(), p.View, template, p.Options, e.TextSearchFiles); runtime != nil || err == nil {
		t.Fatal("forged callback acquired capability", runtime, err)
	}
}

func TestThesaurusPreparationCurrentness(t *testing.T) {
	e, _ := thesaurusCoreSetup(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stmt := thesaurusCreateAST("thes_detached")
	stage, p := dictionaryUnitPrepare(t, e, op, stmt)
	defer e.DiscardCatalogStage(stage)
	if p.Thesaurus == nil || p.Thesaurus.Normalizer == nil || !p.Current(e.CatalogRead()) {
		t.Fatal("captured normalizer", p.Thesaurus)
	}
	sub := *p.Thesaurus.Normalizer
	sub.ObjectRevision++
	changed, err := functions.CompileSimpleDictionary(context.Background(), false, "english")
	if err != nil {
		t.Fatal(err)
	}
	sub.Runtime = changed
	sub.Options = extension.MergeDictionaryOptions(sub.Options, []catalog.DictionaryOption{{Name: "accept", Value: "false", Supplied: true}})
	replacement := e.BeginCatalogStage(e.CatalogRead())
	defer e.DiscardCatalogStage(replacement)
	if err := replacement.Replace(&sub); err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, replacement)
	incumbent := e.CatalogRead()
	if p.Current(incumbent) {
		t.Fatal("normalizer effective revision change accepted")
	}
	e.Mu.Lock()
	result, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
	e.Mu.Unlock()
	if result != nil || !errors.Is(err, ids.ErrCatalogReplaced) || e.CatalogRead().State() != incumbent.State() {
		t.Fatal("stale detached compile publication", result, err)
	}
	if p.Runtime == nil || p.Thesaurus.Normalizer.Runtime == changed {
		t.Fatal("candidate inputs were mutated")
	}
	t.Run("CurrentSourceAuthority", func(t *testing.T) {
		engine, session := thesaurusCoreSetup(t)
		for _, query := range []string{"CREATE ROLE thes_unit_creator LOGIN", "GRANT USAGE,CREATE ON SCHEMA public TO thes_unit_creator", "CREATE SCHEMA thes_unit_private", "CREATE TEXT SEARCH DICTIONARY thes_unit_private.sub (TEMPLATE=pg_catalog.simple,STOPWORDS=english)", "GRANT USAGE ON SCHEMA thes_unit_private TO thes_unit_creator"} {
			enginetest.SessionSQL(t, session, query)
		}
		operation, release := enginetest.RoleOperation(t, engine, "thes_unit_creator")
		defer release()
		statement := thesaurusCreateAST("thes_authority_detached")
		statement.Options[2].Value = "thes_unit_private.sub"
		candidate, preparation := dictionaryUnitPrepare(t, engine, operation, statement)
		defer engine.DiscardCatalogStage(candidate)
		acceptedRuntime := preparation.Runtime
		enginetest.SessionSQL(t, session, "REVOKE USAGE ON SCHEMA thes_unit_private FROM thes_unit_creator")
		before := engine.CatalogRead()
		engine.Mu.Lock()
		published, failure := schemacmds.CompleteDictionaryLocked(engine, operation, statement, candidate, &preparation)
		engine.Mu.Unlock()
		if published != nil || failure == nil || engine.CatalogRead().State() != before.State() || preparation.Runtime != acceptedRuntime {
			t.Fatal("captured authority was reused", published, failure)
		}
		extensionUnitError(t, failure, "42501")
	})
}

func TestThesaurusPublication(t *testing.T) {
	e, _ := thesaurusCoreSetup(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stmt := thesaurusCreateAST("thes_canceled")
	stage, p := dictionaryUnitPrepare(t, e, op, stmt)
	defer e.DiscardCatalogStage(stage)
	before := e.CatalogRead()
	runtime := p.Runtime
	options := append([]catalog.DictionaryOption(nil), p.Options...)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	op.Ctx = ctx
	e.Mu.Lock()
	published, err := schemacmds.CompleteDictionaryLocked(e, op, stmt, stage, &p)
	e.Mu.Unlock()
	if published != nil || !errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State() {
		t.Fatal("canceled detached publication", published, err)
	}
	if p.Runtime != runtime || !reflect.DeepEqual(p.Options, options) {
		t.Fatal("rejected candidate mutated immutable inputs")
	}
}

func TestThesaurusChain(t *testing.T) {
	e, s := thesaurusCoreSetup(t)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	view := e.CatalogRead()
	configType, _ := view.LookupType(sqltype.Regconfig)
	vectorType, _ := view.LookupType(sqltype.TSVector)
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit_cfg"})
	value, err := sqltype.RegTSValue(configType, uint32(address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	runtime := core.HostOf(scope)
	for range 2 {
		for _, tc := range []struct{ input, want string }{{"one two three", "'123':1"}, {"one two extra", "'12':1 'extra':2"}, {"one 7 two", "'2':3 '7':2 'one':1"}, {"booking the tickets", "'cards':3 'invitation':2 'order':1"}, {"booking", "'booking':1"}} {
			vector, err := runtime.ConvertTextSearch(context.Background(), fts.VectorConversion, true, value, tc.input, vectorType)
			if err != nil {
				t.Fatal(err)
			}
			text, err := vector.Type().Output(context.Background(), vector)
			if err != nil || text != tc.want {
				t.Fatal(tc.input, text, err)
			}
		}
	}
}

func TestThesaurusRemap(t *testing.T) {
	e, _ := thesaurusCoreSetup(t)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "thes_unit"})
	d := view.MustTSDictionary(address)
	mappedAddress := ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 92001}
	mappedNamespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 92002}
	mapped := extension.RemapDescriptor(d, map[ids.ObjectAddress]ids.ObjectAddress{address: mappedAddress, d.NormalizerNamespace: mappedNamespace}).(*catalog.TSDictionary)
	if mapped == d || mapped.Address() != mappedAddress || mapped.NormalizerNamespace != mappedNamespace || !reflect.DeepEqual(mapped.Options, d.Options) || d.NormalizerNamespace == mappedNamespace {
		t.Fatal("replay provenance remap", mapped, d)
	}
}
