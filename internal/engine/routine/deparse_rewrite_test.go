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

func TestDeparseRewriteRoutineAssociation(t *testing.T) {
	// Released REL_18_6 pg_proc.dat identities and sources, not values inferred
	// from either the typed provider or the executable declaration inventory.
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for _, tc := range []struct {
		oid       ids.OID
		name      string
		signature catalog.ProcedureSignature
		inputs    []sqltype.ID
		source    string
		deparse   functions.DeparseSignature
	}{
		{1573, "pg_get_ruledef", "26", []sqltype.ID{sqltype.Oid}, "pg_get_ruledef", functions.DeparseRule},
		{2504, "pg_get_ruledef", "26,16", []sqltype.ID{sqltype.Oid, sqltype.Bool}, "pg_get_ruledef_ext", functions.DeparseRulePretty},
		{1662, "pg_get_triggerdef", "26", []sqltype.ID{sqltype.Oid}, "pg_get_triggerdef", functions.DeparseTrigger},
		{2730, "pg_get_triggerdef", "26,16", []sqltype.ID{sqltype.Oid, sqltype.Bool}, "pg_get_triggerdef_ext", functions.DeparseTriggerPretty},
	} {
		t.Run(tc.source, func(t *testing.T) {
			// Successful typed binding uses this exact provider/formal lookup even
			// for NULL, parameterized and prepared calls, before runtime evaluation.
			declaration, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, tc.name, tc.inputs)
			if err != nil {
				t.Fatal(err)
			}
			want := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, tc.signature}
			if declaration.Identity != want || declaration != BuiltinRoutine(tc.oid) {
				t.Fatalf("association = %+v, want canonical %v", declaration, want)
			}
			if declaration.Kind != catalog.FunctionRoutine || declaration.Result != 25 || !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet || declaration.Defaults != 0 || declaration.Variadic != 0 || declaration.Providers != functions.CatalogRoutineProvider || declaration.Outputs != nil {
				t.Fatalf("incorrect released declaration: %+v", declaration)
			}
			prettyIndex := -1
			if len(tc.inputs) == 2 {
				prettyIndex = 1
			}
			if tc.deparse.Name() != tc.name || tc.deparse.PrettyIndex() != prettyIndex {
				t.Fatal("typed deparse signature disagrees with routine declaration")
			}
			count := 0
			for _, procedure := range procedures {
				if procedure.Address().ObjectID != tc.oid {
					continue
				}
				count++
				if procedure.Name() != tc.name || procedure.Signature != tc.signature || procedure.Builtin != declaration || procedure.Result != 25 || !procedure.Strict || procedure.Volatility != expr.Stable || procedure.Display.Language != catalog.InternalRoutineLanguage || procedure.Display.Source != tc.source || procedure.Cost != 1 {
					t.Fatalf("seed lost routine authority: %+v", procedure)
				}
			}
			if count != 1 {
				t.Fatalf("seeded descriptor multiplicity = %d, want 1", count)
			}
			if _, err := LookupBuiltinRoutineDeclaration(functions.OperatorRoutineProvider, tc.name, tc.inputs); err == nil {
				t.Fatal("unrelated provider acquired routine authority")
			}
			if _, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, tc.name, []sqltype.ID{sqltype.Text}); err == nil {
				t.Fatal("undeclared formal acquired routine authority")
			}
		})
	}
}
