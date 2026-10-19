package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateRowPrepared(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		stmt, err := b.DB.PrepareContext(p.Ctx, "UPDATE t SET (a,b)=($1,$2) WHERE id=$3 RETURNING id,a,b")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			args []any
			want [][]any
		}{
			{[]any{7, 8, 1}, [][]any{{int64(1), int64(7), int64(8)}}},
			{[]any{nil, 9, 1}, [][]any{{int64(1), nil, int64(9)}}},
			{[]any{9, 10, 2}, [][]any{{int64(2), int64(9), int64(10)}}},
		} {
			rows, err := stmt.QueryContext(p.Ctx, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			fixture.UpdateCoreRetained(t, b.PG, rows, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, tc.want, false)
		}
		run(fixture.Exec("CREATE TABLE s(id INT,x INT,y INT)", 0))
		held, err := b.DB.PrepareContext(p.Ctx, "UPDATE t SET (a,b)=(SELECT x+$1,y FROM s WHERE s.id=t.id) WHERE t.id=$2 RETURNING id,a,b")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(delta int, want [][]any) {
			rows, err := held.QueryContext(p.Ctx, delta, 1)
			if err != nil {
				t.Fatal(err)
			}
			fixture.UpdateCoreRetained(t, b.PG, rows, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, want, false)
		}
		check(1, [][]any{{int64(1), nil, nil}})
		run(fixture.Exec("INSERT INTO s VALUES(1,100,200)", 1))
		check(2, [][]any{{int64(1), int64(102), int64(200)}})
		run(fixture.Exec("INSERT INTO s VALUES(1,300,400)", 1))
		rows, err := held.QueryContext(p.Ctx, 3, 1)
		if rows != nil {
			rows.Close()
			t.Fatal("cardinality failure exposed rows")
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "more than one row returned by a subquery used as an expression"})
		run(updateRowSnapshot([][]any{{int64(1), int64(102), int64(200)}, {int64(2), int64(9), int64(10)}}))
		run(fixture.Exec("DELETE FROM s WHERE x=100", 1))
		check(4, [][]any{{int64(1), int64(304), int64(400)}})
		run(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
		rows, err = stmt.QueryContext(p.Ctx, nil, 9, 1)
		if rows != nil {
			rows.Close()
			t.Fatal("NULL failure exposed RETURNING")
		}
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (1, null, 9).", Schema: "public", Table: "t", Column: "a"})
		run(updateRowSnapshot([][]any{{int64(1), int64(304), int64(400)}, {int64(2), int64(9), int64(10)}}))
		rows, err = stmt.QueryContext(p.Ctx, 11, 12, 1)
		if err != nil {
			t.Fatal(err)
		}
		fixture.UpdateCoreRetained(t, b.PG, rows, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(12)}}, false)
	})
}

func TestReferenceUpdateRowMetadata(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,v VARCHAR(3),n NUMERIC(4,1))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,1,'old',1.0)", 1))
		q := "UPDATE t SET (a,v,n)=ROW($1,$2,$3) WHERE id=$4 RETURNING a,v,n"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, prepared := range []bool{false, true} {
			for _, id := range []int{1, 9} {
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx, 7, "abc", "1.25", id)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, q, 7, "abc", "1.25", id)
				}
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if len(metadata) != 3 || metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "VARCHAR" || metadata[2].DatabaseTypeName() != "NUMERIC" {
					rows.Close()
					t.Fatal(metadata)
				}
				// Both public adapters expose the destination VARCHAR typmod.
				length, known := metadata[1].Length()
				if !known || length != 3 {
					rows.Close()
					t.Fatal("destination length", length, known)
				}
				precision, scale, known := metadata[2].DecimalSize()
				if !known || precision != 4 || scale != 1 {
					rows.Close()
					t.Fatal("destination numeric typmod", precision, scale, known)
				}
				want := [][]any{}
				if id == 1 {
					want = [][]any{{int64(7), "abc", "1.3"}}
				}
				fixture.UpdateCoreRetained(t, b.PG, rows, []string{"a", "v", "n"}, []string{"INT4", "VARCHAR", "NUMERIC"}, want, false)
			}
		}
		run(fixture.Select("UPDATE t SET (a,v,n)=(SELECT 8,'xyz',2.25) RETURNING WITH (OLD AS o, NEW AS n) o.a,n.a,n.v,n.n", []string{"a", "a", "v", "n"}, []string{"INT4", "INT4", "VARCHAR", "NUMERIC"}, [][]any{{int64(7), int64(8), "xyz", "2.3"}}))
	})
}

func TestReferenceUpdateRowAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				updateRowSeed(t, p, b)
				run(fixture.Exec("CREATE TABLE s(id INT,x INT,y INT)", 0))
				run(fixture.Exec("INSERT INTO s VALUES(1,100,200),(2,300,400),(2,500,600)", 3))
				q := "UPDATE t SET (a,b)=(SELECT x,y FROM s WHERE s.id=t.id) RETURNING id,a,b"
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = b.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
				}
				for _, query := range []bool{false, true} {
					if query {
						var rows *sql.Rows
						if prepared {
							rows, err = stmt.QueryContext(p.Ctx)
						} else {
							rows, err = b.DB.QueryContext(p.Ctx, q)
						}
						if rows != nil {
							rows.Close()
							t.Fatal("partial RETURNING exposed")
						}
					} else {
						var result sql.Result
						if prepared {
							result, err = stmt.ExecContext(p.Ctx)
						} else {
							result, err = b.DB.ExecContext(p.Ctx, q)
						}
						if result != nil {
							t.Fatal("failed update exposed result")
						}
					}
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "more than one row returned by a subquery used as an expression"})
					run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				txrun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				txrun(fixture.Exec("BEGIN", 0))
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				txrun(fixture.Exec("UPDATE t SET a=11 WHERE id=1", 1))
				txrun(fixture.Exec("SAVEPOINT rowpoint", 0))
				txrun(fixture.ImplicitError(q, fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"}))
				txrun(fixture.ErrorDiagnostic("SELECT a FROM t", fixture.TransactionAborted()))
				txrun(fixture.Exec("ROLLBACK TO rowpoint", 0))
				txrun(updateRowSnapshot([][]any{{int64(1), int64(11), int64(20)}, {int64(2), int64(30), int64(40)}}))
				txrun(fixture.Exec("DELETE FROM s WHERE x=500", 1))
				txrun(fixture.Exec(q, 2))
				txrun(fixture.Exec("ROLLBACK", 0))
				run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			})
		})
	}
	t.Run("null_complete_replacement", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,a INT NOT NULL,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10,20),(2,30,40)", 2))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("UPDATE t SET id=id+10,(a,b)=(SELECT 7,8 WHERE FALSE) WHERE id=2 RETURNING *", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (12, null, null).", Schema: "public", Table: "t", Column: "a"})
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			}
		})
	})
}
