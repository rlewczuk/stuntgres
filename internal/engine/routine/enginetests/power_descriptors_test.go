package enginetests

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func TestRoutinePowerDescriptors(t *testing.T) {
	// REL_18_6 pg_proc.dat: aliases retain their own identity and share sources.
	roster := map[ids.OID]struct {
		name      string
		signature catalog.ProcedureSignature
		result    ids.OID
		source    string
	}{
		1346: {"pow", "701,701", 701, "dpow"},
		1368: {"power", "701,701", 701, "dpow"},
		1738: {"pow", "1700,1700", 1700, "numeric_power"},
		2169: {"power", "1700,1700", 1700, "numeric_power"},
	}
	procedures := routine.BuiltinProcedures(authorization.PrincipalRef{})
	seen := make(map[ids.OID]int)
	for i := range procedures {
		d := &procedures[i]
		want, selected := roster[d.Address().ObjectID]
		if !selected {
			if d.Comment != "" {
				t.Fatalf("unrelated routine %d acquired selected description %q", d.Address().ObjectID, d.Comment)
			}
			continue
		}
		seen[d.Address().ObjectID]++
		identity := catalog.RoutineIdentity{ID: d.Address().ObjectID, Namespace: ids.PGCatalogNamespaceOID, Name: want.name, Signature: want.signature}
		if d.Builtin == nil || d.Builtin != routine.BuiltinRoutine(identity.ID) || d.Builtin.Identity != identity ||
			d.Builtin.Providers != functions.MathRoutineProvider || d.Name() != want.name || d.Signature != want.signature ||
			d.Result != want.result || d.RoutineKind != catalog.FunctionRoutine || !d.Strict || d.Volatility != expr.Immutable ||
			d.Builtin.Parallel != 's' || d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 || d.ReturnsSet() ||
			d.Rows != 0 || d.CostEstimate() != 1 || d.SecurityDefiner || d.Comment != "exponentiation" ||
			d.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.source}) {
			t.Fatalf("invalid released exponentiation descriptor: %+v", d)
		}
		formal := sqltype.ID(want.result)
		declaration, err := routine.LookupBuiltinRoutineDeclaration(functions.MathRoutineProvider, want.name, []sqltype.ID{formal, formal})
		if err != nil || declaration != d.Builtin {
			t.Fatalf("original routine association lost for %d: %v", identity.ID, err)
		}
		d.Comment = ""
	}
	for id := range roster {
		if seen[id] != 1 {
			t.Fatalf("exponentiation descriptor %d multiplicity %d", id, seen[id])
		}
	}
	// Each engine gets detached descriptors; one engine's removal cannot change
	// the immutable released template or another engine's initial text.
	for _, d := range routine.BuiltinProcedures(authorization.PrincipalRef{}) {
		if _, selected := roster[d.Address().ObjectID]; selected && d.Comment != "exponentiation" {
			t.Fatalf("descriptor copy changed the released template for %d", d.Address().ObjectID)
		}
	}
}
