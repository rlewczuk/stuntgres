package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestSequenceDiagnosticsFullRecordsAndAtomicOptions(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), "sequence-diagnostics")
	a := sequenceConn(t, db)
	sequenceExec(t, a, "CREATE TABLE t(v INT)")
	sequenceExec(t, a, "CREATE SEQUENCE s")
	cases := []struct {
		q    string
		want stuntgres.Error
	}{
		{"SELECT currval('s')", stuntgres.Error{Code: "55000", Message: `currval of sequence "s" is not yet defined in this session`}},
		{"SELECT lastval()", stuntgres.Error{Code: "55000", Message: "lastval is not yet defined in this session"}},
		{"CREATE SEQUENCE bad INCREMENT 0", stuntgres.Error{Code: "22023", Message: "INCREMENT must not be zero"}},
		{"CREATE SEQUENCE bad CACHE 0", stuntgres.Error{Code: "22023", Message: "CACHE (0) must be greater than zero"}},
		{"CREATE SEQUENCE bad MINVALUE 5 MAXVALUE 5", stuntgres.Error{Code: "22023", Message: "MINVALUE (5) must be less than MAXVALUE (5)"}},
		{"CREATE SEQUENCE bad AS TEXT", stuntgres.Error{Code: "22023", Message: "sequence type must be smallint, integer, or bigint"}},
		{"CREATE SEQUENCE bad AS SMALLINT MAXVALUE 32768", stuntgres.Error{Code: "22023", Message: "MAXVALUE (32768) is out of range for sequence data type smallint"}},
		{"CREATE SEQUENCE bad START 0", stuntgres.Error{Code: "22023", Message: "START value (0) cannot be less than MINVALUE (1)"}},
		{"ALTER SEQUENCE s RESTART 0", stuntgres.Error{Code: "22023", Message: "RESTART value (0) cannot be less than MINVALUE (1)"}},
		{"SELECT setval('s',0)", stuntgres.Error{Code: "22003", Message: `setval: value 0 is out of bounds for sequence "s" (1..9223372036854775807)`}},
		{"SELECT nextval('t')", stuntgres.Error{Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."}},
		{"ALTER SEQUENCE t RESTART", stuntgres.Error{Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."}},
		{"SELECT nextval('0')", stuntgres.Error{Code: "XX000", Message: "could not open relation with OID 0"}},
		{"SELECT nextval('-')", stuntgres.Error{Code: "XX000", Message: "could not open relation with OID 0"}},
		{"SELECT nextval('4294967295')", stuntgres.Error{Code: "XX000", Message: "could not open relation with OID 4294967295"}},
		{"SELECT nextval('a.b.c')", stuntgres.Error{Code: "0A000", Message: `cross-database references are not implemented: "a.b.c"`, Position: "16"}},
		{"SELECT nextval('bad..name')", stuntgres.Error{Code: "42602", Message: "invalid name syntax", Position: "16"}},
		{"SELECT /*é*/ nextval('missing')", stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "22"}},
		{"CREATE SEQUENCE s", stuntgres.Error{Code: "42P07", Message: `relation "s" already exists`}},
		{"CREATE SEQUENCE bad CACHE 1 CACHE 2", stuntgres.Error{Code: "42601", Message: "conflicting or redundant options", Position: "29"}},
		{"CREATE SEQUENCE bad OWNED BY t", stuntgres.Error{Code: "42601", Message: "invalid OWNED BY option", Hint: "Specify OWNED BY table.column or OWNED BY NONE."}},
		{"CREATE SEQUENCE bad OWNED BY t.missing", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
		{"DROP SEQUENCE missing", stuntgres.Error{Code: "42P01", Message: `sequence "missing" does not exist`}},
		{"DROP SEQUENCE t", stuntgres.Error{Code: "42809", Message: `"t" is not a sequence`, Hint: "Use DROP TABLE to remove a table."}},
	}
	for _, c := range cases {
		t.Run(c.q, func(t *testing.T) { _, err := a.ExecContext(ctx, c.q); typeCoreDiagnostic(t, err, c.want) })
	}
	sequenceValue(t, a, "SELECT nextval('s')", 1)
	for _, q := range []string{"ALTER SEQUENCE s INCREMENT 0", "ALTER SEQUENCE s CACHE 0", "ALTER SEQUENCE s AS SMALLINT MINVALUE -32769", "ALTER SEQUENCE s MAXVALUE 1"} {
		if _, err := a.ExecContext(ctx, q); err == nil {
			t.Fatal("invalid ALTER accepted", q)
		}
	}
	sequenceValue(t, a, "SELECT nextval('s')", 2)
	stmt, err := a.PrepareContext(ctx, "SELECT nextval($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var n int64
	err = stmt.QueryRowContext(ctx, "missing").Scan(&n)
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"})
	for _, q := range []string{"SELECT nextval('missing')", "SELECT /*é*/ nextval('missing')"} {
		stmt, err := a.PrepareContext(ctx, q)
		if stmt != nil {
			stmt.Close()
		}
		want := stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Position: "16"}
		if q == "SELECT /*é*/ nextval('missing')" {
			want.Position = "22"
		}
		typeCoreDiagnostic(t, err, want)
	}
}
