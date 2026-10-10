package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
)

func TestReferenceMergeInsertTransactions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, prepared)
				run(referenceExec("CREATE TABLE t(id INT NOT NULL CONSTRAINT t_pkey UNIQUE)", 0))
				run(referenceExec("INSERT INTO t VALUES(99)", 1))
				tx, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				own := func(s referenceStep) {
					if strings.HasPrefix(s.query, "MERGE") {
						s.prepared = prepared
						if b.pg {
							s.affected = 0
						}
					}
					insertSelectRun(t, p.ctx, tx, b.recorder, s)
				}
				own(referenceExec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 1))
				read := func(values [][]any) referenceStep {
					return referenceSelect("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, values)
				}
				own(read([][]any{{int64(1)}, {int64(99)}}))
				run(read([][]any{{int64(99)}}))
				own(referenceExec("SAVEPOINT keep", 0))
				d := referenceDiagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "t_pkey"`, Detail: "Key (id)=(2) already exists.", Schema: "public", Table: "t", Constraint: "t_pkey"}
				s := referenceImplicitError("MERGE INTO t USING (VALUES(2),(2)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", d)
				s.errorStage = "execute"
				own(s)
				own(referenceImplicitError("MERGE INTO t USING (VALUES(3)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				own(referenceExec("ROLLBACK TO SAVEPOINT keep", 0))
				own(read([][]any{{int64(1)}, {int64(99)}}))
				own(referenceExec("MERGE INTO t USING (VALUES(3)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 1))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				want := [][]any{{int64(99)}}
				if commit {
					want = [][]any{{int64(1)}, {int64(3)}, {int64(99)}}
				}
				run(read(want))
			})
		})
	}
}

func TestReferenceMergeInsertPreparedRebind(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := mergeInsertRun(t, p, b, false)
			run(referenceExec("CREATE TABLE t(id INT,v TEXT DEFAULT 'old')", 0))
			run(referenceExec("CREATE TABLE source(id INT)", 0))
			q := "MERGE INTO t USING source s ON t.id=s.id WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)"
			stmt, err := b.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			exec := func(want int64) { result, err := stmt.ExecContext(p.ctx); mergeInsertAffected(t, b, result, err, want) }
			exec(0)
			run(referenceExec("INSERT INTO source VALUES(1)", 1))
			exec(1)
			run(referenceExec("INSERT INTO source VALUES(2)", 1))
			run(referenceExec("INSERT INTO t VALUES(2,'preexisting')", 1))
			exec(0)
			run(referenceExec("ALTER TABLE t ALTER COLUMN v SET DEFAULT 'new'", 0))
			run(referenceExec("INSERT INTO source VALUES(3)", 1))
			exec(1)
			run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "preexisting"}, {int64(3), "new"}}))
			run(referenceExec("ALTER TABLE source RENAME COLUMN id TO renamed", 0))
			result, err := stmt.ExecContext(p.ctx)
			if result != nil {
				t.Fatal("failed held MERGE exposed result")
			}
			d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: "column s.id does not exist", Hint: `Perhaps you meant to reference the column "t.id".`, Position: referencePosition(strings.Index(q, "s.id") + 1)}
			referenceAssertDiagnostic(t, err, &d)
			run(referenceExec("ALTER TABLE source RENAME COLUMN renamed TO id", 0))
			exec(0)
		})
	}
}

func TestReferenceMergeInsertCommandMetadata(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
		run := mergeInsertRun(t, p, b, prepared)
		run(referenceExec("CREATE TABLE t(id INT,v TEXT)", 0))
		q := "MERGE INTO t USING (VALUES($1::integer)) s(id) ON t.id=s.id WHEN NOT MATCHED AND s.id=$1 THEN INSERT VALUES(s.id,'typed')"
		run(referenceExec(q, 1, int64(1)))
		command := referenceSelect(q, []string{}, []string{}, [][]any{}, int64(1))
		command.prepared = prepared
		run(command)
		run(referenceSelect("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "typed"}}))
		if !b.pg {
			conn, err := p.native.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				stmt, err := raw.(driver.Conn).Prepare(q)
				if err != nil {
					return err
				}
				defer stmt.Close()
				if stmt.NumInput() != 1 {
					t.Fatalf("MERGE NumInput=%d", stmt.NumInput())
				}
				result, err := stmt.Exec([]driver.Value{int64(2)})
				insertSelectAffected(t, result, err, 1)
				result, err = stmt.Exec([]driver.Value{int64(2)})
				insertSelectAffected(t, result, err, 0)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "typed"}, {int64(2), "typed"}}))
		}
	})
}

func TestReferenceMergeInsertReadOnly(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
		run := mergeInsertRun(t, p, b, prepared)
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		for _, q := range []string{
			"MERGE INTO t USING (SELECT 2 AS id WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
			"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)",
			"MERGE INTO t USING (VALUES(2)) s(id) ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT VALUES(s.id)",
		} {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("BEGIN READ ONLY", 0))
			s := referenceImplicitError(q, referenceDiagnostic{Code: "25006", Message: "cannot execute MERGE in a read-only transaction"})
			s.prepared, s.errorStage = prepared, "execute"
			insertSelectRun(t, p.ctx, c, b.recorder, s)
			insertSelectRun(t, p.ctx, c, b.recorder, referenceExec("ROLLBACK", 0))
			c.Close()
		}
		run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceMergeInsertCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := mergeInsertRun(t, p, b, false)
			run(referenceExec("CREATE TABLE t(id INT)", 0))
			run(referenceExec("CREATE TABLE s(id INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(99)", 1))
			run(referenceExec("INSERT INTO s VALUES(1)", 1))
			q := "MERGE INTO t USING s ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
			if !b.pg {
				stmt, err := b.db.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				result, err := b.db.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("direct cancellation result=%v err=%v", result, err)
				}
				result, err = stmt.ExecContext(ctx)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("held cancellation result=%v err=%v", result, err)
				}
			} else {
				for _, prepared := range []bool{false, true} {
					c, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					var exec func(context.Context) relationOutcome
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						exec = func(ctx context.Context) relationOutcome {
							r, e := stmt.ExecContext(ctx)
							return relationOutcome{result: r, err: e}
						}
					} else {
						exec = func(ctx context.Context) relationOutcome {
							r, e := c.ExecContext(ctx, q)
							return relationOutcome{result: r, err: e}
						}
					}
					holder, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					insertSelectRun(t, p.ctx, holder, b.recorder, referenceExec("LOCK TABLE s IN ACCESS EXCLUSIVE MODE", 0))
					pid := referenceLockPID(t, p.ctx, c)
					join := referenceLockOperation(t, p.ctx, exec)
					referenceLockWait(t, p.ctx, pid)
					var canceled bool
					if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
						t.Fatalf("server cancel=%v %v", canceled, err)
					}
					o := join()
					if o.result != nil {
						t.Fatal("canceled MERGE exposed result")
					}
					d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
					referenceAssertDiagnostic(t, o.err, &d)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
			}
			run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			run(referenceExec(q, 1))
		})
	}
}
