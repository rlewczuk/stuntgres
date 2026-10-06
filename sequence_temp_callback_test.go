package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequencePreparedPeerTemporaryNamespace(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
		sequenceExec(t, a, q)
	}
	held := sequenceHeld(t, a, "SELECT nextval('s')")
	sequenceHeldValue(t, held, 10)
	sequenceExec(t, a, "CREATE SEQUENCE a.s START 100 CACHE 1")
	sequenceHeldValue(t, held, 11)
	for _, q := range []string{"BEGIN", "CREATE TEMP SEQUENCE unrelated_temp", "COMMIT"} {
		sequenceExec(t, b, q)
	}
	sequenceHeldValue(t, held, 100)
	sequenceValue(t, a, "SELECT last_value FROM b.s", 11)
	sequenceValue(t, a, "SELECT last_value FROM a.s", 100)
	for _, name := range []string{"a.s", "b.s"} {
		var called bool
		if err := a.QueryRowContext(context.Background(), "SELECT is_called FROM "+name).Scan(&called); err != nil || !called {
			t.Fatal("called state", name, called, err)
		}
	}
	sequenceValue(t, a, "SELECT nextval('s')", 101)
	sequenceValue(t, a, "SELECT nextval('b.s')", 12)
}

func TestSequencePreparedTemporaryNoninvalidation(t *testing.T) {
	for _, scenario := range []string{"ExistingNamespace", "PhysicalEnd"} {
		t.Run(scenario, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			a, b := sequenceConn(t, db), sequenceConn(t, db)
			sequenceExec(t, b, "CREATE TEMP SEQUENCE initialized_temp")
			for _, q := range []string{"CREATE SCHEMA temp_stable_a", "CREATE SCHEMA temp_stable_b", "CREATE SEQUENCE temp_stable_b.s START 10 CACHE 1", "SET search_path TO temp_stable_a,temp_stable_b"} {
				sequenceExec(t, a, q)
			}
			held := sequenceHeld(t, a, "SELECT nextval('s')")
			sequenceHeldValue(t, held, 10)
			sequenceExec(t, a, "CREATE SEQUENCE temp_stable_a.s START 100 CACHE 1")
			sequenceHeldValue(t, held, 11)
			if scenario == "ExistingNamespace" {
				sequenceExec(t, b, "CREATE TEMP SEQUENCE another_temp")
			} else {
				// Keep A and its held statement live; returning B now closes
				// its physical driver connection, not merely its pool borrower.
				db.SetMaxIdleConns(0)
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			}
			sequenceHeldValue(t, held, 12)
			for _, state := range []struct {
				name string
				last int64
				called bool
			}{{"temp_stable_b.s", 12, true}, {"temp_stable_a.s", 100, false}} {
				var last int64
				var called bool
				if err := a.QueryRowContext(context.Background(), "SELECT last_value,is_called FROM "+state.name).Scan(&last, &called); err != nil || last != state.last || called != state.called {
					t.Fatal("noninvalidation state", state.name, last, called, err)
				}
			}
			sequenceValue(t, a, "SELECT nextval('s')", 100)
			sequenceValue(t, a, "SELECT nextval('temp_stable_b.s')", 13)
		})
	}
}
