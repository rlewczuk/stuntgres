package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestReplicationOriginStateDeclaration(t *testing.T) {
	type fact struct {
		name, signature string
		result          ids.OID
		volatility      expr.Volatility
		parallel        byte
	}
	facts := map[ids.OID]fact{
		6003: {"pg_replication_origin_create", "25", 26, expr.Volatile, 'u'},
		6004: {"pg_replication_origin_drop", "25", 2278, expr.Volatile, 'u'},
		6005: {"pg_replication_origin_oid", "25", 26, expr.Stable, 's'},
		6012: {"pg_replication_origin_advance", "25,3220", 2278, expr.Volatile, 'u'},
		6013: {"pg_replication_origin_progress", "25,16", 3220, expr.Volatile, 'u'},
	}
	seen := 0
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		want, ok := facts[d.Address().ObjectID]
		if !ok {
			continue
		}
		seen++
		if d.Name() != want.name || string(d.Signature) != want.signature || d.Result != want.result || !d.Strict || d.Volatility != want.volatility || d.Builtin.Parallel != want.parallel || d.ReturnsSet() || d.InputNames != nil || d.LiteralDefaults != nil || d.Builtin.Defaults != 0 || d.Display.Language != catalog.InternalRoutineLanguage || d.Display.Source != want.name {
			t.Fatal("released declaration", d)
		}
	}
	if seen != 5 {
		t.Fatal("origin declaration roster", seen)
	}
}
