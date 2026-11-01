package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLXPathRoutineAssociation(t *testing.T) {
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for _, tc := range []struct {
		oid       ids.OID
		name      string
		inputs    []sqltype.ID
		signature catalog.ProcedureSignature
		result    ids.OID
		wrapper   bool
	}{
		{2931, "xpath", []sqltype.ID{25, 142, 1009}, "25,142,1009", 143, false},
		{2932, "xpath", []sqltype.ID{25, 142}, "25,142", 143, true},
		{3049, "xpath_exists", []sqltype.ID{25, 142, 1009}, "25,142,1009", 16, false},
		{3050, "xpath_exists", []sqltype.ID{25, 142}, "25,142", 16, true},
	} {
		declaration, err := LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, tc.name, tc.inputs)
		if err != nil || declaration != BuiltinRoutine(tc.oid) || declaration.Identity != (catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, tc.signature}) {
			t.Fatal(tc, declaration, err)
		}
		if !declaration.Strict || declaration.Volatility != expr.Immutable || declaration.Parallel != 's' || declaration.Result != tc.result || declaration.ReturnsSet || declaration.Defaults != 0 {
			t.Fatal("released metadata", declaration)
		}
		found := 0
		for _, p := range procedures {
			if p.Address().ObjectID != tc.oid {
				continue
			}
			found++
			language := catalog.InternalRoutineLanguage
			source := tc.name
			if tc.wrapper {
				language, source = catalog.SQLRoutineLanguage, ""
			}
			if p.Display.Language != language || p.Display.Source != source || p.CostEstimate() != 1 || (p.DeclarationBody != nil) != tc.wrapper || p.SQLBody != nil {
				t.Fatal("display/body ownership", p)
			}
			if tc.wrapper && (len(p.Body) != 1 || p.Body[0].Routine.Address().ObjectID != tc.oid-1) {
				t.Fatal("wrapper body edge", p.Body)
			}
		}
		if found != 1 {
			t.Fatal("routine multiplicity", found)
		}
	}
}
