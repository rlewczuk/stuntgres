package engine

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
)

// TestRoleHostStageless pins the role.Host seam: a ddlHost without a stage
// serves the GRANT target lookups (function paths, relations, argument types
// and parameters), and as role.StageHost its OwnerStage is the wrapped stage.
func TestRoleHostStageless(t *testing.T) {
	e := New()
	op, auth := conversionUnitAuth(t, e)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	var h role.Host = RoleHost{DDLHost{E: e, Op: op}}
	for _, name := range []parser.ObjectName{ddlHostName("lower"), ddlHostName("pg_catalog", "lower")} {
		got, err := h.SearchNamespaces(auth, name, ids.ProcedureClassID)
		if err != nil {
			t.Fatal(err)
		}
		want, err := EngineLookupNamespaces(e, auth, name, analyze.FunctionLookup)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want.Count() {
			t.Fatal("function path count", name, got, want)
		}
		for i := range got {
			if got[i] != want.At(i) {
				t.Fatal("function path order", name, got, want)
			}
		}
	}
	ref, err := h.ResolveRelation(auth, ddlHostName("pg_catalog", "pg_class"))
	if err != nil || ref.Address.ClassID != ids.RelationClassID || auth.Catalog.Get(ref.Address).Name() != "pg_class" {
		t.Fatal("relation", ref, err)
	}
	types, err := h.ArgumentTypes(auth)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := types(h.Context(), parser.TypeName{Name: "integer"}); err != nil || id != 23 {
		t.Fatal("argument type", id, err)
	}
	key, err := h.CanonicalParameter("Search_Path")
	if err != nil || key != "search_path" {
		t.Fatal("canonical parameter", key, err)
	}
	if _, err := h.CanonicalParameter("no_such_parameter"); err == nil {
		t.Fatal("an unknown parameter was accepted")
	}
	parameter, err := h.GrantParameter(auth, key)
	if err != nil || parameter.Name() != key || parameter.Address().ClassID != ids.ParameterClassID {
		t.Fatal("grant parameter", parameter, err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var sh role.StageHost = RoleHost{DDLHost{E: e, Op: op, CatalogStage: stage}}
	if sh.OwnerStage() != role.OwnedCleanupStage(stage) {
		t.Fatal("OwnerStage is not the wrapped stage")
	}
}
