package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func sequencePreparedNamespace(t *testing.T) {
	t.Run("NamespaceCreation", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceHeldValue(t, s, 10)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SEQUENCE a.s START 100"} {
			sequenceExec(t, c, q)
		}
		sequenceHeldValue(t, s, 100)
		sequenceExec(t, c, "DROP SCHEMA a CASCADE")
		sequenceHeldValue(t, s, 11)
	})
	t.Run("UserRolePath", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE USER reader", "CREATE USER reader2", "CREATE SCHEMA reader AUTHORIZATION reader", "CREATE SCHEMA reader2 AUTHORIZATION reader2", "SET SESSION AUTHORIZATION reader", "CREATE SEQUENCE reader.s START 30", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION reader2", "CREATE SEQUENCE reader2.s START 300", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION reader", `SET search_path TO "$user",public`} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceHeldValue(t, s, 30)
		sequenceExec(t, c, "RESET SESSION AUTHORIZATION")
		sequenceExec(t, c, "SET SESSION AUTHORIZATION reader2")
		sequenceHeldValue(t, s, 300)
	})
	t.Run("SameUnlogged", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE UNLOGGED SEQUENCE b.s START 10", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceExec(t, c, "CREATE SEQUENCE a.s START 100")
		sequenceExec(t, c, "ALTER SEQUENCE b.s SET UNLOGGED")
		sequenceHeldValue(t, s, 10)
		sequenceExec(t, c, "ALTER SEQUENCE b.s SET LOGGED")
		sequenceHeldValue(t, s, 100)
	})
	t.Run("NumericMissingInput", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		for _, input := range []string{"0", "-", "4294967295"} {
			s := sequenceHeld(t, c, "SELECT nextval('"+input+"'::regclass)")
			var value int64
			err := s.QueryRowContext(context.Background()).Scan(&value)
			oid := input
			if input == "-" {
				oid = "0"
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "XX000", Message: "could not open relation with OID " + oid})
		}
	})
	t.Run("ParameterMissing", func(t *testing.T) {
		c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
		sequenceExec(t, c, "CREATE SEQUENCE s START 14")
		s := sequenceHeld(t, c, "SELECT nextval($1)")
		var value int64
		err := s.QueryRowContext(context.Background(), "missing").Scan(&value)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: `unnamed portal parameter $1 = '...'`})
		if err := s.QueryRowContext(context.Background(), "s").Scan(&value); err != nil || value != 14 {
			t.Fatal(value, err)
		}
	})
}
