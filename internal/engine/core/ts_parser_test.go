package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

var parserSeedAddress = ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722}

func parserCreateNode(name string) *parser.CreateParser {
	s := &parser.CreateParser{Name: configName(name)}
	for _, method := range []struct{ name, routine string }{{"start", "prsd_start"}, {"gettoken", "prsd_nexttoken"}, {"end", "prsd_end"}, {"lextypes", "prsd_lextype"}} {
		s.Options = append(s.Options, parser.ConfigurationOption{Name: parser.Identifier{Text: method.name}, Value: configName(method.routine), Supplied: true})
	}
	return s
}

func parserStage(t *testing.T, e *core.Engine, op *session.Operation, statement parser.Statement) (*core.CatalogStage, error) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	host := schemacmds.SchemaHost{DDLHost: core.DDLHost{E: e, Op: op, CatalogStage: stage}}
	switch s := statement.(type) {
	case *parser.CreateParser:
		err = ddl.StageCreateParser(host, auth, s)
	case *parser.AlterParser:
		err = ddl.StageAlterParser(host, auth, s)
	case *parser.DropParser:
		err = ddl.StageDropParser(host, auth, s)
	}
	return stage, err
}

func TestTSParserProvider(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	seed := view.MustTSParser(parserSeedAddress)
	provider := catalog.TSParserProvider{}
	if err := provider.Validate(view, seed); err != nil {
		t.Fatal(err)
	}
	owner, acl, columns := access.DescriptorACL(seed)
	if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || columns != nil || access.ClassPrivileges(seed) != 0 {
		t.Fatal("parser acquired owner or ACL")
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	stage, err := parserStage(t, e, op, parserCreateNode("parser_ownerless"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	staged := e.StageRead(stage)
	custom := staged.MustTSParser(staged.NameAddress(ids.NameKey{ClassID: ids.TSParserClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "parser_ownerless"}))
	if err := provider.Validate(staged, custom); err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range []*catalog.TSParser{seed, custom} {
		for reference := range access.DescriptorRoleReferences(descriptor) {
			t.Fatal("parser acquired a shared role dependency", descriptor.Address(), reference)
		}
		for _, dependency := range descriptor.Dependencies() {
			if dependency.ClassID == ids.RoleClassID {
				t.Fatal("parser acquired an owner dependency", dependency)
			}
		}
	}
	for _, change := range []func(*catalog.TSParser){
		func(d *catalog.TSParser) { d.Start = 0 },
		func(d *catalog.TSParser) { d.Start = 3719 },
		func(d *catalog.TSParser) { d.End = 3721 },
		func(d *catalog.TSParser) { d.LexType = 3719 },
		func(d *catalog.TSParser) { d.Headline = 3721 },
		func(d *catalog.TSParser) { d.ObjectRevision = 0 },
		func(d *catalog.TSParser) { d.ObjectName = "unbound" },
		func(d *catalog.TSParser) { d.ObjectNamespace.ObjectID = 999999 },
	} {
		next := *seed
		change(&next)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(next.Address(), &next)
		if err := provider.Validate(view.WithState(state), &next); err == nil {
			t.Fatal("invalid authority admitted", next)
		}
	}
	foreign := *seed
	if err := provider.Validate(view, &foreign); err == nil {
		t.Fatal("foreign descriptor pointer admitted")
	}
	// A genuine represented dsimple_init procedure has the right LEXTYPES SQL
	// signature, but not that native parser capability. EXECUTE ACL is irrelevant.
	state := catalog.CloneState(view.State())
	procedure := *view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725})
	procedure.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(procedure.Address(), &procedure)
	next := *seed
	next.Headline, next.LexType = 0, 3725
	state.SetDescriptor(next.Address(), &next)
	checked := view.WithState(state)
	if err := provider.Validate(checked, &next); err != nil {
		t.Fatal("signature validation imposed callback execution privilege or capability", err)
	}
	if _, err := catalog.ResolveParserRuntime(checked, &next); err != nil {
		t.Fatal(err)
	}
	if seed.LexType != 3721 || view.Get(procedure.Address()) == &procedure {
		t.Fatal("candidate mutated original root")
	}
}

func TestTSParserNativeRuntime(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	seed := view.MustTSParser(parserSeedAddress)
	want := []fts.DocumentToken{{ID: 1, Text: "Word"}, {ID: 12, Text: " "}, {ID: 22, Text: "123"}, {ID: 12, Text: " "}, {ID: 4, Text: "foo@example.com"}, {ID: 12, Text: " "}, {ID: 14, Text: "http://"}, {ID: 5, Text: "example.com/stuff/index.html"}, {ID: 6, Text: "example.com"}, {ID: 18, Text: "/stuff/index.html"}, {ID: 12, Text: " !"}}
	for _, tc := range []struct {
		start, token, end, headline, lextype ids.OID
		parse, types                         bool
	}{
		{3717, 3718, 3719, 0, 3721, true, true},
		{1835, 3718, 3719, 0, 3721, false, true},
		{3717, 3557, 3719, 0, 3721, false, true},
		{3717, 3718, 5033, 0, 3721, false, true},
		{3717, 3718, 3719, 0, 3725, true, false},
	} {
		d := *seed
		d.Start, d.Token, d.End, d.Headline, d.LexType = tc.start, tc.token, tc.end, tc.headline, tc.lextype
		state := catalog.CloneState(view.State())
		state.SetDescriptor(d.Address(), &d)
		runtime, err := catalog.ResolveParserRuntime(view.WithState(state), &d)
		if err != nil {
			t.Fatal("valid SQL callback reference rejected", err)
		}
		// No lock is held during actual native execution. Runtime owns no view.
		tokens, err := runtime.Parse(context.Background(), "Word 123 foo@example.com http://example.com/stuff/index.html !")
		if tc.parse {
			if err != nil || !reflect.DeepEqual(tokens, want) {
				t.Fatal("stock callback output", tokens, err)
			}
		} else {
			extensionUnitError(t, err, "0A000")
			if tokens != nil {
				t.Fatal("unsupported callback returned prefix/default tokens")
			}
		}
		types, err := runtime.TokenTypes(context.Background())
		if tc.types {
			if err != nil || len(types) != 23 || types[0].Alias != "asciiword" || types[22].Alias != "entity" {
				t.Fatal(types, err)
			}
		} else {
			extensionUnitError(t, err, "0A000")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if tokens, err := runtime.Parse(ctx, "Word"); !errors.Is(err, context.Canceled) || tokens != nil {
			t.Fatal("canceled callback", tokens, err)
		}
	}
}

func TestTSParserStaging(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	base := e.CatalogRead()
	stage, err := parserStage(t, e, op, parserCreateNode("parser_unit"))
	if err != nil {
		t.Fatal(err)
	}
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	key := ids.NameKey{ClassID: ids.TSParserClassID, Namespace: ns, Name: "parser_unit"}
	d := e.StageRead(stage).MustTSParser(e.StageRead(stage).NameAddress(key))
	if d.Headline != 0 || d.Start != 3717 || base.NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("private creation", d)
	}
	extensionUnitPublish(t, e, op, stage)
	before := e.CatalogRead()
	original := before.MustTSParser(d.Address())
	for _, s := range []*parser.AlterParser{
		{Name: configName("parser_unit"), Action: "rename", Target: parser.Identifier{Text: "parser_new"}},
		{Name: configName("parser_new"), Action: "schema", Target: parser.Identifier{Text: "pg_catalog"}},
	} {
		stage, err = parserStage(t, e, op, s)
		if err != nil {
			t.Fatal(err)
		}
		next := e.StageRead(stage).MustTSParser(d.Address())
		if next == d || next.Address() != d.Address() || next.Revision() <= d.Revision() || next.Start != d.Start || next.Token != d.Token || next.End != d.End || next.Headline != d.Headline || next.LexType != d.LexType {
			t.Fatal("mutable or replaced identity", next, d)
		}
		extensionUnitPublish(t, e, op, stage)
		d = next
	}
	if before.NameAddress(key) != original.Address() || before.Get(original.Address()) != original || e.CatalogRead().NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("name cutover mutated old snapshot")
	}
	bad, err := parserStage(t, e, op, &parser.DropParser{Names: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "parser_new"}}}, configName("parser_missing")}})
	defer e.DiscardCatalogStage(bad)
	extensionUnitError(t, err, "42704")
	if e.CatalogRead().Get(d.Address()) != d {
		t.Fatal("failed list DROP published")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelOp, finish, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	candidate, err := parserStage(t, e, cancelOp, parserCreateNode("parser_cancel"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(candidate)
	prepared, err := e.ValidateCatalogStage(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	incumbent := e.CatalogRead()
	cancel()
	_, err = e.PublishCatalogOperation(cancelOp, nil, prepared, core.PublicationToken{Epoch: candidate.Base.Epoch()})
	if !errors.Is(err, context.Canceled) || e.CatalogRead().State() != incumbent.State() {
		t.Fatal("canceled parser published", err)
	}
	seedStage, err := parserStage(t, e, op, &parser.AlterParser{Name: configName("default"), Action: "rename", Target: parser.Identifier{Text: "seed_current"}})
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(seedStage)
	if e.StageRead(seedStage).MustTSParser(parserSeedAddress).Name() != "seed_current" || base.MustTSParser(parserSeedAddress).Name() != "default" {
		t.Fatal("default did not use ordinary immutable descriptor")
	}
}

func TestTSParserDependencies(t *testing.T) {
	e := engine.New()
	originalRoot := e.CatalogRead()
	denied := *originalRoot.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725})
	denied.ACL = authorization.ACLValue{Explicit: true}
	deniedRoot := catalog.CloneState(originalRoot.State())
	deniedRoot.SetDescriptor(denied.Address(), &denied)
	e.Mu.Lock()
	e.Catalog.PublishLocked(deniedRoot)
	e.Mu.Unlock()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	create := parserCreateNode("parser_dependencies")
	create.Options[3].Value = configName("dsimple_init")
	stage, err := parserStage(t, e, op, create)
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	view := e.StageRead(stage)
	key := ids.NameKey{ClassID: ids.TSParserClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "parser_dependencies"}
	d := view.MustTSParser(view.NameAddress(key))
	callback := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3725}
	if !slices.Contains(d.Dependencies(), callback) || !slices.Contains(stage.DomainBases, catalog.Descriptor(view.MustProcedure(callback))) {
		t.Fatal("represented callback dependency not retained")
	}
	if !stored.DependPinned(view, nil, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3717}) || stored.DependPinned(view, nil, d.Address()) {
		t.Fatal("pinning confused parser or stock callback identities")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): {ClassID: ids.TSParserClassID, ObjectID: 92001}, d.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 92002}, callback: {ClassID: ids.ProcedureClassID, ObjectID: 92003}}
	mapped := extension.RemapDescriptor(d, mapping).(*catalog.TSParser)
	if mapped.Address() != mapping[d.Address()] || mapped.Namespace() != mapping[d.Namespace()] || mapped.LexType != 92003 || mapped.Start != d.Start || d.LexType != 3725 {
		t.Fatal("parser remap lost callback identity", mapped, d)
	}
	config := *e.CatalogRead().MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	config.Parser = d.Address()
	moved := extension.RemapDescriptor(&config, mapping).(*catalog.TSConfiguration)
	if moved.Parser != mapped.Address() || config.Parser != d.Address() {
		t.Fatal("configuration did not remap actual parser")
	}
	config.ObjectAddress.ObjectID = 92010
	config.ObjectNamespace = d.Namespace()
	config.ObjectName = "parser_dependency_config"
	config.ObjectRevision = 10
	config.Mappings = nil
	state := catalog.CloneState(view.State())
	state.SetDescriptor(config.Address(), &config)
	state.BindName(catalog.DescriptorNameKey(&config), config.Address())
	input := view.WithState(state)
	if err := (catalog.TSConfigurationProvider{}).Validate(input, &config); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []parser.DropMode{parser.DropRestrict, parser.DropCascade} {
		retirement := e.BeginCatalogStage(input)
		provider := core.StageDropProvider(context.Background(), retirement, nil, nil, nil)
		err := provider.ExpressionDependencies([]ids.ObjectAddress{d.Address()}, mode, ids.ObjectAddress{})
		if mode == parser.DropRestrict {
			extensionUnitError(t, err, "2BP01")
			if e.StageRead(retirement).Get(d.Address()) != d || e.StageRead(retirement).Get(config.Address()) != &config {
				t.Fatal("RESTRICT retired live dependency")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			current := e.StageRead(retirement)
			if current.Get(d.Address()) != nil || current.Get(config.Address()) != nil || current.Get(callback) != input.Get(callback) {
				t.Fatal("CASCADE did not own parser/configuration closure or retired callback")
			}
		}
		if input.Get(d.Address()) != d || input.Get(config.Address()) != &config || e.CatalogRead().Get(d.Address()) != nil {
			t.Fatal("dependency retirement changed original catalog roots")
		}
		e.DiscardCatalogStage(retirement)
	}
}
