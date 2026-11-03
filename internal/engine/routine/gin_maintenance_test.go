package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestGINMaintenanceRoutineDeclaration(t *testing.T) {
	const oid ids.OID = 3789
	declaration := BuiltinRoutine(oid)
	identity := catalog.RoutineIdentity{ID: oid, Namespace: ids.PGCatalogNamespaceOID, Name: "gin_clean_pending_list", Signature: "2205"}
	if declaration == nil || declaration.Identity != identity || declaration.Kind != catalog.FunctionRoutine || declaration.Result != 20 || !declaration.Strict || declaration.Volatility != expr.Volatile || declaration.Parallel != 'u' || declaration.ReturnsSet || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != functions.GinMaintenanceRoutineProvider || declaration.Outputs != nil {
		t.Fatal("released GIN maintenance declaration", declaration)
	}
	want := builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: "gin_clean_pending_list"}
	if display, ok := builtinRoutineDisplay[oid]; !ok || display != want || BuiltinRoutineCost(declaration) != 1 {
		t.Fatal("released GIN maintenance display", display)
	}
	owner := (&authorization.Domain{}).Principal(10)
	seen := 0
	for _, procedure := range BuiltinProcedures(owner) {
		if procedure.Address().ObjectID != oid {
			continue
		}
		seen++
		if procedure.Owner != owner || procedure.Builtin != declaration || procedure.Name() != identity.Name || procedure.Namespace().ObjectID != ids.PGCatalogNamespaceOID || procedure.Signature != identity.Signature || procedure.Display.Language != catalog.InternalRoutineLanguage || procedure.Display.Source != want.Source || procedure.Display.Support != 0 || procedure.Display.Leakproof || procedure.Display.OmitAllArgTypes || procedure.Cost != 1 || procedure.RowEstimate() != 0 || procedure.InputNames != nil || procedure.OutputDeclaration() != nil || procedure.DeclarationBody != nil {
			t.Fatal("complete GIN maintenance seed lost released metadata", procedure)
		}
	}
	if seen != 1 {
		t.Fatal("missing or duplicate executable GIN maintenance seed", seen)
	}
}
