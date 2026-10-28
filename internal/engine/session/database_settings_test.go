package session_test

import (
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestDatabaseSettingsClassification(t *testing.T) {
	for _, s := range []*parser.AlterDatabaseSettings{{Set: &parser.Set{}}, {Reset: &parser.Reset{}}, {Reset: &parser.Reset{All: true}}, {Set: &parser.Set{}, FromCurrent: true}} {
		base, counted, insert, ok := session.ClassifyCommandTag(s, session.TxState(0))
		if !ok || base != "ALTER DATABASE" || counted || insert || session.ModifyingTag(s) != "ALTER DATABASE" || !session.ExplicitUtility(s) {
			t.Fatal("database settings utility classification", base, counted, insert, ok)
		}
		if tag, supported := session.EventCommand(s); supported || tag != "" {
			t.Fatal("database settings admitted event trigger", tag)
		}
	}
}
