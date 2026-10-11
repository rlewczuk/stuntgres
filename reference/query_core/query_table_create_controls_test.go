package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateFeatureSmoke(t *testing.T) {
	queryTableCreateSmoke(t, false)
}

func TestReferenceQueryTableCreateControls(t *testing.T) {
	queryTableCreateSmoke(t, true)
}

func queryTableCreateSmoke(t *testing.T, retained bool) {
	t.Helper()
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			fixture.QueryTableCreateSource(run)
			q := "CREATE TABLE qc_smoke_copy AS SELECT i+$1::integer AS k,s FROM qc_source"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_smoke_copy")
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 3, 1))
			run(fixture.Select("SELECT i,s INTO qc_smoke_into FROM qc_source", []string{}, []string{}, nil))
			run(fixture.Exec("CREATE TABLE qc_smoke_empty AS SELECT 1/0 AS x WITH NO DATA", 0))
			run(fixture.Exec("CREATE TABLE qc_smoke_zero AS SELECT FROM qc_source", 3))
			run(fixture.Exec("CREATE TABLE qc_smoke_typed AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n", 1))
			run(fixture.Exec("UPDATE qc_source SET i=99", 3))
			run(fixture.Exec("DROP TABLE qc_source RESTRICT", 0))
			check := func() {
				s := fixture.Select("SELECT k,s FROM qc_smoke_copy", []string{"k", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "a"}, {int64(2), "a"}, {int64(3), nil}})
				s.Bag = true
				run(s)
				run(fixture.QueryTableCreateBag("qc_smoke_into"))
				run(fixture.Select("SELECT x FROM qc_smoke_empty", []string{"x"}, []string{"INT4"}, nil))
				run(fixture.Select("SELECT * FROM qc_smoke_zero", []string{}, []string{}, [][]any{{}, {}, {}}))
				run(fixture.Select("SELECT * FROM qc_smoke_typed", []string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}}))
			}
			check()
			run(fixture.Exec("INSERT INTO qc_smoke_copy VALUES(4,'b')", 1))
			run(fixture.Exec("UPDATE qc_smoke_copy SET k=5 WHERE k=4", 1))
			run(fixture.Exec("DELETE FROM qc_smoke_copy WHERE k=5", 1))
			run(fixture.ImplicitError("CREATE TABLE qc_smoke_bad AS SELECT 1 AS x,2 AS x", fixture.Diagnostic{Code: "42701", Message: `column "x" specified more than once`}))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_smoke_bad")
			run(fixture.ImplicitError("CREATE TABLE qc_smoke_bad AS SELECT 1/0 AS x", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_smoke_bad")
			if !b.PG {
				// Explicit native control phase: these APIs have no PostgreSQL equivalent.
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				if retained {
					rows, err := conn.QueryContext(p.Ctx, "SELECT k,s FROM qc_smoke_copy ORDER BY k")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE qc_injected AS SELECT 1 AS i")); err == nil {
						t.Fatal("Restore accepted query creation")
					}
					check()
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					want := [][]any{{int64(2), "a"}, {int64(2), "a"}, {int64(3), nil}}
					for _, pair := range want {
						if !rows.Next() {
							t.Fatal("retained cursor ended early", rows.Err())
						}
						var k int64
						var s *string
						if err := rows.Scan(&k, &s); err != nil {
							t.Fatal(err)
						}
						if k != pair[0] || (s == nil) != (pair[1] == nil) || s != nil && *s != pair[1] {
							t.Fatalf("retained row %d %v want %v", k, s, pair)
						}
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("retained cursor tail", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
				} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.QueryTableCreateAbsent(t, run, b.PG, "qc_smoke_copy")
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				check()
			} else if dump != nil {
				// Replay the actual native-emitted ordinary definition/data on PG.
				run(fixture.Exec("DROP TABLE qc_smoke_copy,qc_smoke_into,qc_smoke_empty,qc_smoke_zero,qc_smoke_typed", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PostgreSQL rejected native dump", err)
				}
				check()
			}
		})
	}
}
