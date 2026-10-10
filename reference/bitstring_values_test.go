package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func bitstringReferenceMetadata(pg bool, widths []int64, nullable []bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != len(widths) {
			t.Fatalf("metadata columns %d want %d", len(types), len(widths))
		}
		for i, typ := range types {
			if typ.DatabaseTypeName() != "BIT" && typ.DatabaseTypeName() != "VARBIT" {
				t.Fatal(typ.DatabaseTypeName())
			}
			if typ.ScanType() != reflect.TypeFor[string]() {
				t.Fatalf("%s ScanType %v", typ.Name(), typ.ScanType())
			}
			if n, ok := typ.Length(); !ok || n != widths[i] {
				t.Fatalf("%s Length %d/%t want %d/true", typ.Name(), n, ok, widths[i])
			}
			if p, s, ok := typ.DecimalSize(); ok {
				t.Fatalf("bit precision %d/%d/%t", p, s, ok)
			}
			n, known := typ.Nullable()
			if pg {
				if known || n {
					t.Fatalf("pq Nullable %t/%t", n, known)
				}
			} else if nullable != nil && (!known || n != nullable[i]) {
				t.Fatalf("native Nullable %t/%t want %t/true", n, known, nullable[i])
			}
		}
	}
}

func TestReferenceBitstringRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			t.Run(backend.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				run := func(s referenceStep) {
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
				run(referenceExec("CREATE TABLE bs_values(id INTEGER,f BIT(3),v BIT VARYING(5),u VARBIT,d BIT)", 0))
				run(referenceExec("INSERT INTO bs_values VALUES(1,B'101',B'',X'0f',B'0'),(2,'001','B10','xA','1'),(3,NULL,NULL,NULL,NULL),(4,B'100','',B'1000',B'1')", 4))
				run(referenceSelect("SELECT id,f,v,u,d FROM bs_values ORDER BY id", []string{"id", "f", "v", "u", "d"}, []string{"INT4", "BIT", "VARBIT", "VARBIT", "BIT"}, [][]any{{int64(1), "101", "", "00001111", "0"}, {int64(2), "001", "10", "1010", "1"}, {int64(3), nil, nil, nil, nil}, {int64(4), "100", "", "1000", "1"}}))
				for _, q := range []string{"SELECT f,v,u,d FROM bs_values ORDER BY id", "SELECT f,v,u,d FROM bs_values WHERE id=3", "SELECT f,v,u,d FROM bs_values WHERE id=99"} {
					want := [][]any{{"101", "", "00001111", "0"}, {"001", "10", "1010", "1"}, {nil, nil, nil, nil}, {"100", "", "1000", "1"}}
					if q == "SELECT f,v,u,d FROM bs_values WHERE id=3" {
						want = [][]any{{nil, nil, nil, nil}}
					}
					if q == "SELECT f,v,u,d FROM bs_values WHERE id=99" {
						want = nil
					}
					s := referenceSelect(q, []string{"f", "v", "u", "d"}, []string{"BIT", "VARBIT", "VARBIT", "BIT"}, want)
					s.checkMetadata = bitstringReferenceMetadata(backend.pg, []int64{3, 5, -1, 1}, []bool{true, true, true, true})
					run(s)
				}
				run(referenceExec("DROP TABLE bs_values", 0))
			})
		}
	}
}

func TestReferenceBitstringDeclarations(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			run := func(s referenceStep) {
				s.prepared = prepared
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec(`CREATE TABLE bs_generic(a "bit",b pg_catalog.bit,c "varbit",d pg_catalog."bit",e pg_catalog.bit(3),f pg_catalog.varbit(5))`, 0))
			run(referenceExec("INSERT INTO bs_generic VALUES(B'10',B'101',B'',B'0000',B'001',B'1')", 1))
			s := referenceSelect("SELECT a,b,c,d,e,f FROM bs_generic", []string{"a", "b", "c", "d", "e", "f"}, []string{"BIT", "BIT", "VARBIT", "BIT", "BIT", "VARBIT"}, [][]any{{"10", "101", "", "0000", "001", "1"}})
			s.checkMetadata = bitstringReferenceMetadata(backend.pg, []int64{-1, -1, -1, -1, 3, 5}, []bool{true, true, true, true, true, true})
			run(s)
			run(referenceExec("DROP TABLE bs_generic", 0))
			for _, decl := range []string{"BIT(1)", "BIT(83886080)", "VARBIT(83886080)", "BIT VARYING(83886080)", "BIT('3')", `"bit"(3)`, `pg_catalog."varbit"('5')`} {
				run(referenceExec("CREATE TABLE bs_modifier_ok(v "+decl+")", 0))
				run(referenceExec("INSERT INTO bs_modifier_ok VALUES(NULL)", 1))
				run(referenceExec("DROP TABLE bs_modifier_ok", 0))
			}
		}
		for _, family := range []string{"BIT", "VARBIT", "BIT VARYING", `"bit"`, "pg_catalog.varbit"} {
			name := "bit"
			if family == "VARBIT" || family == "BIT VARYING" || family == "pg_catalog.varbit" {
				name = "varbit"
			}
			for _, tc := range []struct{ mod, state, message string }{
				{"0", "22023", "length for type " + name + " must be at least 1"},
				{"-1", "22023", "length for type " + name + " must be at least 1"},
				{"83886081", "22023", "length for type " + name + " cannot exceed 83886080"},
				{"1,2", "22023", "invalid type modifier"},
				{"1.5", "22P02", `invalid input syntax for type integer: "1.5"`},
				{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
				{"foo", "22P02", `invalid input syntax for type integer: "foo"`},
				{"+1", "42601", "type modifiers must be simple constants or identifiers"},
				{"1+1", "42601", "type modifiers must be simple constants or identifiers"},
			} {
				q := "CREATE TABLE bs_modifier_bad(v " + family + "(" + tc.mod + "))"
				for _, prepared := range []bool{false, true} {
					t.Run(family+"/"+tc.mod+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						bitstringReferenceError(t, p, backend, backend.db, q, nil, prepared, false, tc.state, tc.message, "32")
					})
				}
			}
		}
	}
}

func TestReferenceBitstringLiterals(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q      string
				values []any
			}{
				{"SELECT B'',b'00001',X'0fA',x'0'", []any{"", "00001", "000011111010", "0000"}},
				{"SELECT B'10'\n'01',X'0'\n'f'", []any{"1001", "00001111"}},
				{"SELECT B'10'\n-- continuation\n'01',X'A'\n'0'", []any{"1001", "10100000"}},
			} {
				columns, types, widths := make([]string, len(tc.values)), make([]string, len(tc.values)), make([]int64, len(tc.values))
				for i := range columns {
					columns[i], types[i], widths[i] = "?column?", "BIT", -1
				}
				s := referenceSelect(tc.q, columns, types, [][]any{tc.values})
				s.prepared = prepared
				s.checkMetadata = bitstringReferenceMetadata(backend.pg, widths, nil)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		}
		conn, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer func() {
				if _, err := conn.ExecContext(p.ctx, "SET standard_conforming_strings=on"); err != nil {
					t.Error(err)
				}
			}()
			for _, setting := range []string{"on", "off"} {
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET standard_conforming_strings="+setting, 0))
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{`SELECT B'1\0'`, `SELECT X'0\f'`} {
						message := "\"\\\" is not a valid binary digit"
						if q == `SELECT X'0\f'` {
							message = "\"\\\" is not a valid hexadecimal digit"
						}
						bitstringReferenceError(t, p, backend, conn, q, nil, prepared, true, "22P02", message, "8")
					}
				}
			}
		}()
	}
}
