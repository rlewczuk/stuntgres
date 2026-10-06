package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

// Parameter input precedes executor demand, strictness and volatile effects.
func TestSequenceRegclassParameterInput(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "Direct"
		if prepared { mode = "Prepared" }
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct { name, query string; args []any }{
				{"Projection", "SELECT $1::regclass", []any{"missing"}},
				{"ZeroRows", "SELECT nextval($1) FROM empty_table", []any{"missing"}},
				{"RuntimeNull", "SELECT setval($1,$2)", []any{"missing", nil}},
				{"NestedEffect", "SELECT setval($1,nextval('t'))", []any{"missing"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					ctx := context.Background()
					c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
					sequenceExec(t, c, "CREATE TABLE empty_table(v INT)")
					sequenceExec(t, c, "CREATE SEQUENCE t START 17")
					query := func(args ...any) (*sql.Rows, error) { return c.QueryContext(ctx, tc.query, args...) }
					if prepared {
						stmt, err := c.PrepareContext(ctx, tc.query)
						if err != nil { t.Fatal("Prepare must not import parameters", err) }
						defer stmt.Close()
						query = func(args ...any) (*sql.Rows, error) { return stmt.QueryContext(ctx, args...) }
					}
					rows, err := query(tc.args...)
					if rows != nil { rows.Close() }
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "missing" does not exist`, Context: "unnamed portal parameter $1 = '...'"})
					var n int64
					typeCoreDiagnostic(t, c.QueryRowContext(ctx, "SELECT currval('t')").Scan(&n), stuntgres.Error{Code: "55000", Message: `currval of sequence "t" is not yet defined in this session`})
					sequenceValue(t, c, "SELECT nextval('t')", 17)
				})
			}
		})
	}
}

func TestSequenceRegclassParameterPresentationAndDemand(t *testing.T) {
	ctx := context.Background()
	c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."A"`, "CREATE TABLE empty_table(v INT)", "SET search_path TO a,b,public"} { sequenceExec(t, c, q) }
	for _, prepared := range []bool{false, true} {
		query := func(arg any) (*sql.Rows, error) { return c.QueryContext(ctx, "SELECT $1::regclass", arg) }
		if prepared {
			stmt, err := c.PrepareContext(ctx, "SELECT $1::regclass")
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			query = func(arg any) (*sql.Rows, error) { return stmt.QueryContext(ctx, arg) }
		}
		for _, tc := range []struct { input any; want any }{{"a.s", "s"}, {"b.s", "b.s"}, {`a."A"`, `"A"`}, {"4294967295", "4294967295"}, {"0", "-"}, {"-", "-"}, {nil, nil}} {
			rows, err := query(tc.input)
			queryCoreRows(t, rows, err, []string{"regclass"}, []string{"REGCLASS"}, [][]any{{tc.want}}, false)
		}
	}
	for _, arg := range []any{"a.s", "4294967295", "-", nil} {
		rows, err := c.QueryContext(ctx, "SELECT nextval($1) FROM empty_table", arg)
		queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{}, false)
		rows, err = c.QueryContext(ctx, "SELECT setval($1,$2)", arg, nil)
		queryCoreRows(t, rows, err, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, false)
	}
	for _, tc := range []struct { input, oid string }{{"4294967295", "4294967295"}, {"0", "0"}, {"-", "0"}} {
		var n int64
		typeCoreDiagnostic(t, c.QueryRowContext(ctx, "SELECT nextval($1)", tc.input).Scan(&n), stuntgres.Error{Code: "XX000", Message: "could not open relation with OID " + tc.oid})
	}
	sequenceValue(t, c, "SELECT nextval('a.s')", 1)
}
