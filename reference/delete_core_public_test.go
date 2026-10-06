//go:build reference

package reference_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
)

func deleteReferenceRows(t *testing.T, p *referencePair, x referenceExecutor, r *referenceRecorder, ids ...int64) {
	t.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	referenceRunStep(t, p.ctx, x, r, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, rows))
}

func TestReferenceDeleteCorePrepared(t *testing.T) {
	p := newReferencePair(t)
	for i, db := range []*sql.DB{p.memory, p.postgres} {
		t.Run([]string{"native", "postgres"}[i], func(t *testing.T) {
			r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
			a, _ := referenceLockConnections(t, p.ctx, db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5)
			stmt, err := a.PrepareContext(p.ctx, "DELETE FROM t AS d WHERE d.v=$1 OR d.v=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			failed, err := stmt.ExecContext(p.ctx, "oops")
			if failed != nil {
				t.Fatal("invalid inferred INT4 parameter exposed a result")
			}
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
			if i == 1 {
				want.Context = "unnamed portal parameter $1 = '...'"
			}
			referenceAssertDiagnostic(t, err, &want)
			deleteReferenceRows(t, p, a, r, 1, 2, 3, 4, 4)
			for _, tc := range []struct {
				arg   any
				count int64
			}{{10, 3}, {nil, 0}, {20, 1}, {10, 0}} {
				result, err := stmt.ExecContext(p.ctx, tc.arg)
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
			deleteReferenceRows(t, p, a, r, 2)
			// A held statement survives an unrelated descriptor change, rebinds its
			// referenced column, and still observes the next command's new row.
			referenceLockExec(t, p.ctx, a, "ALTER TABLE t RENAME COLUMN f TO flag", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(7,10,FALSE)", 1)
			result, err := stmt.ExecContext(p.ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			for _, args := range [][]any{nil, {10, 20}} {
				result, err := stmt.ExecContext(p.ctx, args...)
				if err == nil || result != nil {
					t.Fatal("argument-count failure must expose no result", result, err)
				}
				var native *stuntgres.Error
				if errors.As(err, &native) {
					t.Fatal("Go argument-count boundary fabricated SQLSTATE", err)
				}
			}
			deleteReferenceRows(t, p, a, r, 2)
			truth, err := a.PrepareContext(p.ctx, "DELETE FROM t WHERE $1")
			if err != nil {
				t.Fatal(err)
			}
			defer truth.Close()
			failed, err = truth.ExecContext(p.ctx, "truth")
			if failed != nil {
				t.Fatal("invalid contextual BOOL parameter exposed a result")
			}
			want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`}
			if i == 1 {
				want.Context = "unnamed portal parameter $1 = '...'"
			}
			referenceAssertDiagnostic(t, err, &want)
			deleteReferenceRows(t, p, a, r, 2)
			for _, tc := range []struct {
				arg   any
				count int64
			}{{false, 0}, {nil, 0}, {true, 1}, {true, 0}} {
				result, err := truth.ExecContext(p.ctx, tc.arg)
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
			rows, err := stmt.QueryContext(p.ctx, 10)
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
			deleteReferenceRows(t, p, a, r)
		})
	}
}

func TestReferenceDeleteCoreVisibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			p := newReferencePair(t)
			for i, db := range []*sql.DB{p.memory, p.postgres} {
				t.Run([]string{"native", "postgres"}[i]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
					r := []*referenceRecorder{p.memoryRecord, p.postgresRecord}[i]
					a, b := referenceLockConnections(t, p.ctx, db)
					referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT NOT NULL)", 0)
					referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1),(2),(2)", 3)
					tx, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					held, err := tx.PrepareContext(p.ctx, "DELETE FROM t WHERE id=$1")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					for _, want := range []int64{1, 0} {
						result, err := held.ExecContext(p.ctx, 1)
						if err != nil {
							t.Fatal(err)
						}
						n, err := result.RowsAffected()
						if err != nil || n != want {
							t.Fatal(n, err)
						}
					}
					deleteReferenceRows(t, p, tx, r, 2, 2)
					deleteReferenceRows(t, p, b, r, 1, 2, 2)
					referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(7)", 1)
					referenceLockExec(t, p.ctx, tx, "DELETE FROM t WHERE id=7", 1)
					referenceLockExec(t, p.ctx, tx, "DELETE FROM t WHERE id=2", 2)
					referenceLockExec(t, p.ctx, tx, "INSERT INTO t VALUES(2),(2)", 2)
					deleteReferenceRows(t, p, tx, r, 2, 2)
					deleteReferenceRows(t, p, b, r, 1, 2, 2)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if commit {
						deleteReferenceRows(t, p, b, r, 2, 2)
					} else {
						deleteReferenceRows(t, p, b, r, 1, 2, 2)
					}
				})
			}
		})
	}
}

func TestReferenceDeleteCoreForeignKeyBoundary(t *testing.T) {
	p := newReferencePair(t)
	// PostgreSQL-only executable mechanism: native FK syntax/enforcement is not
	// implemented. This is not native parity or a native constraint substitute.
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s) }
	run(referenceExec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
	run(referenceExec("CREATE TABLE c(pid INT CONSTRAINT c_pid_fkey REFERENCES p(id))", 0))
	run(referenceExec("INSERT INTO p VALUES(1),(2)", 2))
	run(referenceExec("INSERT INTO c VALUES(2)", 1))
	step := referenceImplicitError("DELETE FROM p", referenceDiagnostic{Code: "23503", Message: `update or delete on table "p" violates foreign key constraint "c_pid_fkey" on table "c"`, Detail: `Key (id)=(2) is still referenced from table "c".`, Schema: "public", Table: "c", Constraint: "c_pid_fkey"})
	for _, prepared := range []bool{false, true} {
		step.prepared, step.errorStage = prepared, "execute"
		run(step)
		run(referenceSelect("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(referenceSelect("SELECT pid FROM c", []string{"pid"}, []string{"INT4"}, [][]any{{int64(2)}}))
	}
}

func TestReferenceDeleteCoreNotNull(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,v INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,NULL),(2,20),(3,30)", 3))
	p.step(t, referenceExec("DELETE FROM t WHERE id=1", 1))
	p.step(t, referenceExec("ALTER TABLE t ALTER COLUMN v SET NOT NULL", 0))
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}, {int64(3), int64(30)}}))
	p.step(t, referenceExec("DELETE FROM t", 2))
	bad := referenceImplicitError("INSERT INTO t VALUES(4,NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (4, null).", Schema: "public", Table: "t", Column: "v"})
	bad.errorStage = "execute"
	p.step(t, bad)
	p.step(t, referenceExec("INSERT INTO t VALUES(5,50)", 1))
	p.step(t, referenceExec("ALTER TABLE t ALTER COLUMN v DROP NOT NULL", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(6,NULL)", 1))
	bad = referenceImplicitError("ALTER TABLE t ALTER COLUMN v SET NOT NULL", referenceDiagnostic{Code: "23502", Message: `column "v" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "v"})
	bad.errorStage = "execute"
	p.step(t, bad)
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(50)}, {int64(6), nil}}))
}

func TestReferenceDeleteCoreZeroWidth(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE zero(v INT)", 0))
			p.step(t, referenceExec("INSERT INTO zero VALUES(1),(1),(2)", 3))
			p.step(t, referenceExec("ALTER TABLE zero DROP COLUMN v", 0))
			before := referenceSelect("SELECT * FROM zero", []string{}, []string{}, [][]any{{}, {}, {}})
			before.bag = true
			p.step(t, before)
			deletion := referenceExec("DELETE FROM zero", 3)
			deletion.prepared = prepared
			p.step(t, deletion)
			empty := referenceSelect("SELECT * FROM zero", []string{}, []string{}, [][]any{})
			empty.bag = true
			p.step(t, empty)
			deletion.affected = 0
			p.step(t, deletion)
			p.step(t, empty)
		})
	}
}
