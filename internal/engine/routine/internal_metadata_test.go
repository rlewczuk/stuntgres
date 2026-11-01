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
	roster := [...]ids.OID{40, 41, 54, 55, 88, 2410, 2411, 2420, 2421, 3717, 3718, 3719, 3720, 3721, 1644, 1645, 1646, 1647, 1648, 1649, 1650, 1651, 1652, 1653, 1654, 1655}
	seen := make(map[uint32]int)
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		if d.Builtin == nil {
			continue
		}
		for _, id := range roster {
			if d.Address().ObjectID != id {
				continue
			}
			seen[uint32(id)]++
			if d.Builtin != BuiltinRoutine(id) || d.Builtin.Providers != 0 || d.RoutineKind != catalog.FunctionRoutine || !d.Strict || d.ReturnsSet() || d.Display.Language != catalog.InternalRoutineLanguage || d.Display.Source != d.Name() || d.Display.HasBinary || d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.CostEstimate() != 1 {
				t.Fatalf("invalid metadata-only descriptor: %+v", d)
			}
			want := expr.Immutable
			if id >= 1644 && id <= 1655 {
				want = expr.Volatile
			}
			if d.Volatility != want || d.Builtin.Parallel != 's' || d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 {
				t.Fatal("released routine attributes", d)
			}
			var inputs []sqltype.ID
			if d.Signature != "" {
				for token := range strings.SplitSeq(string(d.Signature), ",") {
					n, err := strconv.ParseUint(token, 10, 32)
					if err != nil {
						t.Fatal(err)
					}
					inputs = append(inputs, sqltype.ID(n))
				}
			}
			for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider} {
				if _, err := LookupBuiltinRoutineDeclaration(provider, d.Name(), inputs); err == nil {
					t.Fatal("metadata descriptor acquired executable authority", d.Name())
				}
			}
		}
	}
	for _, id := range roster {
		if seen[uint32(id)] != 1 {
			t.Fatal("descriptor multiplicity", id, seen[uint32(id)])
		}
	}
}

func TestRoutineOperatorEstimatorMetadataDescriptors(t *testing.T) {
	// REL_18_6 pg_proc.dat and pg_proc.h defaults independently specify this
	// bounded roster. Metadata never supplies an executable scalar provider.
	roster := []struct {
		id        ids.OID
		name      string
		signature catalog.ProcedureSignature
	}{
		{101, "eqsel", "2281,26,2281,23"},
		{102, "neqsel", "2281,26,2281,23"},
		{103, "scalarltsel", "2281,26,2281,23"},
		{104, "scalargtsel", "2281,26,2281,23"},
		{105, "eqjoinsel", "2281,26,2281,21,2281"},
		{106, "neqjoinsel", "2281,26,2281,21,2281"},
		{107, "scalarltjoinsel", "2281,26,2281,21,2281"},
		{108, "scalargtjoinsel", "2281,26,2281,21,2281"},
		{336, "scalarlesel", "2281,26,2281,23"},
		{337, "scalargesel", "2281,26,2281,23"},
		{386, "scalarlejoinsel", "2281,26,2281,21,2281"},
		{398, "scalargejoinsel", "2281,26,2281,21,2281"},
	}
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for _, want := range roster {
		matches := 0
		for _, d := range procedures {
			if d.Address().ObjectID != want.id {
				continue
			}
			matches++
			identity := catalog.RoutineIdentity{ID: want.id, Namespace: ids.PGCatalogNamespaceOID, Name: want.name, Signature: want.signature}
			if d.Builtin == nil || d.Builtin != BuiltinRoutine(want.id) || d.Builtin.Identity != identity ||
				d.Builtin.Providers != 0 || d.Name() != want.name || d.Signature != want.signature ||
				d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) ||
				d.Result != 701 || d.RoutineKind != catalog.FunctionRoutine || !d.Strict || d.Volatility != expr.Stable ||
				d.Builtin.Parallel != 's' || d.Builtin.Defaults != 0 || d.Builtin.Variadic != 0 ||
				d.ReturnsSet() || d.Rows != 0 || d.CostEstimate() != 1 || d.SecurityDefiner ||
				d.Display != (catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: want.name}) ||
				d.SQL != nil || d.SQLBody != nil || d.SQLDefaults != nil || d.Language != (ids.ObjectAddress{}) {
				t.Fatal("invalid genuine estimator metadata", want, d)
			}
			inputs := []sqltype.ID{2281, 26, 2281, 23}
			if strings.Contains(string(want.signature), ",21,") {
				inputs = []sqltype.ID{2281, 26, 2281, 21, 2281}
			}
			for _, provider := range []expr.RoutineProvider{functions.CatalogRoutineProvider, functions.OperatorRoutineProvider, functions.EmbeddedRoutineProvider, functions.IndexSupportRoutineProvider} {
				if _, err := LookupBuiltinRoutineDeclaration(provider, want.name, inputs); err == nil {
					t.Fatal("metadata acquired scalar provider", want.name, provider)
				}
			}
		}
		if matches != 1 {
			t.Fatal("estimator descriptor multiplicity", want.id, matches)
		}
	}
}
