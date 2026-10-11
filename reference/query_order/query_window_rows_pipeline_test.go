package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowRowsPipeline(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRowsSeed(t, p, b)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g WHERE id<>2 ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(2)}, {int64(4), int64(2)}, {int64(5), int64(2)}}),
			fixture.Select("SELECT k,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n FROM g GROUP BY k ORDER BY k", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(2)}}),
			fixture.Select("SELECT DISTINCT k,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g ORDER BY k,n", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(2)}, {int64(3), int64(2)}}),
			fixture.Select("SELECT id,count(*) OVER(ORDER BY k,id ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) AS n FROM g ORDER BY id LIMIT 1", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(5)}}),
			queryWindowRangeResult("SELECT id,n FROM (SELECT id,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g) AS frames ORDER BY id", 1, 2, 2, 2, 2),
			queryWindowRangeResult("SELECT id,row_number() OVER(ORDER BY k,id ROWS 2 PRECEDING) AS n FROM g ORDER BY id", 1, 2, 3, 4, 5),
		} {
			queryWindowRoutes(t, p, b, s)
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT k,count(*) OVER(ORDER BY k ROWS 1 PRECEDING) AS n FROM g GROUP BY k HAVING count(*)>1 ORDER BY k", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(2)}}))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT a.id,count(*) OVER(ORDER BY a.id ROWS 1 PRECEDING) AS n FROM g AS a JOIN g AS z ON a.id=z.id ORDER BY a.id", 1, 2, 2, 2, 2))
		q := "SELECT id,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g"
		var doubled [][]any
		for _, row := range queryWindowValues(1, 2, 2, 2, 2) {
			doubled = append(doubled, row, row)
		}
		queryWindowRoutes(t, p, b, fixture.Select(q+" UNION ALL "+q+" ORDER BY id,n", []string{"id", "n"}, []string{"INT4", "INT8"}, doubled))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE rows_dst(id INT,n BIGINT)", 0))
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO rows_dst "+q, 5)
			s.Prepared = prepared
			queryWindowRun(t, p, b, s)
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,n FROM rows_dst ORDER BY id,n", []string{"id", "n"}, []string{"INT4", "INT8"}, doubled))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE offset_dependency(v INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO offset_dependency VALUES(1)", 1))
		queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW rows_dependency AS SELECT id,count(*) OVER(ORDER BY k,id ROWS (SELECT v FROM offset_dependency) PRECEDING) AS n FROM g", 0))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,n FROM rows_dependency ORDER BY id", 1, 2, 2, 2, 2))
		queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE offset_dependency RENAME COLUMN v TO renamed", 0))
		queryWindowRoutes(t, p, b, queryWindowRangeResult("SELECT id,n FROM rows_dependency ORDER BY id", 1, 2, 2, 2, 2))
		queryWindowRun(t, p, b, fixture.ImplicitError("DROP TABLE offset_dependency", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table offset_dependency because other objects depend on it", Detail: "view rows_dependency depends on table offset_dependency", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
	})
}

func queryWindowRowsMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, empty := range []bool{false, true} {
		s := queryWindowRowsResult("BETWEEN 1 PRECEDING AND 2 PRECEDING", 0, 0, 0, 0, 0)
		if empty {
			s.Query = "SELECT id,count(*) OVER(ORDER BY k,id ROWS 1 PRECEDING) AS n FROM g WHERE FALSE ORDER BY id"
			s.Rows = nil
		}
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			cols, err := rows.ColumnTypes()
			if err != nil || len(cols) != 2 {
				t.Fatal(cols, err)
			}
			if cols[1].ScanType() != reflect.TypeFor[int64]() {
				t.Fatal(cols[1].ScanType())
			}
			nullable, known := cols[1].Nullable()
			if nullable || known == b.PG {
				t.Fatal(nullable, known)
			}
			if n, ok := cols[1].Length(); n != 0 || ok {
				t.Fatal(n, ok)
			}
			if precision, scale, ok := cols[1].DecimalSize(); precision != 0 || scale != 0 || ok {
				t.Fatal(precision, scale, ok)
			}
		}
		s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
			if _, ok := v.(int64); !ok {
				t.Fatalf("column %d row %d raw %T", column, row, v)
			}
			return false
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
			var id int64
			var n sql.NullInt64
			if err := rows.Scan(&id, &n); err != nil || !n.Valid || id != s.Rows[row][0] || n.Int64 != s.Rows[row][1] {
				t.Fatal(id, n, err)
			}
		}
		queryWindowRoutes(t, p, b, s)
	}
}

func TestReferenceQueryWindowRowsMetadata(t *testing.T) {
	queryWindowRowsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowRowsSeed(t, p, b)
		queryWindowRowsMetadata(t, p, b)
		queryWindowRoutes(t, p, b, fixture.Select("SELECT count(*) OVER(ORDER BY k,id ROWS CURRENT ROW) FROM g ORDER BY id", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
	})
}
