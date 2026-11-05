package extension_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// Inspect actual sealed owners and ordinary resident view/type/array closures.
func TestPgBuffercacheNUMAInventory(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	view := e.CatalogRead()
	if len(ext.Installation.Members) != 7 || len(ext.Members) != 13 {
		t.Fatal("seven sealed callables and two ordinary view/type/array closures", ext.Installation.Members, ext.Members)
	}
	if err := extension.StockPgBuffercache.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	wantSlots := map[string]int{
		"pg_buffercache_pages": 0,
		"pg_buffercache_evict": 1,
		"pg_buffercache_evict_relation": 2,
		"pg_buffercache_evict_all": 3,
		"pg_buffercache_summary": 4,
		"pg_buffercache_usage_counts": 5,
		"pg_buffercache_numa_pages": 6,
	}
	seen := make(map[string]bool)
	views := make(map[string]*catalog.Relation)
	for _, address := range ext.Members {
		switch address.ClassID {
		case ids.ProcedureClassID:
			p := view.MustProcedure(address)
			slot, ok := wantSlots[p.Name()]
			if !ok || seen[p.Name()] || p.Origin == nil || int(p.Origin.Declaration) != slot {
				t.Fatal("appending NUMA changed an incumbent sealed slot", p)
			}
			if err := extension.StockPgBuffercache.ValidateProcedure(view, p); err != nil {
				t.Fatal("invalid genuine declaration", p.Name(), err)
			}
			if p.Name() == "pg_buffercache_numa_pages" && (p.Signature != "" || p.Result != 2249 || p.OutputDeclaration() != nil || !p.Installed.ReturnsSet || p.Display.Source != "pg_buffercache_numa_pages") {
				t.Fatal("NUMA must retain genuine anonymous-record declaration", p)
			}
			if p.Name() != "pg_buffercache_pages" && p.Name() != "pg_buffercache_numa_pages" {
				if _, err := extension.PgBuffercacheKindOf(view, p); err == nil {
					t.Fatal("declared-OUT eviction/aggregate adopted anonymous-record authority", p)
				}
			}
			seen[p.Name()] = true
		case ids.RelationClassID:
			r := view.MustRelation(address)
			views[r.Name()] = r
		}
	}
	if len(seen) != len(wantSlots) || len(views) != 2 {
		t.Fatal("missing or extra genuine callable/view members", seen, views)
	}
	for _, name := range []string{"pg_buffercache", "pg_buffercache_numa"} {
		r := views[name]
		if r == nil || r.RelationKind != catalog.ViewRelation || r.VirtualView || r.View == nil || r.RowType == 0 {
			t.Fatal("missing ordinary consumer", name, r)
		}
		row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)})
		array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
		if row.Address() == array.Address() || row.OwningRelation != r.Address() || array.OwningRelation != r.Address() || row.Extension != ext.Address() || array.Extension != ext.Address() || !slices.Contains(ext.Members, row.Address()) || !slices.Contains(ext.Members, array.Address()) {
			t.Fatal("ordinary resident closure is not genuine extension-owned state", name, row, array)
		}
	}
}
