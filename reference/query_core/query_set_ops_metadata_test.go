package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQuerySetOpsTypmodMetadata(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sm(a NUMERIC(6,2),b NUMERIC(7,3),c VARCHAR(5),d VARCHAR(7),n INT NOT NULL)", 0),
			fixture.Exec("INSERT INTO sm VALUES(1.20,2.300,'a','b',1)", 1),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, tc := range []struct {
			left, right, typ          string
			rows                      [][]any
			precision, scale, length  int64
			decimalKnown, lengthKnown bool
		}{
			{"a", "a", "NUMERIC", [][]any{{"1.20"}, {"1.20"}}, 6, 2, 0, true, false},
			{"a", "b", "NUMERIC", [][]any{{"1.20"}, {"2.300"}}, 0, 0, 0, false, false},
			{"c", "c", "VARCHAR", [][]any{{"a"}, {"a"}}, 0, 0, 5, false, true},
			{"c", "d", "VARCHAR", [][]any{{"a"}, {"b"}}, 0, 0, 0, false, false},
			{"n", "n", "INT4", [][]any{{int64(1)}, {int64(1)}}, 0, 0, 0, false, false},
			{"n", "NULL", "INT4", [][]any{{int64(1)}, {nil}}, 0, 0, 0, false, false},
		} {
			for _, empty := range []string{"", " WHERE FALSE"} {
				q := "SELECT " + tc.left + " FROM sm" + empty + " UNION ALL SELECT " + tc.right + " FROM sm" + empty
				want := tc.rows
				if empty != "" {
					want = [][]any{}
				}
				s := querySetOpsBag(q, tc.left, tc.typ, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					columns, err := rows.ColumnTypes()
					if err != nil || len(columns) != 1 {
						t.Fatal(columns, err)
					}
					c := columns[0]
					wantScan := reflect.TypeFor[int64]()
					if tc.typ == "NUMERIC" || tc.typ == "VARCHAR" {
						wantScan = reflect.TypeFor[string]()
					}
					if b.PG && tc.typ == "NUMERIC" {
						wantScan = reflect.TypeFor[any]()
					}
					if b.PG && tc.typ == "INT4" {
						wantScan = reflect.TypeFor[int32]()
					}
					if c.Name() != tc.left || c.DatabaseTypeName() != tc.typ || c.ScanType() != wantScan {
						t.Fatalf("descriptor before Next: %v", c)
					}
					if nullable, known := c.Nullable(); nullable || known {
						t.Fatalf("synthetic nullable=%t/%t", nullable, known)
					}
					precision, scale, decimalKnown := tc.precision, tc.scale, tc.decimalKnown
					length, lengthKnown := tc.length, tc.lengthKnown
					// lib/pq exposes the protocol's absent typmod with these
					// codec-specific sentinel descriptors (root artifact8).
					if b.PG && tc.typ == "NUMERIC" && !decimalKnown {
						precision, scale, decimalKnown = 65535, 65531, true
					}
					if tc.typ == "VARCHAR" && !lengthKnown {
						length, lengthKnown = math.MaxInt64, true
						if b.PG {
							length = -5
						}
					}
					if got, known := c.Length(); got != length || known != lengthKnown {
						t.Fatalf("length=%d/%t want%d/%t", got, known, length, lengthKnown)
					}
					if p, s, known := c.DecimalSize(); p != precision || s != scale || known != decimalKnown {
						t.Fatalf("decimal=%d,%d/%t want%d,%d/%t", p, s, known, precision, scale, decimalKnown)
					}
				}
				querySetOpsRoutes(t, p, b, s)
			}
		}
	})
}

func TestReferenceQuerySetOpsZeroWidth(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		querySetOpsSeed(t, p, b)
		for _, tc := range []struct {
			op                        string
			both, leftOnly, rightOnly int
		}{
			{"UNION ALL", 10, 6, 4}, {"UNION", 1, 1, 1}, {"UNION DISTINCT", 1, 1, 1},
			{"INTERSECT ALL", 4, 0, 0}, {"INTERSECT", 1, 0, 0}, {"INTERSECT DISTINCT", 1, 0, 0},
			{"EXCEPT ALL", 2, 6, 0}, {"EXCEPT", 0, 1, 0}, {"EXCEPT DISTINCT", 0, 1, 0},
		} {
			for _, sides := range []struct {
				left, right string
				count       int
			}{
				{"", "", tc.both}, {"", " WHERE FALSE", tc.leftOnly}, {" WHERE FALSE", "", tc.rightOnly}, {" WHERE FALSE", " WHERE FALSE", 0},
			} {
				want := make([][]any, sides.count)
				for i := range want {
					want[i] = []any{}
				}
				q := "SELECT FROM sl" + sides.left + " " + tc.op + " SELECT FROM sr" + sides.right
				s := fixture.Select(q, []string{}, []string{}, want)
				s.Bag = true
				querySetOpsRoutes(t, p, b, s)
				querySetOpsRoutes(t, p, b, fixture.Exec(q, int64(sides.count)))
			}
		}
	})
}

func TestReferenceQuerySetOpsParameterDiagnostics(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		// Actual Prepare witnesses are independent of driver argument admission.
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{
			{"SELECT $1::TEXT UNION SELECT $1::INT", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "UNION types text and integer cannot be matched", Position: "30"}},
			{"SELECT $2 UNION SELECT 1", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P18", Message: "could not determine data type of parameter $1"}},
		} {
			s := fixture.ErrorDiagnostic(tc.q, tc.want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, true, "prepare"
			for _, nonContext := range []bool{false, true} {
				s.NonContext = nonContext
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
	})
}
