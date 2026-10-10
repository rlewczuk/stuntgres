package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func moneyReferenceScanType(t *testing.T, c *sql.ColumnType, postgres bool) {
	t.Helper()
	want := reflect.TypeFor[string]()
	if postgres {
		want = reflect.TypeFor[any]()
	}
	if c.ScanType() != want {
		t.Fatalf("MONEY ScanType got %v want %v", c.ScanType(), want)
	}
}

func TestReferenceMoneyParametersAndMetadata(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		moneyReferenceRun(t, p, b, referenceExec("CREATE TABLE money_params(id INT,v MONEY,required MONEY NOT NULL DEFAULT '1')", 0))
		q := "INSERT INTO money_params(id,v) VALUES($1,$2)"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
		owned := []byte("$123.455")
		for i, arg := range []any{"1.235", owned, int64(123), float64(123.455), nil, []byte(nil)} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(i+1), arg))
		}
		owned[0] = 'X'
		failure := moneyReferenceFailure(q, "22P02", "invalid input syntax for type money: \"oops\"", "", "unnamed portal parameter $2 = '...'", "execute")
		failure.args = []any{int64(7), "oops"}
		referenceRunStep(t, p.ctx, held, b.recorder, failure)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(7), "2"))
		moneyReferenceRun(t, p, b, referenceSelect("SELECT id,v FROM money_params ORDER BY id", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{{int64(1), "$1.24"}, {int64(2), "$123.46"}, {int64(3), "$123.00"}, {int64(4), "$123.46"}, {int64(5), nil}, {int64(6), nil}, {int64(7), "$2.00"}}), referencePrepared(referenceSelect("SELECT $1::numeric::money AS v", []string{"v"}, []string{"MONEY"}, [][]any{{"$1.24"}}, "1.235")))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query    string
				rows     [][]any
				nullable []bool
				declared bool
			}{
				{"SELECT v,required FROM money_params WHERE id=0", nil, []bool{true, false}, true},
				{"SELECT v FROM money_params WHERE id=5", [][]any{{nil}}, []bool{true}, true},
				{"SELECT v FROM money_params WHERE id=2", [][]any{{"$123.46"}}, []bool{true}, true},
				{"SELECT NULL::money AS v", [][]any{{nil}}, []bool{true}, false},
			} {
				columns, types := []string{"v"}, []string{"MONEY"}
				if len(tc.nullable) == 2 {
					columns = append(columns, "required")
					types = append(types, "MONEY")
				}
				s := referenceSelect(tc.query, columns, types, tc.rows)
				s.prepared = prepared
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { moneyReferenceMetadata(t, rows, b.pg, tc.declared, tc.nullable) }
				if len(tc.rows) != 0 && tc.rows[0][0] != nil {
					s.checkScan = func(t *testing.T, rows *sql.Rows, index int) {
						var text string
						if err := rows.Scan(&text); err != nil || text != "$123.46" {
							t.Fatal(text, err)
						}
						var bytes []byte
						if err := rows.Scan(&bytes); err != nil || string(bytes) != "$123.46" {
							t.Fatal(bytes, err)
						}
					}
				}
				moneyReferenceRun(t, p, b, s)
			}
			moneyReferenceRun(t, p, b, referenceSelect("SELECT v/v AS ratio FROM money_params WHERE id=0", []string{"ratio"}, []string{"FLOAT8"}, nil), referenceSelect("SELECT v/v AS ratio FROM money_params WHERE id=5", []string{"ratio"}, []string{"FLOAT8"}, [][]any{{nil}}))
		}
		query := "SELECT $1::money AS v"
		selectStmt, err := b.db.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		selectHeld := referenceHeldStatement{referenceExecutor: b.db, statement: selectStmt, query: query}
		for _, locale := range []string{"C", "POSIX", ""} {
			moneyReferenceRun(t, p, b, referenceExec("SET lc_monetary TO '"+locale+"'", 0))
			referenceRunStep(t, p.ctx, selectHeld, b.recorder, referenceSelect(query, []string{"v"}, []string{"MONEY"}, [][]any{{"$1,234.50"}}, []byte("1234.5")))
		}
	})
}

func TestReferenceMoneyDiagnosticsAndAtomicity(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		for _, q := range []string{"SELECT 'oops'::money", "SELECT ''::text AS prefix, 'oops'::money", "SELECT 'é🙂'::text AS prefix, 'oops'::money"} {
			t.Run(q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					s := moneyReferenceFailure(q, "22P02", "invalid input syntax for type money: \"oops\"", moneyReferenceLiteralPosition(q, "'oops'"), "", stage)
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		moneyReferenceRun(t, p, b, referenceExec("CREATE TABLE money_atomic(id INT,v MONEY)", 0), referenceExec("INSERT INTO money_atomic VALUES(1,'1'),(2,'92233720368547758.07')", 2))
		unchanged := referenceSelect("SELECT id,v FROM money_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{{int64(1), "$1.00"}, {int64(2), "$92,233,720,368,547,758.07"}})
		for _, tc := range []struct{ query, code, message, position string }{
			{"INSERT INTO money_atomic VALUES(3,'2'),(4,'oops')", "22P02", "invalid input syntax for type money: \"oops\"", "41"},
			{"INSERT INTO money_atomic VALUES(3,2::numeric::money),(4,92233720368547759::bigint::money)", "22003", "bigint out of range", ""},
			{"UPDATE money_atomic SET v=v+'0.01'::money", "22003", "money out of range", ""},
		} {
			t.Run(tc.query, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared && tc.code == "22P02" {
						stage = "prepare"
					}
					position := tc.position
					if tc.code == "22P02" {
						position = moneyReferenceLiteralPosition(tc.query, "'oops'")
					}
					s := moneyReferenceFailure(tc.query, tc.code, tc.message, position, "", stage)
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s, unchanged)
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO money_atomic VALUES(4,$1),(5,$2)"
			s := moneyReferenceFailure(q, "22P02", "invalid input syntax for type money: \"oops\"", "", "unnamed portal parameter $2 = '...'", "execute")
			s.prepared = prepared
			s.args = []any{"4", "oops"}
			moneyReferenceRun(t, p, b, s, unchanged)
		}
		moneyReferenceRun(t, p, b, referenceExec("INSERT INTO money_atomic VALUES($1,$2)", 1, int64(3), "3"))
		for _, tc := range []struct{ query, code, message, position string }{
			{"CREATE TABLE money_bad(v money(2))", "42601", "type modifier is not allowed for type \"money\"", "26"},
			{"CREATE TABLE money_bad(v pg_catalog.monies)", "42704", "type \"pg_catalog.monies\" does not exist", "26"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				moneyReferenceRun(t, p, b, moneyReferenceFailure(tc.query, tc.code, tc.message, tc.position, "", "execute"))
			})
		}
	})
}
