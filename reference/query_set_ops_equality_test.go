package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// These are independent quotient-class bags, not a choice of the physical
// spelling retained for SQL-equal scales, padded strings, zeroes or intervals.
// Left: A×3, B, C, NULL×2. Right: A×2, B, D, NULL.
func querySetOpsEqualityExercise(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	operations := []struct {
		op      string
		classes []int
	}{
		{"UNION", []int{0, 1, 2, 3, 4}},
		{"UNION DISTINCT", []int{0, 1, 2, 3, 4}},
		{"UNION ALL", []int{0, 0, 0, 1, 1, 1, 1, 1, 2, 2, 3, 4}},
		{"INTERSECT", []int{0, 1, 2}},
		{"INTERSECT DISTINCT", []int{0, 1, 2}},
		{"INTERSECT ALL", []int{0, 1, 1, 2}},
		{"EXCEPT", []int{3}},
		{"EXCEPT DISTINCT", []int{3}},
		{"EXCEPT ALL", []int{0, 1, 3}},
	}
	for i, tc := range []struct {
		name, leftType, rightType, publicType string
		left, right, classify                 string
		array                                 bool
	}{
		{"numeric_scales", "NUMERIC", "NUMERIC", "NUMERIC", "(1.0),(1.00),(1.000),(2),(3),(NULL),(NULL)", "(1.0000),(1.00000),(2.00),(4),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v=1 THEN 1 WHEN v=2 THEN 2 WHEN v=3 THEN 3 ELSE 4 END", false},
		{"bpchar_padding", "CHAR(2)", "CHAR(4)", "BPCHAR", "('a'),('a '),('a'),('b'),('c'),(NULL),(NULL)", "('a'),('a  '),('b '),('d'),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='a'::CHAR(4) THEN 1 WHEN v='b'::CHAR(4) THEN 2 WHEN v='c'::CHAR(4) THEN 3 ELSE 4 END", false},
		{"real_nan_zero", "REAL", "REAL", "FLOAT4", "('0'),('-0'),('0'),('NaN'),('1'),(NULL),(NULL)", "('-0'),('0'),('NaN'),('2'),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='0'::REAL THEN 1 WHEN v='NaN'::REAL THEN 2 WHEN v='1'::REAL THEN 3 ELSE 4 END", false},
		{"double_nan_zero", "DOUBLE PRECISION", "DOUBLE PRECISION", "FLOAT8", "('0'),('-0'),('0'),('NaN'),('1'),(NULL),(NULL)", "('-0'),('0'),('NaN'),('2'),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='0'::DOUBLE PRECISION THEN 1 WHEN v='NaN'::DOUBLE PRECISION THEN 2 WHEN v='1'::DOUBLE PRECISION THEN 3 ELSE 4 END", false},
		{"interval_representations", "INTERVAL", "INTERVAL", "INTERVAL", "('1 month'),('30 days'),('720 hours'),('1 day'),('2 days'),(NULL),(NULL)", "('30 days'),('1 month'),('24 hours'),('3 days'),(NULL)", "CASE WHEN v IS NULL THEN 0 WHEN v='1 month'::INTERVAL THEN 1 WHEN v='1 day'::INTERVAL THEN 2 WHEN v='2 days'::INTERVAL THEN 3 ELSE 4 END", false},
		{"array_shape_bounds", "INT[]", "INT[]", "_INT4", "('{1,2}'),('{1,2}'),('{1,2}'),('[0:1]={1,2}'),('{{1,2}}'),(NULL),(NULL)", "('{1,2}'),('{1,2}'),('[0:1]={1,2}'),('[2:3]={1,2}'),(NULL)", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, right := fmt.Sprintf("set_equal_left_%d", i), fmt.Sprintf("set_equal_right_%d", i)
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE "+left+"(v "+tc.leftType+")", 0),
				referenceExec("CREATE TABLE "+right+"(v "+tc.rightType+")", 0),
				referenceExec("INSERT INTO "+left+" VALUES"+tc.left, 7),
				referenceExec("INSERT INTO "+right+" VALUES"+tc.right, 5),
			} {
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			for _, op := range operations {
				t.Run(op.op, func(t *testing.T) {
					set := "SELECT v FROM " + left + " " + op.op + " SELECT v FROM " + right
					query, name, typ := "SELECT "+tc.classify+" AS equality_class FROM ("+set+") s", "equality_class", "INT4"
					want := make([][]any, len(op.classes))
					for n, class := range op.classes {
						want[n] = []any{int64(class)}
						if tc.array {
							want[n] = []any{[]any{nil, "{1,2}", "[0:1]={1,2}", "{{1,2}}", "[2:3]={1,2}"}[class]}
						}
					}
					if tc.array {
						query, name, typ = set, "v", tc.publicType
					}
					result := querySetOpsBag(query, name, typ, want)
					querySetOpsRoutes(t, p, b, result)
					querySetOpsRoutes(t, p, b, referenceExec(query, int64(len(want))))
					held := referenceViewHold(t, p, b, query)
					for range 2 {
						referenceRunStep(t, p.ctx, held, b.recorder, result)
					}
					// The common set schema itself, not the classifier's INT4
					// schema, is checked before Next on every direct/prepared route.
					empty := querySetOpsBag("SELECT v FROM "+left+" WHERE FALSE "+op.op+" SELECT v FROM "+right+" WHERE FALSE", "v", tc.publicType, [][]any{})
					empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil || len(columns) != 1 {
							t.Fatal(columns, err)
						}
						if columns[0].Name() != "v" || columns[0].DatabaseTypeName() != tc.publicType {
							t.Fatalf("common set metadata before Next: name=%q type=%q", columns[0].Name(), columns[0].DatabaseTypeName())
						}
					}
					querySetOpsRoutes(t, p, b, empty)
				})
			}
		})
	}
}

func TestReferenceQuerySetOpsEquality(t *testing.T) {
	querySetOpsScenario(t, func(p *referencePair, b referenceBackend) {
		querySetOpsEqualityExercise(t, p, b)
	})
}
