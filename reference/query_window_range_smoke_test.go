package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func queryWindowRangeSmokeContracts(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	q := "SELECT id,count(*) OVER(ORDER BY k RANGE $1 PRECEDING) AS n FROM range_t ORDER BY id"
	stmt, err := b.db.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := referenceHeldStatement{b.db, stmt, q}
	for _, tc := range []struct {
		arg    any
		values []int64
	}{{3, []int64{1, 3, 3, 4, 4, 2, 2}}, {1, []int64{1, 3, 3, 1, 2, 2, 2}}} {
		s := queryWindowRangeResult(q, tc.values...)
		s.args = []any{tc.arg}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		if tc.arg == 3 {
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "22004", Message: "frame starting offset must not be null"}, nil)
			failure.selectRows, failure.errorStage = true, "execute"
			referenceRunStep(t, p.ctx, held, b.recorder, failure)
		}
	}
	queryWindowRangeAnalysis(t, p, b, "SELECT count(*) OVER(RANGE 1 PRECEDING) FROM range_t", "42P20", "RANGE with offset PRECEDING/FOLLOWING requires exactly one ORDER BY column", "(RANGE", "")
	queryWindowRangeAnalysis(t, p, b, "SELECT count(*) OVER(ORDER BY 'x'::TEXT RANGE 1 PRECEDING) FROM range_t", "0A000", "RANGE with offset PRECEDING/FOLLOWING is not supported for column type text", "1 PRECEDING", "")
	empty := referenceSelect("SELECT count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM range_t WHERE FALSE", []string{"n"}, []string{"INT8"}, nil)
	empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil || len(cols) != 1 || cols[0].ScanType() != reflect.TypeFor[int64]() {
			t.Fatal(cols, err)
		}
	}
	queryWindowRoutes(t, p, b, empty)
	queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE BETWEEN 1 PRECEDING AND 2 PRECEDING) AS n FROM range_t ORDER BY id", 0, 0, 0, 0, 0, 2, 2))
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE smoke_gaps(id INT,k INT)", 0), referenceExec("INSERT INTO smoke_gaps VALUES(1,1),(2,3),(3,5)", 3),
		referenceExec("CREATE TABLE smoke_clock(id INT,k TIME)", 0), referenceExec("INSERT INTO smoke_clock VALUES(1,'00:30'),(2,'23:30')", 2),
		referenceExec("CREATE VIEW smoke_range_view AS SELECT id,count(*) OVER w AS n FROM range_t WINDOW w AS(ORDER BY k RANGE 1 PRECEDING)", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE 1 PRECEDING) AS n FROM smoke_gaps ORDER BY id", 1, 1, 1))
	if b.pg {
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n FROM smoke_gaps ORDER BY id", 1, 2, 2))
	} else {
		queryWindowRangeAnalysis(t, p, b, "SELECT count(*) OVER(ORDER BY k ROWS 1 PRECEDING) FROM smoke_gaps", "0A000", "explicit window frames are unsupported", "ROWS", "")
	}
	queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,count(*) OVER(ORDER BY k RANGE INTERVAL '1 hour' PRECEDING) AS n FROM smoke_clock ORDER BY id", 1, 1))
	view := queryWindowRangeResult("SELECT id,n FROM smoke_range_view ORDER BY id", 1, 3, 3, 1, 2, 2, 2)
	queryWindowRoutes(t, p, b, view)
	if !b.pg {
		queryWindowRangeControls(t, p, []referenceStep{view})
	}
}
