package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestExtensionProviderIdentity(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	view := e.CatalogRead()
	seen := map[ids.ObjectAddress]bool{}
	// extension.TestRegisterAddsOnlyValidImplementations covers the checks.
	if err := EngineRegisterExtension(e, "other", extension.StockUnaccent); err == nil {
		t.Fatal("initialized registry mutated")
	}
	for i, signature := range []catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature} {
		proc := view.MustProcedure(ext.Members[i])
		if proc.Signature != signature {
			t.Fatal("declared signature", i, proc.Signature)
		}
		vol, strict, result := expr.Stable, true, ids.OID(25)
		if i >= 2 {
			vol, strict, result = expr.Volatile, false, 2281
		}
		if proc.Volatility != vol || proc.Strict != strict || proc.Result != result {
			t.Fatal("procedure properties", i)
		}
		// Every member holds the registered provider's one implementation
		// instance; typed views of another extension's kind stay nil.
		if proc.Implementation != e.Catalog.Extensions["unaccent"] || extension.UnaccentProviderOf(proc) != extension.StockUnaccent || extension.PgcryptoProviderOf(proc) != nil || extension.IntaggProviderOf(proc) != nil {
			t.Fatal("member implementation identity", i)
		}
	}
	if tmpl, _ := view.TSTemplate(ext.Members[4]); tmpl == nil || tmpl.Implementation != extension.StockUnaccent || extension.UnaccentTemplateProviderOf(tmpl) != extension.StockUnaccent {
		t.Fatal("template implementation identity")
	}
	if dict := view.MustTSDictionary(ext.Members[5]); dict.Implementation != extension.StockUnaccent || extension.UnaccentDictionaryProviderOf(dict) != extension.StockUnaccent {
		t.Fatal("dictionary implementation identity")
	}
	if extension.UnaccentProviderOf((&catalog.Procedure{})) != nil || extension.UnaccentDictionaryProviderOf((&catalog.TSDictionary{})) != nil {
		t.Fatal("absent implementation has a typed view")
	}
	for _, a := range ext.Members {
		if seen[a] {
			t.Fatal("duplicate member")
		}
		seen[a] = true
		d := view.Get(a)
		if err := e.Catalog.Providers[a.ClassID].Validate(view, d); err != nil {
			t.Fatal(err)
		}
		if d.Namespace() != ext.TargetNamespace || catalog.ExtensionMembership(d) != ext.Address() {
			t.Fatal("broken member placement")
		}
		if _, ok := d.(*catalog.TSTemplate); ok {
			if refs := descriptorReferences(d); len(refs) != 0 {
				t.Fatal("template acquired owner", refs)
			}
		} else {
			owner, _, _ := access.DescriptorACL(d)
			if owner != ext.Owner {
				t.Fatal("superuser member ownership")
			}
		}
	}
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "unaccent", Signature: string(catalog.UnaccentTextSignature)}
	text := view.NameAddress(key)
	key.Signature = string(catalog.UnaccentDictionarySignature)
	dictionary := view.NameAddress(key)
	if text == (ids.ObjectAddress{}) || dictionary == (ids.ObjectAddress{}) || text == dictionary {
		t.Fatal("overloads collapsed")
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, EngineCatalogRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, edge := range edges {
		if edge.Referenced == ext.Address() && edge.DepType == 'e' {
			n++
			if !seen[edge.Dependent] {
				t.Fatal("foreign member")
			}
		}
	}
	if n != 6 {
		t.Fatal("membership count", n)
	}
	bad := *ext
	bad.Members = slices.Clone(ext.Members)
	bad.Members[1] = bad.Members[0]
	if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(view, &bad); err == nil {
		t.Fatal("duplicate members accepted")
	}
}
