package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestReplicationMessagesDeclarations(t *testing.T) {
	seen := 0
	for _, d := range BuiltinProcedures(authorization.PrincipalRef{}) {
		oid := d.Address().ObjectID
		if oid != 3577 && oid != 3578 && oid != 6344 {
			continue
		}
		seen++
		signature, result, source := "", ids.OID(2278), "pg_sync_replication_slots"
		if oid == 3577 {
			signature, result, source = "16,25,25,16", 3220, "pg_logical_emit_message_text"
		}
		if oid == 3578 {
			signature, result, source = "16,25,17,16", 3220, "pg_logical_emit_message_bytea"
		}
		if string(d.Signature) != signature || d.Result != result || !d.Strict || d.Volatility != expr.Volatile || d.ReturnsSet() || d.Builtin.Parallel != 'u' || d.ACL.Explicit || d.Display.Language != catalog.InternalRoutineLanguage || d.Display.Source != source {
			t.Fatal("released declaration", d)
		}
		if oid == 6344 {
			if d.InputNames != nil || d.LiteralDefaults != nil || d.Builtin.Defaults != 0 {
				t.Fatal("sync has no inputs/defaults", d)
			}
			continue
		}
		if d.InputNames == nil || d.InputNames.Len() != 4 || d.LiteralDefaults.Len() != 1 || d.Builtin.Defaults != 1 || d.LiteralDefaults.At(0) != (catalog.RoutineLiteral{SourceType: 16, Text: "false"}) {
			t.Fatal("emit declaration owners", d)
		}
		for i, name := range []string{"transactional", "prefix", "message", "flush"} {
			if d.InputNames.At(i) != name {
				t.Fatal("actual released input name", oid, i, d.InputNames.At(i))
			}
		}
	}
	if seen != 3 {
		t.Fatal("released identity roster", seen)
	}
}
