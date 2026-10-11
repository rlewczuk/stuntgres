package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowMetadata(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		for _, empty := range []string{"", " WHERE FALSE"} {
			s := fixture.Select("SELECT id,p,row_number() OVER(ORDER BY id) AS rn,count(*) OVER() AS n FROM t"+empty+" ORDER BY id", []string{"id", "p", "rn", "n"}, []string{"INT4", "TEXT", "INT8", "INT8"}, nil)
			if empty == "" {
				s.Rows = [][]any{{int64(1), "a", int64(1), int64(7)}, {int64(2), "a", int64(2), int64(7)}, {int64(3), "a", int64(3), int64(7)}, {int64(4), "b", int64(4), int64(7)}, {int64(5), "b", int64(5), int64(7)}, {int64(6), nil, int64(6), int64(7)}, {int64(7), nil, int64(7), int64(7)}}
			}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil || len(cols) != 4 {
					t.Fatal(cols, err)
				}
				for i, c := range cols {
					scan := reflect.TypeFor[int64]()
					if i == 1 {
						scan = reflect.TypeFor[string]()
					}
					if b.PG && i == 0 {
						scan = reflect.TypeFor[int32]()
					}
					if c.Name() != s.Columns[i] || c.DatabaseTypeName() != s.Types[i] || c.ScanType() != scan {
						t.Fatal("metadata", i, c)
					}
					nullable, known := c.Nullable()
					wantNullable, wantKnown := false, !b.PG
					if i == 1 {
						wantNullable = true
					}
					if b.PG {
						wantNullable = false
					}
					if nullable != wantNullable || known != wantKnown {
						t.Fatal("nullable", i, nullable, known)
					}
					if i >= 2 {
						if n, k := c.Length(); n != 0 || k {
							t.Fatal("bigint length", n, k)
						}
						if p, s, k := c.DecimalSize(); p != 0 || s != 0 || k {
							t.Fatal("bigint decimal", p, s, k)
						}
					}
				}
			}
			s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
				if column < 2 {
					return false
				}
				if _, ok := v.(int64); !ok {
					t.Fatalf("window raw %T %v", v, v)
				}
				return true
			}
			s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
				var id int64
				var p sql.NullString
				var rn int64
				var n sql.NullInt64
				if err := rows.Scan(&id, &p, &rn, &n); err != nil || rn != int64(row+1) || !n.Valid || n.Int64 != 7 {
					t.Fatal(id, p, rn, n, err)
				}
			}
			queryWindowRoutes(t, p, b, s)
		}
	})
}

func TestReferenceQueryWindowLifecycle(t *testing.T) {
	queryWindowScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		q := "SELECT id,row_number() OVER(PARTITION BY p ORDER BY k*$1,id) AS rn FROM t WHERE id>$2 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{b.DB, stmt, q}
		for _, tc := range []struct {
			arg any
			v   []int64
		}{{1, []int64{1, 2, 3, 1, 2, 1, 2}}, {-1, []int64{2, 3, 1, 1, 2, 1, 2}}, {nil, []int64{1, 2, 3, 1, 2, 1, 2}}, {1, []int64{1, 2, 3, 1, 2, 1, 2}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "rn"}, []string{"INT4", "INT8"}, queryWindowValues(tc.v...), tc.arg, 0))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id", "rn"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}, {int64(4), int64(1)}, {int64(5), int64(2)}, {int64(6), int64(1)}, {int64(7), int64(2)}}, 1, 2))
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE atomic_dst(id INT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO atomic_dst VALUES(17)", 1))
		for _, tc := range []struct{ q, code, message string }{
			{"INSERT INTO atomic_dst SELECT row_number() OVER(ORDER BY 1/(id-3)) FROM t", "22012", "division by zero"},
			{"INSERT INTO atomic_dst SELECT row_number() OVER(ORDER BY id)+2147483646 FROM t", "22003", "integer out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				queryWindowRun(t, p, b, s)
				queryWindowRun(t, p, b, fixture.Select("SELECT id FROM atomic_dst", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
			}
		}
		for _, suffix := range []string{"", " LIMIT 1"} {
			s := fixture.ImplicitError("SELECT row_number() OVER(ORDER BY 1/(id-3)) FROM t"+suffix, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows = true
			for _, prepared := range []bool{false, true} {
				s.Prepared, s.ErrorStage = prepared, "execute"
				queryWindowRun(t, p, b, s)
			}
		}
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared, true)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				st, err := c.PrepareContext(p.Ctx, "SELECT count(*) OVER() FROM t")
				if err != nil {
					t.Fatal(err)
				}
				var rows *sql.Rows
				if prepared {
					rows, err = st.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, "SELECT count(*) OVER() FROM t")
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				var result sql.Result
				if prepared {
					result, err = st.ExecContext(ctx)
				} else {
					result, err = c.ExecContext(ctx, "SELECT count(*) OVER() FROM t")
				}
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
				st.Close()
				result, err = c.ExecContext(ctx, "INSERT INTO atomic_dst SELECT row_number() OVER() FROM t")
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT row_number() OVER(),count(*) OVER()", []string{"row_number", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
		queryWindowRun(t, p, b, fixture.Select("SELECT id FROM atomic_dst", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
	})
}
