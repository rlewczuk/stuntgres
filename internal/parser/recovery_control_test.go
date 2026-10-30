package parser

import (
	"strings"
	"testing"
)

func TestRecoveryControlParserDiagnostics(t *testing.T) {
	for _, name := range []string{"pg_is_wal_replay_paused", "pg_get_wal_replay_pause_state", "pg_promote", "pg_wal_replay_pause", "pg_wal_replay_resume"} {
		for _, spelling := range []string{name, "pg_catalog."+name, `"`+strings.ToUpper(name)+`"`} {
			statements, err := Parse("/*é🙂*/ SELECT "+spelling+"(*)")
			if err != nil || len(statements) != 1 { t.Fatal(spelling, statements, err) }
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || !call.Star || len(call.Args) != 0 || call.Pos.Character != 15 { t.Fatal("lost failure syntax/character position", call) }
			want := name
			if strings.HasPrefix(spelling, `"`) { want = strings.ToUpper(name) }
			if call.Name.Parts[len(call.Name.Parts)-1].Text != want { t.Fatal("lost function spelling", call) }
		}
		statements, err := Parse("SELECT "+name+"() OVER ()")
		if err != nil || len(statements) != 1 { t.Fatal("decorator rejected before binder", name, err) }
		statements, err = Parse("SELECT "+name+" FROM t")
		if err != nil || len(statements) != 1 { t.Fatal(statements, err) }
		column, ok := statements[0].(*Select).Targets[0].(ColumnExpr)
		if !ok || column.Name.Parts[0].Text != name { t.Fatal("bare identifier changed", statements) }
	}
}
