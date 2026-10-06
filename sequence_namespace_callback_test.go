package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestSequencePreparedNamespaceCallback(t *testing.T) {
	for _, event := range []string{"Create", "Rename"} {
		t.Run(event, func(t *testing.T) {
			c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SCHEMA unrelated", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
				sequenceExec(t, c, q)
			}
			held := sequenceHeld(t, c, "SELECT nextval('s')")
			sequenceHeldValue(t, held, 10)
			sequenceExec(t, c, "CREATE SEQUENCE a.s START 100 CACHE 1")
			sequenceHeldValue(t, held, 11)
			if event == "Create" {
				sequenceExec(t, c, "CREATE SCHEMA callback_created")
			} else {
				sequenceExec(t, c, "ALTER SCHEMA unrelated RENAME TO callback_renamed")
			}
			sequenceHeldValue(t, held, 100)
			sequenceValue(t, c, "SELECT last_value FROM b.s", 11)
			sequenceValue(t, c, "SELECT last_value FROM a.s", 100)
			sequenceValue(t, c, "SELECT nextval('s')", 101)
			sequenceValue(t, c, "SELECT nextval('b.s')", 12)
		})
	}
}
