package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func ddlHostName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

func TestDDLHostCreationNamespaceRecapturesCopy(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
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
	recaptured := DDLAuthorityOf(next)
	if ns, ok := recaptured.Catalog.Namespace(ref.Address); !ok || ns.Temporary == (ids.SessionToken{}) || recaptured.StagedTemporary != ref.Address {
		t.Fatal("the returned authority does not read the staged namespace", ok)
	}
}

func TestDDLHostStageKeepsLifecycle(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	e.DiscardCatalogStage(stage)
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "closed", ObjectRevision: 1, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err == nil {
		t.Fatal("a discarded stage accepted a descriptor")
	}
}

func TestDDLHostSearchNamespacesMapsClasses(t *testing.T) {
	e := New()
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
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
	name := ddlHostName("seam")
	results := map[ids.OID][]ids.ObjectAddress{}
	for class, kind := range map[ids.OID]analyze.LookupKind{ids.TypeClassID: analyze.TypeLookup, ids.ConversionClassID: analyze.ConversionLookup, ids.OperatorClassID: analyze.OperatorLookup, ids.ProcedureClassID: analyze.FunctionLookup} {
		got, err := h.SearchNamespaces(auth, name, class)
		if err != nil {
			t.Fatal(err)
		}
		want, err := e.lookupNamespaces(auth, name, kind)
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
	temporary := temporaryNamespace(auth)
	if temporary == (ids.ObjectAddress{}) || !slices.Contains(results[ids.TypeClassID], temporary) || slices.Contains(results[ids.ConversionClassID], temporary) {
		t.Fatal("type and conversion searches share a lookup kind", results)
	}
	if _, err := h.SearchNamespaces(auth, name, ids.RelationClassID); err == nil {
		t.Fatal("an unsupported class was searched")
	}
}

func TestDDLHostRecaptureLeavesArgument(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	h := DDLHost{E: e, Op: op, CatalogStage: stage}
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 90000}, ObjectName: "recaptured", ObjectRevision: 1, Owner: auth.State.CurrentUser}
	if err := h.Stage().Create(ns); err != nil {
		t.Fatal(err)
	}
	base := auth.Catalog
	next := DDLAuthorityOf(h.Recapture(auth, h.StageRead()))
	if _, ok := next.Catalog.Namespace(ns.Address()); !ok || next.PairedCatalog != next.Catalog {
		t.Fatal("the returned authority does not read the stage")
	}
	if _, ok := auth.Catalog.Namespace(ns.Address()); ok || auth.Catalog != base || auth.PairedCatalog != base {
		t.Fatal("the argument authority was recaptured")
	}
}
