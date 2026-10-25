package engine

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func indexHostFixture(t *testing.T) (TableHost, AuthorizationView, partialIndexFixture) {
	t.Helper()
	f := newPartialIndexFixture(t)
	op, done := roleOperation(t, f.e, "postgres")
	t.Cleanup(done)
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stage := f.e.BeginCatalogStage(auth.Catalog)
	t.Cleanup(func() { f.e.DiscardCatalogStage(stage) })
	return TableHost{DDLHost{E: f.e, Op: op, CatalogStage: stage}}, auth, f
}

func TestIndexHostBindPredicateMatchesEngineBinding(t *testing.T) {
	h, auth, f := indexHostFixture(t)
	var _ index.Host = h
	raw, err := parser.Parse("CREATE INDEX pi ON t(k) WHERE NOT active AND k > 1")
	if err != nil {
		t.Fatal(err)
	}
	predicate := raw[0].(*parser.CreateIndex).Predicate
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	want, err := f.e.bindIndexPredicate(h.Op, sqltype.InputEnvironment{}, auth, f.parent, predicate)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.BindIndexPredicate(auth, f.parent, predicate, sqltype.InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if want == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("host predicate %#v, engine predicate %#v", got, want)
	}
}

func TestIndexHostLookupCollationKeepsUnsupportedDiagnostic(t *testing.T) {
	h, _, f := indexHostFixture(t)
	f.e.Mu.Lock()
	defer f.e.Mu.Unlock()
	_, err := h.LookupCollation(ddlHostName("ucs_basic"))
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "collation is unsupported" {
		t.Fatalf("LookupCollation(ucs_basic) error %v, want 0A000 collation is unsupported", err)
	}
	if id, err := h.LookupCollation(ddlHostName("C")); err != nil || id == 0 {
		t.Fatalf(`LookupCollation("C") = %d, %v`, id, err)
	}
}
