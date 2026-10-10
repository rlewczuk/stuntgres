package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func referenceBtreeStreams(t *testing.T, p *referencePair, fn func(*testing.T, *sql.DB, *referenceRecorder)) {
	t.Helper()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { fn(t, backend.db, backend.recorder) })
	}
}
func TestReferenceBtreeIndexMaintenance(t *testing.T) {
	p := newReferenceScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		var inserted *sql.Stmt
		for _, step := range []referenceStep{
			referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
			referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
			referenceExec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
			referenceExec("CREATE INDEX si ON t USING btree(s)", 0),
			referencePrepared(referenceExec("CREATE INDEX si_prepared ON t USING btree(s)", 0)),
			referenceExec("INSERT INTO t VALUES(2,'b')", 1),
			referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, 0, "")),
			referencePrepared(referenceExec("INSERT INTO t VALUES($1,$2)", 1, nil, nil)),
			referenceExec("INSERT INTO t SELECT n,s FROM t WHERE n=3", 1),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {nil}, {nil}}),
			referenceSelect("SELECT n FROM t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(1)}, {int64(0)}, {nil}, {nil}}),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}),
			referenceExec("UPDATE t SET n=10 WHERE n=1", 1),
			referenceExec("DELETE FROM t WHERE n=3", 2),
			referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}),
		} {
			if step.query == "INSERT INTO t VALUES($1,$2)" {
				if inserted == nil {
					var err error
					inserted, err = db.PrepareContext(p.ctx, step.query)
					if err != nil {
						t.Fatal(err)
					}
					defer inserted.Close()
				}
				step.prepared = false
				referenceRunStep(t, p.ctx, referenceHeldStatement{db, inserted, step.query}, recorder, step)
			} else {
				if step.query == "UPDATE t SET n=10 WHERE n=1" {
					referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}))
					for _, tc := range []struct {
						q    string
						arg  any
						want [][]any
					}{
						{"SELECT n,s FROM t WHERE n<$1", 2, [][]any{{int64(0), ""}, {int64(1), "a"}}},
						{"SELECT n,s FROM t WHERE n<=$1", 1, [][]any{{int64(0), ""}, {int64(1), "a"}}},
						{"SELECT n,s FROM t WHERE n>$1", 2, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
						{"SELECT n,s FROM t WHERE n>=$1", 3, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
						{"SELECT n,s FROM t WHERE n=$1", 9, nil},
					} {
						pre := referencePrepared(referenceSelect(tc.q, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.want, tc.arg))
						pre.bag = true
						referenceRunStep(t, p.ctx, db, recorder, pre)
					}
				}
				referenceRunStep(t, p.ctx, db, recorder, step)
			}
		}
		for _, tc := range []struct {
			predicate string
			boundary  any
			rows      [][]any
		}{
			{"n<$1", 2, [][]any{{int64(0)}}}, {"n<=$1", 0, [][]any{{int64(0)}}},
			{"n>$1", 2, [][]any{{int64(10)}}}, {"n>=$1", 10, [][]any{{int64(10)}}},
			{"n=$1", 99, nil}, {"n=$1", nil, nil},
			{"n IS NOT NULL", nil, [][]any{{int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}},
		} {
			args := []any{tc.boundary}
			if tc.predicate == "n IS NOT NULL" {
				args = nil
			}
			step := referencePrepared(referenceSelect("SELECT n FROM t WHERE "+tc.predicate, []string{"n"}, []string{"INT4"}, tc.rows, args...))
			step.bag = true
			referenceRunStep(t, p.ctx, db, recorder, step)
		}
	})
}
func TestReferenceBtreeIndexDiagnostics(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonContext", true: "context"}[contextual], func(t *testing.T) {
			p := newReferenceScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
				for _, step := range []referenceStep{referenceExec("CREATE TABLE t(n INT)", 0), referenceExec("CREATE SEQUENCE seq", 0), referenceExec("CREATE VIEW v AS SELECT n FROM t", 0), referenceExec("CREATE INDEX idx ON t(n)", 0)} {
					if contextual {
						referenceRunStep(t, p.ctx, db, recorder, step)
					} else {
						result, err := db.Exec(step.query)
						deleteReferenceCount(t, result, err, step.affected)
					}
				}
				for _, tc := range []struct{ query, code, message, detail string }{
					{"CREATE INDEX retry ON public.missing(n)", "42P01", `relation "public.missing" does not exist`, ""},
					{"CREATE INDEX retry ON absent.t(n)", "3F000", `schema "absent" does not exist`, ""},
					{"CREATE INDEX idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
					{"CREATE INDEX IF NOT EXISTS idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
					{"CREATE INDEX retry ON seq(n)", "42809", `cannot create index on relation "seq"`, "This operation is not supported for sequences."},
					{"CREATE INDEX retry ON v(n)", "42809", `cannot create index on relation "v"`, "This operation is not supported for views."},
					{"CREATE INDEX retry ON idx(n)", "42809", `cannot open relation "idx"`, "This operation is not supported for indexes."},
					{"CREATE INDEX retry ON t USING absent(n)", "42704", `access method "absent" does not exist`, ""},
					{"CREATE INDEX retry ON t(ctid)", "0A000", "index creation on system columns is not supported", ""},
					{"CREATE INDEX retry ON t(" + strings.TrimSuffix(strings.Repeat("n,", 33), ",") + ")", "54011", "cannot use more than 32 columns in an index", ""},
				} {
					step := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
					step.errorStage = "execute"
					if contextual {
						referenceRunStep(t, p.ctx, db, recorder, step)
						referenceRunStep(t, p.ctx, db, recorder, referencePrepared(step))
					} else {
						result, err := db.Exec(tc.query)
						if result != nil {
							t.Fatal("failed Exec exposed a result")
						}
						referenceAssertDiagnostic(t, err, step.diagnostic)
						stmt, err := db.Prepare(tc.query)
						if err != nil {
							t.Fatal("semantic error at Prepare", err)
						}
						defer stmt.Close()
						result, err = stmt.Exec()
						if result != nil {
							t.Fatal("failed prepared Exec exposed a result")
						}
						referenceAssertDiagnostic(t, err, step.diagnostic)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					sentinel := referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil)
					if contextual {
						referenceRunStep(t, p.ctx, db, recorder, sentinel)
					} else {
						rows, err := db.Query(sentinel.query)
						deleteReferenceConsume(t, db == p.postgres, rows, err, sentinel)
					}
				}
				for _, step := range []referenceStep{
					referenceExec("CREATE INDEX retry ON t(n)", 0),
					referenceExec("CREATE INDEX ON t(n)", 0), referenceExec("CREATE INDEX ON t(n)", 0),
					referenceSelect("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='t_n_idx' OR relname='t_n_idx1' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"t_n_idx", "i", int64(1)}, {"t_n_idx1", "i", int64(1)}}),
				} {
					if contextual {
						referenceRunStep(t, p.ctx, db, recorder, step)
					} else if step.selectRows {
						rows, err := db.Query(step.query)
						deleteReferenceConsume(t, db == p.postgres, rows, err, step)
					} else {
						result, err := db.Exec(step.query)
						deleteReferenceCount(t, result, err, step.affected)
					}
				}
				const laterQuery = "CREATE INDEX held ON later(n)"
				var held *sql.Stmt
				var err error
				if contextual {
					held, err = db.PrepareContext(p.ctx, laterQuery)
				} else {
					held, err = db.Prepare(laterQuery)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				if contextual {
					executor := referenceHeldStatement{db, held, laterQuery}
					step := referenceImplicitError(laterQuery, referenceDiagnostic{Code: "42P01", Message: `relation "later" does not exist`})
					step.errorStage = "execute"
					referenceRunStep(t, p.ctx, executor, recorder, step)
					referenceRunStep(t, p.ctx, db, recorder, referenceExec("CREATE TABLE later(n INT)", 0))
					referenceRunStep(t, p.ctx, executor, recorder, referenceExec(laterQuery, 0))
				} else {
					result, err := held.Exec()
					if result != nil {
						t.Fatal("failed held Exec exposed a result")
					}
					want := controlsReferenceDiagnostic("42P01", `relation "later" does not exist`)
					referenceAssertDiagnostic(t, err, &want)
					result, err = db.Exec("CREATE TABLE later(n INT)")
					deleteReferenceCount(t, result, err, 0)
					result, err = held.Exec()
					deleteReferenceCount(t, result, err, 0)
				}
				referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname,relnatts FROM pg_catalog.pg_class WHERE relname='held'", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"held", int64(1)}}))
			})
		})
	}
}
func TestReferenceBtreeIndexControls(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonContext", true: "context"}[contextual], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, step := range []referenceStep{
				referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
				referenceExec("INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3),
				referenceExec("CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
				referenceExec("ALTER TABLE t RENAME n TO key", 0), referenceExec("ALTER TABLE t RENAME TO renamed", 0),
			} {
				for _, b := range p.backends() {
					if contextual {
						referenceRunStep(t, p.ctx, b.db, b.recorder, step)
					} else {
						result, err := b.db.Exec(step.query)
						deleteReferenceCount(t, result, err, step.affected)
					}
				}
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), `CREATE INDEX "idx" ON "public"."renamed" USING btree ("key" DESC NULLS LAST, "s" ASC NULLS FIRST);`) {
				t.Fatal(dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			absent := referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil)
			if contextual {
				referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, absent)
			} else {
				rows, err := p.memory.Query(absent.query)
				deleteReferenceConsume(t, false, rows, err, absent)
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			if p.postgres != nil {
				result, err := p.postgres.ExecContext(p.ctx, "DROP TABLE renamed")
				deleteReferenceCount(t, result, err, 0)
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal("real PostgreSQL dump replay", err)
				}
			}
			for _, step := range []referenceStep{
				referenceExec("INSERT INTO renamed VALUES(3,'c')", 1),
				referenceSelect("SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", []string{"key", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}}),
				referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}),
			} {
				if contextual {
					p.step(t, step)
					continue
				}
				for _, b := range p.backends() {
					if step.selectRows {
						rows, err := b.db.Query(step.query)
						deleteReferenceConsume(t, b.pg, rows, err, step)
					} else {
						result, err := b.db.Exec(step.query)
						deleteReferenceCount(t, result, err, step.affected)
					}
				}
			}
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE INDEX broken ON renamed(missing);")); err == nil {
				t.Fatal("bad late index replaced destination")
			}
			for _, step := range []referenceStep{
				referenceSelect("SELECT key FROM renamed ORDER BY key DESC NULLS LAST", []string{"key"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}),
				referenceSelect("SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}),
			} {
				if contextual {
					p.step(t, step)
					continue
				}
				for _, b := range p.backends() {
					rows, err := b.db.Query(step.query)
					deleteReferenceConsume(t, b.pg, rows, err, step)
				}
			}
		})
	}
}
func TestReferenceBtreeIndexTransactionDistinction(t *testing.T) {
	referenceIndexTransactionDistinction(t, false)
}

func TestReferenceUniqueIndexTransactionDistinction(t *testing.T) {
	referenceIndexTransactionDistinction(t, true)
}

func referenceIndexTransactionDistinction(t *testing.T, unique bool) {
	head := "CREATE INDEX"
	if unique {
		head = "CREATE UNIQUE INDEX"
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(n INT)", 0))
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
				tx, err := db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if db == p.memory {
					step := referencePrepared(referenceImplicitError(head+" idx ON t(n)", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
					step.errorStage = "execute"
					step.prepared = prepared
					referenceRunStep(t, p.ctx, tx, recorder, step)
				} else {
					step := referenceExec(head+" idx ON t(n)", 0)
					step.prepared = prepared
					referenceRunStep(t, p.ctx, tx, recorder, step)
					referenceRunStep(t, p.ctx, tx, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
				referenceRunStep(t, p.ctx, db, recorder, referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil))
				tx, err = db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, recorder, referenceExec("SET TRANSACTION READ ONLY", 0))
				sqlReadOnly := referenceImplicitError(head+" idx ON missing(n)", referenceDiagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"})
				sqlReadOnly.prepared = prepared
				sqlReadOnly.errorStage = "execute"
				referenceRunStep(t, p.ctx, tx, recorder, sqlReadOnly)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				tx, err = db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := referencePrepared(referenceImplicitError(head+" idx ON missing(n)", referenceDiagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}))
				step.errorStage = "execute"
				step.prepared = prepared
				referenceRunStep(t, p.ctx, tx, recorder, step)
			})

		})
	}
}

// Preserve the original non-Context consumers alongside the recorded lanes.
func TestReferenceBtreeIndexMaintenanceNonContext(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
				referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
				referenceExec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
				referenceExec("CREATE INDEX si ON t USING btree(s)", 0),
				referenceExec("INSERT INTO t VALUES(2,'b')", 1),
			} {
				result, err := b.db.Exec(s.query)
				deleteReferenceCount(t, result, err, s.affected)
			}
			stmt, err := b.db.Prepare("INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{0, ""}, {nil, nil}} {
				result, err := stmt.Exec(args...)
				deleteReferenceCount(t, result, err, 1)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := b.db.Exec("INSERT INTO t SELECT n,s FROM t WHERE n=3")
			deleteReferenceCount(t, result, err, 1)
			s := referenceSelect("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}})
			rows, err := b.db.Query(s.query)
			deleteReferenceConsume(t, b.pg, rows, err, s)
			for _, tc := range []struct {
				q    string
				arg  int
				want [][]any
			}{
				{"SELECT n,s FROM t WHERE n<$1", 2, [][]any{{int64(0), ""}, {int64(1), "a"}}},
				{"SELECT n,s FROM t WHERE n<=$1", 1, [][]any{{int64(0), ""}, {int64(1), "a"}}},
				{"SELECT n,s FROM t WHERE n>$1", 2, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
				{"SELECT n,s FROM t WHERE n>=$1", 3, [][]any{{int64(3), "c"}, {int64(3), "c"}}},
				{"SELECT n,s FROM t WHERE n=$1", 9, nil},
			} {
				held, err := b.db.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s := referenceSelect(tc.q, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.want)
				s.bag = true
				rows, err := held.Query(tc.arg)
				deleteReferenceConsume(t, b.pg, rows, err, s)
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
			result, err = b.db.Exec("UPDATE t SET n=10 WHERE n=1")
			deleteReferenceCount(t, result, err, 1)
			result, err = b.db.Exec("DELETE FROM t WHERE n=3")
			deleteReferenceCount(t, result, err, 2)
			s = referenceSelect("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}})
			rows, err = b.db.Query(s.query)
			deleteReferenceConsume(t, b.pg, rows, err, s)
		})
	}
}

func TestReferenceBtreeIndexTransactionNonContext(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			result, err := b.db.Exec("CREATE TABLE t(n INT)")
			deleteReferenceCount(t, result, err, 0)
			for _, readOnly := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					tx, err := b.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if readOnly {
						result, err := tx.Exec("SET TRANSACTION READ ONLY")
						deleteReferenceCount(t, result, err, 0)
					}
					q := "CREATE INDEX idx ON t(n)"
					want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
					if readOnly {
						q = "CREATE INDEX idx ON missing(n)"
						want = controlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
					}
					var result sql.Result
					if prepared {
						stmt, prepareErr := tx.Prepare(q)
						if prepareErr != nil {
							t.Fatal("syntax-only utility Prepare", prepareErr)
						}
						defer stmt.Close()
						result, err = stmt.Exec()
						if closeErr := stmt.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
					} else {
						result, err = tx.Exec(q)
					}
					if b.pg && !readOnly {
						deleteReferenceCount(t, result, err, 0)
						s := referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}})
						rows, err := tx.Query(s.query)
						deleteReferenceConsume(t, b.pg, rows, err, s)
					} else {
						if result != nil {
							t.Fatal("failed transaction Exec exposed a result")
						}
						referenceAssertDiagnostic(t, err, &want)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					for _, s := range []referenceStep{
						referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil),
						referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil),
					} {
						rows, err := b.db.Query(s.query)
						deleteReferenceConsume(t, b.pg, rows, err, s)
					}
				}
			}
		})
	}
}
