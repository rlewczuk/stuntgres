package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func sequenceHeld(t *testing.T, c *sql.Conn, q string) *sql.Stmt {
	t.Helper()
	s, err := c.PrepareContext(context.Background(), q)
	if err != nil {
		t.Fatal(q, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func sequenceHeldValue(t *testing.T, s *sql.Stmt, want int64) {
	t.Helper()
	var value int64
	if err := s.QueryRowContext(context.Background()).Scan(&value); err != nil || value != want {
		t.Fatalf("held got=%d want=%d err=%v", value, want, err)
	}
}
func sequenceHeldMissing(t *testing.T, s *sql.Stmt) {
	t.Helper()
	var value int64
	err := s.QueryRowContext(context.Background()).Scan(&value)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
}
func sequencePreparedMatrix(t *testing.T) {
	fresh := func(t *testing.T) *sql.Conn { return sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name())) }
	t.Run("SchemaMove", func(t *testing.T) {
		c := fresh(t)
		sequenceExec(t, c, "CREATE SCHEMA moved")
		sequenceExec(t, c, "SET search_path TO public")
		sequenceExec(t, c, "CREATE SEQUENCE s START 20")
		name := sequenceHeld(t, c, "SELECT nextval('s')")
		qualified := sequenceHeld(t, c, "SELECT nextval('public.s')")
		sequenceExec(t, c, "ALTER SEQUENCE s SET SCHEMA moved")
		sequenceHeldMissing(t, name)
		var value int64
		err := qualified.QueryRowContext(context.Background()).Scan(&value)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "public.s" does not exist`, Position: "16"})
		sequenceExec(t, c, "SET search_path TO moved,public")
		sequenceHeldValue(t, name, 20)
		sequenceExec(t, c, "CREATE SEQUENCE public.s START 70")
		sequenceHeldValue(t, qualified, 70)
	})
	t.Run("EffectivePath", func(t *testing.T) {
		c := fresh(t)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 10", "CREATE SEQUENCE b.s START 100", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceHeldValue(t, s, 10)
		sequenceExec(t, c, "SET search_path TO b,a")
		sequenceHeldValue(t, s, 100)
		sequenceExec(t, c, "CREATE TEMP SEQUENCE s START 200")
		sequenceHeldValue(t, s, 200)
	})
	t.Run("GeneratorDoesNotInvalidate", func(t *testing.T) {
		c := fresh(t)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceExec(t, c, "CREATE SEQUENCE a.s START 100")
		sequenceHeldValue(t, s, 10)
		sequenceValue(t, c, "SELECT setval('b.s',40,true)", 40)
		sequenceValue(t, c, "SELECT currval('b.s')", 40)
		sequenceValue(t, c, "SELECT lastval()", 40)
		sequenceExec(t, c, "SET search_path TO a,a,b")
		sequenceHeldValue(t, s, 41)
		sequenceValue(t, c, "SELECT nextval('s')", 100)
	})
	t.Run("OptionInvalidation", func(t *testing.T) {
		for _, option := range []string{"AS BIGINT", "INCREMENT 1", "START 10", "RESTART 10", "MINVALUE 1", "NO MINVALUE", "MAXVALUE 9223372036854775807", "NO MAXVALUE", "CACHE 1", "CYCLE", "NO CYCLE", "SET UNLOGGED"} {
			t.Run(option, func(t *testing.T) {
				c := fresh(t)
				for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
					sequenceExec(t, c, q)
				}
				s := sequenceHeld(t, c, "SELECT nextval('s')")
				sequenceExec(t, c, "CREATE SEQUENCE a.s START 100")
				sequenceExec(t, c, "ALTER SEQUENCE b.s "+option)
				sequenceHeldValue(t, s, 100)
				sequenceValue(t, c, "SELECT nextval('b.s')", 10)
			})
		}
		for _, option := range []string{"OWNED BY NONE", "OWNED BY b.t.c", "OWNER TO postgres", "SET LOGGED"} {
			t.Run("NoOp/"+option, func(t *testing.T) {
				c := fresh(t)
				for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE b.t(c INT)", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
					sequenceExec(t, c, q)
				}
				s := sequenceHeld(t, c, "SELECT nextval('s')")
				sequenceExec(t, c, "CREATE SEQUENCE a.s START 100")
				sequenceExec(t, c, "ALTER SEQUENCE b.s "+option)
				sequenceHeldValue(t, s, 10)
			})
		}
	})
	t.Run("DropRecreate", func(t *testing.T) {
		c := fresh(t)
		sequenceExec(t, c, "CREATE SEQUENCE s START 10")
		next := sequenceHeld(t, c, "SELECT nextval('s')")
		curr := sequenceHeld(t, c, "SELECT currval('s')")
		sequenceExec(t, c, "DROP SEQUENCE s")
		sequenceHeldMissing(t, next)
		sequenceHeldMissing(t, curr)
		var value int64
		sequenceExec(t, c, "CREATE SEQUENCE s START 90")
		err := curr.QueryRowContext(context.Background()).Scan(&value)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "55000", Message: `currval of sequence "s" is not yet defined in this session`})
		sequenceHeldValue(t, next, 90)
		sequenceHeldValue(t, curr, 90)
		sequenceValue(t, c, "SELECT lastval()", 90)
	})
	t.Run("Transactional", func(t *testing.T) {
		c := fresh(t)
		sequenceExec(t, c, "CREATE SEQUENCE s")
		s := sequenceHeld(t, c, "SELECT nextval('s')")
		sequenceExec(t, c, "BEGIN")
		sequenceExec(t, c, "ALTER SEQUENCE s RENAME TO identity")
		sequenceHeldMissing(t, s)
		sequenceExec(t, c, "ROLLBACK")
		sequenceHeldValue(t, s, 1)
		sequenceExec(t, c, "BEGIN")
		sequenceExec(t, c, "ALTER SEQUENCE s RESTART 100")
		sequenceHeldValue(t, s, 100)
		sequenceExec(t, c, "ROLLBACK")
		sequenceHeldValue(t, s, 2)
		sequenceExec(t, c, "BEGIN")
		sequenceExec(t, c, "ALTER SEQUENCE s RENAME TO identity")
		sequenceExec(t, c, "COMMIT")
		sequenceHeldMissing(t, s)
	})
	t.Run("MultipleLiteralGroup", func(t *testing.T) {
		c := fresh(t)
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "CREATE SEQUENCE b.t START 20", "SET search_path TO a,b"} {
			sequenceExec(t, c, q)
		}
		s := sequenceHeld(t, c, "SELECT nextval('s'),nextval('t')")
		for _, q := range []string{"CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE a.t START 200", "ALTER SEQUENCE b.t CACHE 1"} {
			sequenceExec(t, c, q)
		}
		var a, b int64
		if err := s.QueryRowContext(context.Background()).Scan(&a, &b); err != nil || a != 100 || b != 200 {
			t.Fatal(a, b, err)
		}
	})
}
