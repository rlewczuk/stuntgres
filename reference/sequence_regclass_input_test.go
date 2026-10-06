//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

// Oracle-only witnesses stay runnable while native parameter input is red.
func TestReferenceSequenceRegclassParameterInput(t *testing.T) {
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
					p := newReferencePair(t)
					c := sequenceProbeConn(t, p)
					sequenceProbeExec(t, p.ctx, c, "CREATE TABLE empty_table(v INT)")
					sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE t START 17")
					query := func(args ...any) (*sql.Rows, error) { return c.QueryContext(p.ctx, tc.query, args...) }
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, tc.query)
						if err != nil { t.Fatal("Prepare must not import parameters", err) }
						defer stmt.Close()
						query = func(args ...any) (*sql.Rows, error) { return stmt.QueryContext(p.ctx, args...) }
					}
					rows, err := query(tc.args...)
					if rows != nil { rows.Close() }
					want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
					want.Context = "unnamed portal parameter $1 = '...'"
					referenceAssertDiagnostic(t, err, &want)
					want = controlsReferenceDiagnostic("55000", `currval of sequence "t" is not yet defined in this session`)
					var n int64
					referenceAssertDiagnostic(t, c.QueryRowContext(p.ctx, "SELECT currval('t')").Scan(&n), &want)
					sequenceProbeValue(t, p.ctx, c, "SELECT nextval('t')", 17)
				})
			}
		})
	}
}

func TestReferenceSequenceRegclassParameterPresentationAndDemand(t *testing.T) {
	p := newReferencePair(t)
	c := sequenceProbeConn(t, p)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s", "CREATE SEQUENCE b.s", `CREATE SEQUENCE a."A"`, "CREATE TABLE empty_table(v INT)", "SET search_path TO a,b,public"} { sequenceProbeExec(t, p.ctx, c, q) }
	for _, prepared := range []bool{false, true} {
		query := func(arg any) (*sql.Rows, error) { return c.QueryContext(p.ctx, "SELECT $1::regclass", arg) }
		if prepared {
			stmt, err := c.PrepareContext(p.ctx, "SELECT $1::regclass")
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			query = func(arg any) (*sql.Rows, error) { return stmt.QueryContext(p.ctx, arg) }
		}
		for _, tc := range []struct { input any; want any }{{"a.s", "s"}, {"b.s", "b.s"}, {`a."A"`, `"A"`}, {"4294967295", "4294967295"}, {"0", "-"}, {"-", "-"}, {nil, nil}} {
			rows, err := query(tc.input)
			if err != nil { t.Fatal(err) }
			names, err := rows.Columns()
			if err != nil || len(names) != 1 || names[0] != "regclass" { rows.Close(); t.Fatal(names, err) }
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "REGCLASS" { rows.Close(); t.Fatal(types, err) }
			if !rows.Next() { rows.Close(); t.Fatal("missing projection row", rows.Err()) }
			var got any
			if err := rows.Scan(&got); err != nil || referenceNormalize(got) != tc.want { rows.Close(); t.Fatal(tc.input, got, tc.want, err) }
			if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("extra row or cursor error", rows.Err()) }
			rows.Close()
		}
	}
	for _, arg := range []any{"a.s", "4294967295", "-", nil} {
		rows, err := c.QueryContext(p.ctx, "SELECT nextval($1) FROM empty_table", arg)
		if err != nil { t.Fatal(arg, err) }
		if rows.Next() || rows.Err() != nil { rows.Close(); t.Fatal("expected empty result", rows.Err()) }
		rows.Close()
		var n any
		if err := c.QueryRowContext(p.ctx, "SELECT setval($1,$2)", arg, nil).Scan(&n); err != nil || n != nil { t.Fatal(arg, n, err) }
	}
	for _, tc := range []struct { input, oid string }{{"4294967295", "4294967295"}, {"0", "0"}, {"-", "0"}} {
		var n int64
		want := controlsReferenceDiagnostic("XX000", "could not open relation with OID " + tc.oid)
		referenceAssertDiagnostic(t, c.QueryRowContext(p.ctx, "SELECT nextval($1)", tc.input).Scan(&n), &want)
	}
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('a.s')", 1)
}

func TestReferenceSequenceRegclassParameterNamespaceInput(t *testing.T) {
	p := newReferencePair(t)
	c := sequenceProbeConn(t, p)
	defer func() {
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA private CASCADE", "DROP ROLE seq_input_reader"} { sequenceProbeExec(t, p.ctx, c, q) }
	}()
	for _, q := range []string{"CREATE ROLE seq_input_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s", "REVOKE ALL ON SCHEMA private FROM PUBLIC", "SET SESSION AUTHORIZATION seq_input_reader"} { sequenceProbeExec(t, p.ctx, c, q) }
	for _, query := range []string{"SELECT $1::regclass", "SELECT setval($1,$2)"} {
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil { t.Fatal("parameter Prepare needs no source namespace access", err) }
		args := []any{"private.s"}
		if query == "SELECT setval($1,$2)" { args = append(args, nil) }
		rows, err := stmt.QueryContext(p.ctx, args...)
		if rows != nil { rows.Close() }
		stmt.Close()
		want := controlsReferenceDiagnostic("42501", "permission denied for schema private")
		want.Context = "unnamed portal parameter $1 = '...'"
		referenceAssertDiagnostic(t, err, &want)
	}
	sequenceProbeExec(t, p.ctx, c, "RESET SESSION AUTHORIZATION")
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('private.s')", 1)
}
