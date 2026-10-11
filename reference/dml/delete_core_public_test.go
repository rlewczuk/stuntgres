//go:build reference

package reference_test

import (
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteCorePrepared(t *testing.T) {
	p := fixture.NewPair(t)
	for i, db := range []*sql.DB{p.Memory, p.Postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i]
			a, _ := fixture.LockConnections(t, p.Ctx, db)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5)
			stmt, err := a.PrepareContext(p.Ctx, "DELETE FROM t AS d WHERE d.v=$1 OR d.v=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			failed, err := stmt.ExecContext(p.Ctx, "oops")
			if failed != nil {
				t.Fatal("invalid inferred INT4 parameter exposed a result")
			}
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
			want.Context = "unnamed portal parameter $1 = '...'"
			fixture.AssertDiagnostic(t, err, &want)
			fixture.DeleteReferenceRows(t, p, a, r, 1, 2, 3, 4, 4)
			for _, tc := range []struct {
				arg   any
				count int64
			}{{10, 3}, {nil, 0}, {20, 1}, {10, 0}} {
				result, err := stmt.ExecContext(p.Ctx, tc.arg)
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if err != nil || count != tc.count {
					t.Fatal(count, err, tc.count)
				}
				if _, err = result.LastInsertId(); err == nil {
					t.Fatal("DELETE invented LastInsertId")
				}
			}
			fixture.DeleteReferenceRows(t, p, a, r, 2)
			// A held statement survives an unrelated descriptor change, rebinds its
			// referenced column, and still observes the next command's new row.
			fixture.LockExec(t, p.Ctx, a, "ALTER TABLE t RENAME COLUMN f TO flag", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(7,10,FALSE)", 1)
			result, err := stmt.ExecContext(p.Ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			for _, args := range [][]any{nil, {10, 20}} {
				result, err := stmt.ExecContext(p.Ctx, args...)
				if err == nil || result != nil {
					t.Fatal("argument-count failure must expose no result", result, err)
				}
				var native *stuntgres.Error
				if errors.As(err, &native) {
					t.Fatal("Go argument-count boundary fabricated SQLSTATE", err)
				}
			}
			fixture.DeleteReferenceRows(t, p, a, r, 2)
			truth, err := a.PrepareContext(p.Ctx, "DELETE FROM t WHERE $1")
			if err != nil {
				t.Fatal(err)
			}
			defer truth.Close()
			failed, err = truth.ExecContext(p.Ctx, "truth")
			if failed != nil {
				t.Fatal("invalid contextual BOOL parameter exposed a result")
			}
			want = fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`}
			want.Context = "unnamed portal parameter $1 = '...'"
			fixture.AssertDiagnostic(t, err, &want)
			fixture.DeleteReferenceRows(t, p, a, r, 2)
			for _, tc := range []struct {
				arg   any
				count int64
			}{{false, 0}, {nil, 0}, {true, 1}, {true, 0}} {
				result, err := truth.ExecContext(p.Ctx, tc.arg)
				if err != nil {
					t.Fatal(err)
				}
				n, err := result.RowsAffected()
				if err != nil || n != tc.count {
					t.Fatal(n, err)
				}
			}
			// Query of a command without RETURNING has an empty description, not a
			// fabricated row containing the affected count.
			rows, err := stmt.QueryContext(p.Ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			names, err := rows.Columns()
			if err != nil || len(names) != 0 {
				t.Fatal(names, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 0 {
				t.Fatal(types, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("DELETE without RETURNING exposed rows", rows.Err())
			}
			fixture.DeleteReferenceRows(t, p, a, r)
		})
	}
}

func TestReferenceDeleteCoreVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			p := fixture.NewPair(t)
			for i, db := range []*sql.DB{p.Memory, p.Postgres} {
				t.Run([]string{"native", "postgres"}[i]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
					r := []*fixture.Recorder{p.MemoryRecord, p.PostgresRecord}[i]
					a, b := fixture.LockConnections(t, p.Ctx, db)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT NOT NULL)", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1),(2),(2)", 3)
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					held, err := tx.PrepareContext(p.Ctx, "DELETE FROM t WHERE id=$1")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, want := range []int64{1, 0} {
						result, err := held.ExecContext(p.Ctx, 1)
						if err != nil {
							t.Fatal(err)
						}
						n, err := result.RowsAffected()
						if err != nil || n != want {
							t.Fatal(n, err)
						}
					}
					fixture.DeleteReferenceRows(t, p, tx, r, 2, 2)
					fixture.DeleteReferenceRows(t, p, b, r, 1, 2, 2)
					fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(7)", 1)
					fixture.LockExec(t, p.Ctx, tx, "DELETE FROM t WHERE id=7", 1)
					fixture.LockExec(t, p.Ctx, tx, "DELETE FROM t WHERE id=2", 2)
					fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(2),(2)", 2)
					fixture.DeleteReferenceRows(t, p, tx, r, 2, 2)
					fixture.DeleteReferenceRows(t, p, b, r, 1, 2, 2)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						fixture.DeleteReferenceRows(t, p, b, r, 2, 2)
					} else {
						fixture.DeleteReferenceRows(t, p, b, r, 1, 2, 2)
					}
				})
			}
		})
	}
}

func TestReferenceDeleteCoreNotNull(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL,v INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,NULL),(2,20),(3,30)", 3))
	p.Step(t, fixture.Exec("DELETE FROM t WHERE id=1", 1))
	p.Step(t, fixture.Exec("ALTER TABLE t ALTER COLUMN v SET NOT NULL", 0))
	p.Step(t, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
	p.Step(t, fixture.Exec("DELETE FROM t", 2))
	bad := fixture.ImplicitError("INSERT INTO t VALUES(4,NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, null).", Schema: "public", Table: "t", Column: "v"})
	bad.ErrorStage = "execute"
	p.Step(t, bad)
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(5,50)", 1))
	p.Step(t, fixture.Exec("ALTER TABLE t ALTER COLUMN v DROP NOT NULL", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(6,NULL)", 1))
	bad = fixture.ImplicitError("ALTER TABLE t ALTER COLUMN v SET NOT NULL", fixture.Diagnostic{Code: "23502", Message: `column "v" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "v"})
	bad.ErrorStage = "execute"
	p.Step(t, bad)
	p.Step(t, fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(50)}, {int64(6), nil}}))
}

func TestReferenceDeleteCoreZeroWidth(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewPair(t)
			p.Step(t, fixture.Exec("CREATE TABLE zero(v INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO zero VALUES(1),(1),(2)", 3))
			p.Step(t, fixture.Exec("ALTER TABLE zero DROP COLUMN v", 0))
			before := fixture.Select("SELECT * FROM zero", []string{}, []string{}, [][]any{{}, {}, {}})
			before.Bag = true
			p.Step(t, before)
			deletion := fixture.Exec("DELETE FROM zero", 3)
			deletion.Prepared = prepared
			p.Step(t, deletion)
			empty := fixture.Select("SELECT * FROM zero", []string{}, []string{}, [][]any{})
			empty.Bag = true
			p.Step(t, empty)
			deletion.Affected = 0
			p.Step(t, deletion)
			p.Step(t, empty)
		})
	}
}
