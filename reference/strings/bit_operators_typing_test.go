package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitOperatorsTyping(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE bop_types(id INTEGER,f "bit",v VARBIT,t TEXT,b BYTEA,i INTEGER,s SMALLINT,l BIGINT,n NUMERIC,z BOOLEAN,w VARBIT)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bop_types VALUES(1,B'10001',B'01101','101',NULL,3,3,3,3,TRUE,B'10001'),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)", 2))
		for _, field := range []string{"f", "w"} {
			for _, tc := range []struct{ op, typ, right, left string }{
				{"&", "BIT", "00001", "00001"},
				{"|", "BIT", "11101", "11101"}, {"#", "BIT", "11100", "11100"},
				{"=", "BOOL", "", ""}, {"<>", "BOOL", "", ""}, {"<", "BOOL", "", ""},
				{"<=", "BOOL", "", ""}, {">", "BOOL", "", ""}, {">=", "BOOL", "", ""},
			} {
				// Use the same f value for each family so unknown resolution is isolated.
				known := field
				for _, side := range []bool{false, true} {
					for _, operand := range []string{"'01101'", "$1", "NULL"} {
						lhs, rhs := known, operand
						var want any = tc.right
						if side {
							lhs, rhs = rhs, lhs
							want = tc.left
						}
						if tc.typ == "BOOL" {
							want = map[string]bool{"=": false, "<>": true, "<": side, "<=": side, ">": !side, ">=": !side}[tc.op]
						}
						if operand == "NULL" {
							want = nil
						}
						args := []any(nil)
						if operand == "$1" {
							args = []any{"01101"}
						}
						q := "SELECT " + lhs + tc.op + rhs + " AS r FROM bop_types WHERE id=1"
						t.Run(field+"/"+tc.op+"/"+operand+fmt.Sprint(side), func(t *testing.T) { bitOperatorsBoth(t, p, b, c, bitOperatorsOne(q, tc.typ, want, args...)) })
					}
				}
			}
			for _, tc := range []struct {
				expr string
				want any
			}{
				{"~" + field, nil}, {field + "<<3", nil}, {field + ">>3", nil}, {field + "<<NULL", nil}, {field + ">>NULL", nil},
				{field + "||B'1'", nil}, {"B'1'||" + field, nil}, {field + "&B'1'", nil}, {"B'1'&" + field, nil},
				{field + "|B'1'", nil}, {"B'1'|" + field, nil}, {field + "#B'1'", nil}, {"B'1'#" + field, nil},
			} {
				typ := "BIT"
				if tc.expr == field+"||B'1'" || tc.expr == "B'1'||"+field {
					typ = "VARBIT"
				}
				bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT "+tc.expr+" AS r FROM bop_types WHERE id=2", typ, tc.want))
			}
		}
		for _, tc := range []struct {
			expr, typ string
			want      any
			args      []any
		}{
			{"'10'||'01'", "TEXT", "1001", nil}, {"t||f", "TEXT", "10110001", nil}, {"f||t", "TEXT", "10001101", nil},
			{"'101'<<1", "INT4", int64(202), nil}, {"NULL<<1", "INT4", nil, nil},
			{"f<<'3'", "BIT", "01000", nil}, {"f>>$1", "BIT", "00100", []any{2}},
			{"f<<s", "BIT", "01000", nil}, {"v<<i", "BIT", "01000", nil},
			{"f OPERATOR(pg_catalog.&) v", "BIT", "00001", nil},
			{"f OPERATOR(pg_catalog.||) v", "VARBIT", "1000101101", nil},
			{"OPERATOR(pg_catalog.~) f", "BIT", "01110", nil},
			{"f OPERATOR(pg_catalog.<<) 3", "BIT", "01000", nil},
			{"f OPERATOR(pg_catalog.>) v", "BOOL", true, nil},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT "+tc.expr+" AS r FROM bop_types WHERE id=1", tc.typ, tc.want, tc.args...))
			})
		}
		for _, tc := range []struct {
			q, typ string
			want   any
			args   []any
		}{
			{"SELECT B'10001'||'011' AS r", "VARBIT", "10001011", nil},
			{"SELECT '011'||B'10001' AS r", "VARBIT", "01110001", nil},
			{"SELECT v||'10001' AS r FROM bop_types WHERE id=1", "VARBIT", "0110110001", nil},
			{"SELECT '10001'||v AS r FROM bop_types WHERE id=1", "VARBIT", "1000101101", nil},
			{"SELECT v||$1 AS r FROM bop_types WHERE id=1", "VARBIT", "0110110001", []any{"10001"}},
			{"SELECT $1||v AS r FROM bop_types WHERE id=1", "VARBIT", "1000101101", []any{"10001"}},
			{"SELECT f||$1 AS r FROM bop_types WHERE id=1", "VARBIT", "1000110001", []any{"10001"}},
			{"SELECT $1||f AS r FROM bop_types WHERE id=1", "VARBIT", "1000110001", []any{"10001"}},
			{"SELECT f||NULL AS r FROM bop_types WHERE id=1", "VARBIT", nil, nil},
			{"SELECT NULL||f AS r FROM bop_types WHERE id=1", "VARBIT", nil, nil},
			{"SELECT v||NULL AS r FROM bop_types WHERE id=1", "VARBIT", nil, nil},
			{"SELECT NULL||v AS r FROM bop_types WHERE id=1", "VARBIT", nil, nil},
			{"SELECT f&NULL AS r FROM bop_types WHERE id=2", "BIT", nil, nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := bitOperatorsOne(tc.q, tc.typ, tc.want, tc.args...)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					cols, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					bitOperatorsMetadata(t, cols, s.Columns, s.Types)
				}
				bitOperatorsBoth(t, p, b, c, s)
			})
		}
		for _, tc := range []struct {
			q, code, message, hint string
			position               int
		}{
			{"SELECT ~'101' AS r", "42725", "operator is not unique: ~ unknown", bitOperatorsAmbiguousHint, 8},
			{"SELECT ~NULL AS r", "42725", "operator is not unique: ~ unknown", bitOperatorsAmbiguousHint, 8},
			{"SELECT '101'&'010' AS r", "42725", "operator is not unique: unknown & unknown", bitOperatorsAmbiguousHint, 13},
			{"SELECT NULL&NULL AS r", "42725", "operator is not unique: unknown & unknown", bitOperatorsAmbiguousHint, 12},
			{"SELECT '101'<<'1' AS r", "42725", "operator is not unique: unknown << unknown", bitOperatorsAmbiguousHint, 13},
			{"SELECT NULL<<NULL AS r", "42725", "operator is not unique: unknown << unknown", bitOperatorsAmbiguousHint, 12},
			{"SELECT ~TRUE AS r", "42883", "operator does not exist: ~ boolean", bitOperatorsUnaryHint, 8},
			{"SELECT f&b AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit & bytea", bitOperatorsBinaryHint, 9},
			{"SELECT f&t AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit & text", bitOperatorsBinaryHint, 9},
			{"SELECT f&i AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit & integer", bitOperatorsBinaryHint, 9},
			{"SELECT f&n AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit & numeric", bitOperatorsBinaryHint, 9},
			{"SELECT f&z AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit & boolean", bitOperatorsBinaryHint, 9},
			{"SELECT f<<l AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit << bigint", bitOperatorsBinaryHint, 9},
			{"SELECT f>>n AS r FROM bop_types WHERE FALSE", "42883", "operator does not exist: bit >> numeric", bitOperatorsBinaryHint, 9},
			{"SELECT f OPERATOR(public.&) v AS r FROM bop_types WHERE id=1", "42883", "operator does not exist: bit public.& bit varying", bitOperatorsBinaryHint, 10},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position, d.Hint = fixture.Position(tc.position), tc.hint
					fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(tc.q, d, prepared, true, true))
				})
			}
		}
		for _, q := range []string{"SELECT f=v AND v=$1 AS r FROM bop_types WHERE id=1", "SELECT v=f AND f=$1 AS r FROM bop_types WHERE id=1"} {
			t.Run(q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22P02", `"2" is not a valid binary digit`)
				d.Context = "unnamed portal parameter $1 = '...'"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(q, d, true, false, true, "2"))
			})
		}
	})
}
