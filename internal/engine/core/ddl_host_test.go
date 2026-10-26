package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ddlHostName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

func TestDDLHostCreationNamespaceRecapturesCopy(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	ref, next, err := h.CreationNamespace(auth, ddlHostName("pg_temp", "seam"))
	if err != nil {
		t.Fatal(err)
	}
	if stage.DescriptorCount() == 0 {
		t.Fatal("pg_temp creation staged no namespace")
	}
	if _, ok := auth.Catalog.Namespace(ref.Address); ok || auth.StagedTemporary != (ids.ObjectAddress{}) {
		t.Fatal("the argument authority was recaptured")
	}
	recaptured := core.DDLAuthorityOf(next)
	if ns, ok := recaptured.Catalog.Namespace(ref.Address); !ok || ns.Temporary == (ids.SessionToken{}) || recaptured.StagedTemporary != ref.Address {
		t.Fatal("the returned authority does not read the staged namespace", ok)
	}
}

func TestDDLHostStageKeepsLifecycle(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	e.DiscardCatalogStage(stage)
	h := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "closed", ObjectRevision: 1, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err == nil {
		t.Fatal("a discarded stage accepted a descriptor")
	}
}

func TestDDLHostSearchNamespacesMapsClasses(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, `CREATE TEMP TABLE typedef_search_seam(id INT)`)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	name := ddlHostName("seam")
	results := map[ids.OID][]ids.ObjectAddress{}
	for class, kind := range map[ids.OID]analyze.LookupKind{ids.TypeClassID: analyze.TypeLookup, ids.ConversionClassID: analyze.ConversionLookup, ids.OperatorClassID: analyze.OperatorLookup, ids.ProcedureClassID: analyze.FunctionLookup} {
		got, err := h.SearchNamespaces(auth, name, class)
		if err != nil {
			t.Fatal(err)
		}
		want, err := core.EngineLookupNamespaces(e, auth, name, kind)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want.Count() {
			t.Fatal("namespace count", class, got, want)
		}
		for i := range got {
			if got[i] != want.At(i) {
				t.Fatal("namespace order", class, got, want)
			}
		}
		results[class] = got
	}
	temporary := core.TemporaryNamespace(auth)
	if temporary == (ids.ObjectAddress{}) || !slices.Contains(results[ids.TypeClassID], temporary) || slices.Contains(results[ids.ConversionClassID], temporary) {
		t.Fatal("type and conversion searches share a lookup kind", results)
	}
	if _, err := h.SearchNamespaces(auth, name, ids.RelationClassID); err == nil {
		t.Fatal("an unsupported class was searched")
	}
}

func TestDDLHostConfigurationSourceNamespaces(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE ROLE config_source_actor",
		"CREATE SCHEMA config_source_private",
		"CREATE TEMP TABLE config_source_temp(id INT)",
		"SET search_path = config_source_private, pg_temp, pg_catalog",
		"SET SESSION AUTHORIZATION config_source_actor",
	} {
		sessionSQL(t, s, query)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	ordinary, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	e.ImportTargets = make(map[authorization.PrincipalRef]authorization.PrincipalRef)
	core.OperationReplay(op).TableDestination = true
	defer func() {
		core.OperationReplay(op).TableDestination = false
		e.ImportTargets = nil
	}()
	trusted, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.State.CurrentUser == ordinary.State.Authenticated ||
		core.PathInput(ordinary, false).View.State.CurrentUser != ordinary.State.CurrentUser ||
		core.PathInput(trusted, false).View.State.CurrentUser != trusted.State.Authenticated {
		t.Fatal("fixture did not distinguish caller from trusted definition lookup")
	}
	stage := e.BeginCatalogStage(ordinary.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	private := ordinary.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "config_source_private"})
	temporary := core.TemporaryNamespace(ordinary)
	if private == (ids.ObjectAddress{}) || temporary == (ids.ObjectAddress{}) {
		t.Fatal("missing real namespace fixtures")
	}
	assertDiagnostic := func(t *testing.T, err error, want sqlstate.Error) {
		t.Helper()
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, want) {
			t.Fatalf("diagnostic: got %#v (%v), want %#v", got, err, want)
		}
	}
	for _, authority := range []struct {
		name string
		auth core.AuthorizationView
	}{
		{"ordinary", ordinary},
		{"restore", trusted},
	} {
		t.Run(authority.name, func(t *testing.T) {
			auth := authority.auth
			before := auth
			for _, class := range []ids.OID{ids.TSConfigurationClassID, ids.TSDictionaryClassID, 3601, ids.OperatorClassID} {
				name := ddlHostName("config_source_private", "operand")
				name.Parts[0].Pos = parser.Position{Offset: 22, Character: 20}
				name.Parts[1].Pos = parser.Position{Offset: 44, Character: 42}
				source := parser.ObjectName{Parts: slices.Clone(name.Parts)}
				_, err := h.SearchConfigurationNamespaces(auth, name, class)
				assertDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for schema config_source_private"})
				got, err := h.SearchNamespaces(auth, name, class)
				if authority.name == "restore" {
					if err != nil || !slices.Equal(got, []ids.ObjectAddress{private}) {
						t.Fatal("generic trusted lookup lost destination authority", class, got, err)
					}
				} else {
					assertDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for schema config_source_private", Position: "20"})
				}
				name.Parts[0].Text = "config_source_absent"
				_, err = h.SearchConfigurationNamespaces(auth, name, class)
				assertDiagnostic(t, err, sqlstate.Error{Code: "3F000", Message: `schema "config_source_absent" does not exist`})
				_, err = h.SearchNamespaces(auth, name, class)
				assertDiagnostic(t, err, sqlstate.Error{Code: "3F000", Message: `schema "config_source_absent" does not exist`, Position: "20"})
				name.Parts[0].Text = source.Parts[0].Text
				if !reflect.DeepEqual(name, source) || !reflect.DeepEqual(auth, before) {
					t.Fatal("configuration lookup mutated source AST or caller authority", class)
				}
				path, err := h.SearchConfigurationNamespaces(auth, ddlHostName("operand"), class)
				if err != nil || slices.Contains(path, private) || slices.Contains(path, temporary) || len(path) == 0 {
					t.Fatal("semantic path retained denied or temporary namespace", class, path, err)
				}
				generic, err := h.SearchNamespaces(auth, ddlHostName("operand"), class)
				if err != nil || slices.Contains(generic, temporary) ||
					slices.Contains(generic, private) != (authority.name == "restore") {
					t.Fatal("generic path authority changed", class, generic, err)
				}
			}
			if !reflect.DeepEqual(auth, before) ||
				core.PathInput(auth, false).View.State.CurrentUser != core.PathInput(before, false).View.State.CurrentUser {
				t.Fatal("configuration lookup changed caller restore flag")
			}
		})
	}
}

func TestDDLHostRecaptureLeavesArgument(t *testing.T) {
	e := engine.New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := core.DDLHost{E: e, Op: op, CatalogStage: stage}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "recaptured", ObjectRevision: 1, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err != nil {
		t.Fatal(err)
	}
	base := auth.Catalog
	next := core.DDLAuthorityOf(h.Recapture(auth, h.StageRead()))
	if _, ok := next.Catalog.Namespace(ns.Address()); !ok || next.PairedCatalog != next.Catalog {
		t.Fatal("the returned authority does not read the stage")
	}
	if _, ok := auth.Catalog.Namespace(ns.Address()); ok || auth.Catalog != base || auth.PairedCatalog != base {
		t.Fatal("the argument authority was recaptured")
	}
}
