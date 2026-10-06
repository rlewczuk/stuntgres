package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSequenceGrammarFormsAndWholeInput(t *testing.T) {
	for _, q := range []string{
		"CREATE SEQUENCE s", "CREATE TEMP SEQUENCE s", "CREATE TEMPORARY SEQUENCE s", "CREATE UNLOGGED SEQUENCE IF NOT EXISTS s AS SMALLINT INCREMENT BY -2 NO MINVALUE MAXVALUE -1 START WITH -1 CACHE 7 NO CYCLE OWNED BY public.t.c",
		"ALTER SEQUENCE IF EXISTS s AS INTEGER INCREMENT 2 MINVALUE 2 NO MAXVALUE START 2 RESTART WITH 4 CACHE 10 CYCLE OWNED BY NONE", "ALTER SEQUENCE s RESTART", "ALTER SEQUENCE s SET LOGGED", "ALTER SEQUENCE s SET UNLOGGED", "ALTER SEQUENCE s OWNER TO postgres", "ALTER SEQUENCE s SET SCHEMA moved", "ALTER SEQUENCE s RENAME TO renamed", "DROP SEQUENCE IF EXISTS s, public.t CASCADE", "DROP SEQUENCE s RESTRICT", "COMMENT ON SEQUENCE s IS 'text'", "SELECT nextval('s'),currval($1),lastval(),setval('s',42,false)", "SELECT nextval('s'::regclass)", "SELECT nextval(CAST('s' AS regclass))",
		`SELECT setval('s',nextval('t'),"flag") FROM runtime_null`,
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			if _, err = Parse(q + " trailing junk"); err == nil {
				t.Fatal("trailing syntax accepted", q)
			}
		})
	}
	for _, q := range []string{"CREATE SEQUENCE", "CREATE SEQUENCE s CACHE", "ALTER SEQUENCE s", "ALTER SEQUENCE s RESTART WITH", "DROP SEQUENCE s,", "SELECT nextval('s') garbage garbage", "SELECT nextval(,'s')", "SELECT setval('s',)"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("incomplete syntax accepted", q)
		}
	}
}

func TestSequenceCastRoutingAndBoundaries(t *testing.T) {
	for _, q := range []string{
		"SELECT nextval('s'::regclass)",
		"SELECT nextval($1::regclass)",
		"SELECT nextval(CAST('s' AS regclass))",
		"SELECT nextval(CAST($1 AS regclass))",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		call := statements[0].(*Select).Targets[0].(CallExpr)
		if _, ok := call.Args[0].(RegclassExpr); !ok {
			t.Fatalf("%s: cast AST missing: %#v", q, call)
		}
	}
	for _, q := range []string{"SELECT nextval('s':regclass)", "SELECT nextval('s': :regclass)", "SELECT nextval('s'::)"} {
		statements, err := Parse(q)
		var got *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &got) || got.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
}

func TestSequenceStandaloneRegclassProjection(t *testing.T) {
	for _, query := range []string{"SELECT 's'::regclass", "SELECT CAST('s' AS regclass)", "SELECT $1::regclass"} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(RegclassExpr); !ok {
			t.Fatal("standalone regclass AST missing", query)
		}
	}
	for _, query := range []string{"SELECT 1", "SELECT 's'", "SELECT $1", "SELECT TIMESTAMP 'epoch'"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("arbitrary relationless scalar selector admitted", query)
		}
	}
}
