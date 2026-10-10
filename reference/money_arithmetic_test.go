package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceMoneyArithmeticAndResolution(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		for _, typ := range []string{"smallint", "integer", "bigint", "real", "double precision"} {
			t.Run(typ, func(t *testing.T) {
				division := "$79.82"
				if typ == "real" || typ == "double precision" {
					division = "$79.83"
				}
				for _, prepared := range []bool{false, true} {
					for _, tc := range []struct {
						expr  string
						value any
						typ   string
					}{
						{"'878.08'::money/11::" + typ, division, "MONEY"}, {"'-878.08'::money/11::" + typ, "-" + division, "MONEY"},
						{"'1.23'::money*2::" + typ, "$2.46", "MONEY"}, {"2::" + typ + "*'1.23'::money", "$2.46", "MONEY"},
						{"'1.23'::money*0::" + typ, "$0.00", "MONEY"}, {"'1.23'::money*(-2)::" + typ, "-$2.46", "MONEY"},
						{"NULL::money/11::" + typ, nil, "MONEY"}, {"NULL::money*2::" + typ, nil, "MONEY"},
					} {
						s := moneyReferenceValue(tc.expr, tc.typ, tc.value)
						s.prepared = prepared
						moneyReferenceRun(t, p, b, s)
					}
					q := "SELECT '1'::money/0::" + typ
					s := moneyReferenceFailure(q, "22012", "division by zero", "", "", "execute")
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		for _, tc := range []struct {
			expression, typ string
			value           any
		}{
			{"'90000000000000099.00'::money/10::bigint", "MONEY", "$9,000,000,000,000,009.90"},
			{"'123'::money/'2'::money", "FLOAT8", float64(61.5)}, {"'123'::money/'2'", "FLOAT8", float64(61.5)}, {"'123'::money/2", "MONEY", "$61.50"},
			{"'878.08'::money/11::numeric", "MONEY", "$79.83"}, {"11::numeric*'1.01'::money", "MONEY", "$11.11"},
			{"'0.01'::money/2::float8", "MONEY", "$0.00"}, {"'0.05'::money/2::float8", "MONEY", "$0.02"},
			{"'-0.01'::money/2::float8", "MONEY", "$0.00"}, {"'-0.05'::money/2::float8", "MONEY", "-$0.02"},
			{"'1.23'::money+'2.34'::money", "MONEY", "$3.57"}, {"'1.23'::money-'2.34'::money", "MONEY", "-$1.11"},
			{"'1.23'::money/'Infinity'::float8", "MONEY", "$0.00"}, {"NULL::money/NULL::money", "FLOAT8", nil},
			{"NULL::money+NULL::money", "MONEY", nil}, {"'-92233720368547758.08'::money/2::bigint", "MONEY", "-$46,116,860,184,273,879.04"},
			{"'90071992547409.93'::money/'0.01'::money", "FLOAT8", float64(9007199254740992)},
			{"'0.01'::money*16777217::real", "MONEY", "$167,772.16"}, {"'0.01'::money*16777217::float8", "MONEY", "$167,772.17"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := moneyReferenceValue(tc.expression, tc.typ, tc.value)
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		for _, tc := range []struct{ expression, code, message string }{
			{"'92233720368547758.07'::money+'0.01'::money", "22003", "money out of range"},
			{"'-92233720368547758.08'::money-'0.01'::money", "22003", "money out of range"},
			{"'92233720368547758.07'::money*2::bigint", "22003", "money out of range"},
			{"'1'::money/'0'::money", "22012", "division by zero"}, {"'1'::money/'-0'::float8", "22012", "division by zero"},
			{"'1'::money*'NaN'::float8", "22003", "money out of range"}, {"'1'::money/'NaN'::float8", "22003", "money out of range"},
			{"'1'::money*'Infinity'::float8", "22003", "money out of range"},
			{"'1'::money*'1e308'::float8", "22003", "value out of range: overflow"},
			{"'0.01'::money/'1e-308'::float8", "22003", "money out of range"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := moneyReferenceFailure("SELECT "+tc.expression, tc.code, tc.message, "", "", "execute")
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		for _, tc := range []struct{ expression, signature, token string }{
			{"+'1'::money", "+ money", "+"}, {"-'1'::money", "- money", "-"}, {"'1'::money*'2'::money", "money * money", "*"},
			{"2/'1'::money", "integer / money", "/"}, {"'1'::money%'2'::money", "money % money", "%"},
			{"'1'::money^'2'::money", "money ^ money", "^"}, {"'1'::money+2", "money + integer", "+"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				q := "SELECT " + tc.expression
				for _, prepared := range []bool{false, true} {
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					s := moneyReferenceFailure(q, "42883", "operator does not exist: "+tc.signature, moneyReferenceLiteralPosition(q, tc.token), "", stage)
					s.diagnostic.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					if tc.signature == "+ money" || tc.signature == "- money" {
						s.diagnostic.Hint = "No operator matches the given name and argument type. You might need to add an explicit type cast."
					}
					s.prepared = prepared
					moneyReferenceRun(t, p, b, s)
				}
			})
		}
		moneyReferenceRun(t, p, b, referenceExec("CREATE TABLE money_order(v MONEY)", 0), referenceExec("INSERT INTO money_order VALUES('2'),('-1'),('2'),('0'),(NULL)", 5), referenceExec("CREATE INDEX money_order_idx ON money_order(v)", 0), referenceSelect("SELECT v FROM money_order ORDER BY v", []string{"v"}, []string{"MONEY"}, [][]any{{"-$1.00"}, {"$0.00"}, {"$2.00"}, {"$2.00"}, {nil}}), referenceSelect("SELECT v,count(*) AS n FROM money_order GROUP BY v ORDER BY v", []string{"v", "n"}, []string{"MONEY", "INT8"}, [][]any{{"-$1.00", int64(1)}, {"$0.00", int64(1)}, {"$2.00", int64(2)}, {nil, int64(1)}}))
		for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
			want := op == "<>" || op == "<" || op == "<="
			moneyReferenceRun(t, p, b, moneyReferenceValue(fmt.Sprintf("'1'::money %s '2'::money", op), "BOOL", want))
		}
	})
}

func TestReferenceMoneyUniqueIndexes(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("standalone=%v/prepared=%v", standalone, prepared), func(t *testing.T) {
				moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					run := func(s referenceStep) {
						s.prepared = prepared
						moneyReferenceRun(t, p, b, s)
					}
					if standalone {
						run(referenceExec("CREATE TABLE money_unique(id INT,v MONEY)", 0))
						run(referenceExec("CREATE UNIQUE INDEX money_unique_u ON money_unique(v)", 0))
					} else {
						run(referenceExec("CREATE TABLE money_unique(id INT,v MONEY CONSTRAINT money_unique_u UNIQUE)", 0))
					}
					run(referenceExec("INSERT INTO money_unique VALUES(1,'$1,234.50'),(2,'90071992547409.93'),(3,'90071992547409.94'),(4,NULL),(5,NULL)", 5))
					unchanged := referenceSelect("SELECT id,v FROM money_unique ORDER BY id", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{
						{int64(1), "$1,234.50"},
						{int64(2), "$90,071,992,547,409.93"},
						{int64(3), "$90,071,992,547,409.94"},
						{int64(4), nil},
						{int64(5), nil},
					})
					run(unchanged)
					for _, input := range []string{"1234.5", "$1,234.500"} {
						failure := referenceErrorDiagnostic("INSERT INTO money_unique VALUES($1,$2)", uniqueReferenceDuplicate("money_unique", "money_unique_u", "Key (v)=($1,234.50) already exists."), int64(6), input)
						failure.errorStage = "execute"
						run(failure)
						run(unchanged)
					}
					failure := referenceErrorDiagnostic("INSERT INTO money_unique VALUES(6,'2'),(7,'90,071,992,547,409.930')", uniqueReferenceDuplicate("money_unique", "money_unique_u", "Key (v)=($90,071,992,547,409.93) already exists."))
					failure.errorStage = "execute"
					run(failure)
					run(unchanged)
					failure = referenceErrorDiagnostic("UPDATE money_unique SET v='$1,234.500' WHERE id=3", uniqueReferenceDuplicate("money_unique", "money_unique_u", "Key (v)=($1,234.50) already exists."))
					failure.errorStage = "execute"
					run(failure)
					run(unchanged)
					run(referenceExec("INSERT INTO money_unique VALUES(6,'2')", 1))
					run(referenceSelect("SELECT id,v FROM money_unique WHERE v='2'::money", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{{int64(6), "$2.00"}}))
				})
			})
		}
	}
}
