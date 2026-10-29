package routine

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestRoutineInternalMetadataDescriptors(t *testing.T) {
	// Independent released parser/vector/DDL-receiver/RI roster. This is not
	// the complete identity ledger and does not grant executable providers.
	roster := [...]ids.OID{40,41,54,55,88,2410,2411,2420,2421,3717,3718,3719,3720,3721,1644,1645,1646,1647,1648,1649,1650,1651,1652,1653,1654,1655}
	seen := make(map[uint32]int)
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		if d.Builtin == nil { continue }
		for _, id := range roster {
			if d.Address().ObjectID != id { continue }
			seen[uint32(id)]++
			if d.Builtin != BuiltinRoutine(id) || d.Builtin.Providers != 0 || d.RoutineKind != catalog.FunctionRoutine || !d.Strict || d.ReturnsSet() || d.Display.Language != catalog.InternalRoutineLanguage || d.Display.Source != d.Name() || d.Display.HasBinary || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.CostEstimate() != 1 {
				t.Fatalf("invalid metadata-only descriptor: %+v",d)
			}
			want := expr.Immutable
			if id >= 1644 && id <= 1655 { want = expr.Volatile }
			if d.Volatility != want || d.Builtin.Parallel != 's' || d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 { t.Fatal("released routine attributes",d) }
			var inputs []sqltype.ID
			if d.Signature != "" {
				for token := range strings.SplitSeq(string(d.Signature),",") {
					n,err := strconv.ParseUint(token,10,32)
					if err != nil { t.Fatal(err) }
					inputs = append(inputs,sqltype.ID(n))
				}
			}
			for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider,functions.OperatorRoutineProvider} {
				if _,err := LookupBuiltinRoutineDeclaration(provider,d.Name(),inputs);err == nil { t.Fatal("metadata descriptor acquired executable authority",d.Name()) }
			}
		}
	}
	for _,id := range roster { if seen[uint32(id)] != 1 { t.Fatal("descriptor multiplicity",id,seen[uint32(id)]) } }
}
