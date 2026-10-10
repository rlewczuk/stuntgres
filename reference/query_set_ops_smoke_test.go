package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceQuerySetOpsFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		querySetOpsControlSeed(t, p, b)
		for _, tc := range []struct {
			op   string
			rows [][]any
		}{
			{"UNION", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}},
			{"UNION DISTINCT", [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}},
			{"UNION ALL", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}, {nil}}},
			{"INTERSECT", [][]any{{int64(1)}, {nil}}},
			{"INTERSECT DISTINCT", [][]any{{int64(1)}, {nil}}},
			{"INTERSECT ALL", [][]any{{int64(1)}, {int64(1)}, {nil}}},
			{"EXCEPT", [][]any{{int64(2)}}},
			{"EXCEPT DISTINCT", [][]any{{int64(2)}}},
			{"EXCEPT ALL", [][]any{{int64(1)}, {int64(2)}, {nil}}},
		} {
			q := "SELECT renamed_v AS v FROM renamed_sl " + tc.op + " SELECT v FROM sr"
			querySetOpsRoutes(t, p, b, querySetOpsBag(q, "v", "INT4", tc.rows))
			querySetOpsRoutes(t, p, b, referenceExec(q, int64(len(tc.rows))))
		}
		for _, s := range []referenceStep{
			querySetOpsBag("SELECT 1 AS v UNION SELECT 2 INTERSECT SELECT 2", "v", "INT4", [][]any{{int64(1)}, {int64(2)}}),
			querySetOpsBag("(SELECT 1 AS v UNION SELECT 2) INTERSECT SELECT 2", "v", "INT4", [][]any{{int64(2)}}),
			querySetOpsBag("VALUES(1),(1),(NULL) INTERSECT ALL VALUES(1),(NULL),(NULL)", "column1", "INT4", [][]any{{int64(1)}, {nil}}),
			referenceSelect("(SELECT renamed_v AS v FROM renamed_sl ORDER BY renamed_v NULLS LAST LIMIT 2) UNION ALL (SELECT v FROM sr ORDER BY v DESC NULLS LAST LIMIT 1) ORDER BY 1 LIMIT 2 OFFSET 1", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}),
			querySetOpsBag("SELECT NULL AS v UNION SELECT 1", "v", "INT4", [][]any{{nil}, {int64(1)}}),
			querySetOpsBag("SELECT * FROM set_copy_bag FOR UPDATE", "v", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}, {nil}, {nil}, {nil}}),
		} {
			querySetOpsRoutes(t, p, b, s)
		}
		// Independently check codec widths and exact exported REAL values. pq
		// decodes float4's shortest decimal; native exports its binary32 value.
		value := float64(float32(2.2))
		if b.pg {
			value = 2.2
		}
		f := querySetOpsBag("SELECT 1 AS v UNION SELECT '2.2'::REAL", "v", "FLOAT4", [][]any{{float64(1)}, {value}})
		f.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			scan := reflect.TypeFor[float64]()
			if b.pg {
				scan = reflect.TypeFor[float32]()
			}
			if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "FLOAT4" || columns[0].ScanType() != scan {
				t.Fatal("REAL common metadata before Next", columns, err)
			}
		}
		f.checkRaw = func(t *testing.T, column, row int, raw any) bool {
			v, ok := raw.(float64)
			if !ok || v != 1 && v != value {
				t.Fatalf("exact FLOAT4 export: %T %v", raw, raw)
			}
			return true
		}
		querySetOpsRoutes(t, p, b, f)
		q := "SELECT $1 AS v UNION ALL SELECT 1"
		held := referenceViewHold(t, p, b, q)
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{7, [][]any{{int64(7)}, {int64(1)}}},
			{nil, [][]any{{nil}, {int64(1)}}},
			{9, [][]any{{int64(9)}, {int64(1)}}},
		} {
			s := querySetOpsBag(q, "v", "INT4", tc.rows)
			s.args = []any{tc.arg}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		querySetOpsConsumerWitness(t, p, b)
		width := referenceImplicitError("SELECT /*é*/ 1 UNION SELECT 2,3", referenceDiagnostic{Code: "42601", Message: "each UNION query must have the same number of columns", Position: "29"})
		querySetOpsRoutes(t, p, b, width)
		order := referenceImplicitError("SELECT 1 AS x UNION SELECT 2 ORDER BY x+1", referenceDiagnostic{Code: "0A000", Message: "invalid UNION/INTERSECT/EXCEPT ORDER BY clause", Position: "39", Detail: "Only result column names can be used, not expressions or functions.", Hint: "Add the expression/function to every SELECT, or move the UNION into a FROM clause."})
		querySetOpsRoutes(t, p, b, order)
		locks := referenceImplicitError("SELECT * FROM (SELECT v FROM renamed_sl AS sl(v) UNION SELECT v FROM sr) d FOR SHARE", referenceDiagnostic{Code: "0A000", Message: "FOR SHARE is not allowed with UNION/INTERSECT/EXCEPT"})
		querySetOpsRoutes(t, p, b, locks)
		for _, s := range querySetOpsControlReads(b.pg) {
			querySetOpsRoutes(t, p, b, s)
			held := referenceViewHold(t, p, b, s.query)
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
	}
	querySetOpsNativeControls(t, p)
}
