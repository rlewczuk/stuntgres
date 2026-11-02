package routine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type regLookupDeclarationCase struct {
	oid, result ids.OID
	name        string
}

func TestRegLookupCollRoleDeclarations(t *testing.T) {
	regLookupDeclarations(t, []regLookupDeclarationCase{
		{4195, 4191, "to_regcollation"},
		{4093, 4096, "to_regrole"},
	})
}

func TestRegLookupCallableDeclarations(t *testing.T) {
	regLookupDeclarations(t, []regLookupDeclarationCase{
		{3494, 24, "to_regproc"},
		{3479, 2202, "to_regprocedure"},
		{3492, 2203, "to_regoper"},
		{3476, 2204, "to_regoperator"},
	})
}

func regLookupDeclarations(t *testing.T, cases []regLookupDeclarationCase) {
	t.Helper()
	// REL_18_6 pg_proc.dat gives the OIDs and TEXT/result signatures;
	// pg_proc.h supplies strict/internal/parallel-safe/scalar defaults.
	owner := (&authorization.Domain{}).Principal(10)
	procedures := BuiltinProcedures(owner)
	for _, tc := range cases {
		declaration := BuiltinRoutine(tc.oid)
		identity := catalog.RoutineIdentity{ID: tc.oid, Namespace: ids.PGCatalogNamespaceOID, Name: tc.name, Signature: "25"}
		if declaration == nil || declaration.Identity != identity || declaration.Kind != catalog.FunctionRoutine || declaration.Result != tc.result || !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != functions.CatalogRoutineProvider || declaration.Outputs != nil {
			t.Fatal("released registry lookup declaration", tc, declaration)
		}
		associated, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, tc.name, []sqltype.ID{sqltype.Text})
		if err != nil || associated != declaration {
			t.Fatal("successful registry lookup association lost declaration ownership", tc, associated, err)
		}
		wantDisplay := builtinDisplayFacts{Language: catalog.InternalRoutineLanguage, Source: tc.name}
		if display, ok := builtinRoutineDisplay[tc.oid]; !ok || display != wantDisplay || BuiltinRoutineCost(declaration) != 1 {
			t.Fatal("released registry lookup display", tc, display)
		}
		seen := 0
		for _, procedure := range procedures {
			if procedure.Address().ObjectID != tc.oid {
				continue
			}
			seen++
			if procedure.Owner != owner || procedure.Builtin != declaration || procedure.Name() != tc.name || string(procedure.Signature) != "25" || procedure.Result != tc.result || !procedure.Strict || procedure.Volatility != expr.Stable || procedure.ReturnsSet() || procedure.InputNames != nil || procedure.LiteralDefaults != nil || procedure.OutputDeclaration() != nil || procedure.Display.Language != catalog.InternalRoutineLanguage || procedure.Display.Source != tc.name || procedure.Cost != 1 || procedure.RowEstimate() != 0 {
				t.Fatal("complete seed lost registry lookup declaration", tc, procedure)
			}
		}
		if seen != 1 {
			t.Fatal("missing/duplicate registry lookup seed", tc, seen)
		}
	}
}
