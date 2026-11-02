package enginetests_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"testing"
)

func TestCatalogTableOIDSubobjectValidation(t *testing.T) {
	read := engine.New().CatalogRead()
	seen := map[string]bool{}
	for _, descriptor := range read.State().Descriptors() {
		r, ok := descriptor.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.CatalogRelation {
			continue
		}
		ns, ok := read.Namespace(r.Namespace())
		if !ok || ns.Address().ObjectID != ids.PGCatalogNamespaceOID {
			continue
		}
		want := !r.VirtualView && pgcatalog.SummaryViewDefinition(r.Address().ObjectID) == ""
		if r.CatalogView == want || pgcatalog.HasSystemAttributes(read, r) != want {
			t.Fatal("canonical catalog eligibility diverged", r.Name(), r.CatalogView, want)
		}
		for _, subID := range []int32{-1, -2, -4, -6} {
			if r.HasSupportedAttribute(subID) != want || r.HasAttribute(subID) {
				t.Fatal("system attribute eligibility or dense schema diverged", r.Name(), subID)
			}
		}
		if r.HasSupportedAttribute(-3) || r.HasSupportedAttribute(-5) {
			t.Fatal("unsupported command identity became executable", r.Name())
		}
		if r.Name() == "pg_class" || r.Name() == "pg_tables" {
			seen[r.Name()] = true
		}
		if r.Name() == "pg_tables" && (r.VirtualView || !r.CatalogView) {
			t.Fatal("summary view confused with execution VirtualView")
		}
	}
	if len(seen) != 2 {
		t.Fatal("bootstrap controls absent", seen)
	}
}
