package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceBtreeStreams(t *testing.T, p *fixture.Pair, fn func(*testing.T, *sql.DB, *fixture.Recorder)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { fn(t, backend.DB, backend.Recorder) })
	}
}
func TestReferenceBtreeIndexMaintenance(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		var inserted *sql.Stmt
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0),
			fixture.Exec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
			fixture.Exec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
			fixture.Exec("CREATE INDEX si ON t USING btree(s)", 0),
			fixture.Prepared(fixture.Exec("CREATE INDEX si_prepared ON t USING btree(s)", 0)),
			fixture.Exec("INSERT INTO t VALUES(2,'b')", 1),
			fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, 0, "")),
			fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1,$2)", 1, nil, nil)),
			fixture.Exec("INSERT INTO t SELECT n,s FROM t WHERE n=3", 1),
			fixture.Select("SELECT n FROM t ORDER BY n ASC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {nil}, {nil}}),
			fixture.Select("SELECT n FROM t ORDER BY n DESC NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(3)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(1)}, {int64(0)}, {nil}, {nil}}),
			fixture.Select("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}}),
			fixture.Exec("UPDATE t SET n=10 WHERE n=1", 1),
			fixture.Exec("DELETE FROM t WHERE n=3", 2),
			fixture.Select("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}}),
		} {
			if step.Query == "INSERT INTO t VALUES($1,$2)" {
				if inserted == nil {
					var err error
					inserted, err = db.PrepareContext(p.Ctx, step.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer inserted.Close()
				}
				step.Prepared = false
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{db, inserted, step.Query}, recorder, step)
			} else {
				if step.Query == "UPDATE t SET n=10 WHERE n=1" {
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}}))
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
						pre := fixture.Prepared(fixture.Select(tc.q, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.want, tc.arg))
						pre.Bag = true
						fixture.RunStep(t, p.Ctx, db, recorder, pre)
					}
				}
				fixture.RunStep(t, p.Ctx, db, recorder, step)
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
			step := fixture.Prepared(fixture.Select("SELECT n FROM t WHERE "+tc.predicate, []string{"n"}, []string{"INT4"}, tc.rows, args...))
			step.Bag = true
			fixture.RunStep(t, p.Ctx, db, recorder, step)
		}
	})
}
func TestReferenceBtreeIndexDiagnostics(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonContext", true: "context"}[contextual], func(t *testing.T) {
			p := fixture.NewScenario(t)
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
				for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE t(n INT)", 0), fixture.Exec("CREATE SEQUENCE seq", 0), fixture.Exec("CREATE VIEW v AS SELECT n FROM t", 0), fixture.Exec("CREATE INDEX idx ON t(n)", 0)} {
					if contextual {
						fixture.RunStep(t, p.Ctx, db, recorder, step)
					} else {
						result, err := db.Exec(step.Query)
						fixture.DeleteReferenceCount(t, result, err, step.Affected)
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
					step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
					step.ErrorStage = "execute"
					if contextual {
						fixture.RunStep(t, p.Ctx, db, recorder, step)
						fixture.RunStep(t, p.Ctx, db, recorder, fixture.Prepared(step))
					} else {
						result, err := db.Exec(tc.query)
						if result != nil {
							t.Fatal("failed Exec exposed a result")
						}
						fixture.AssertDiagnostic(t, err, step.Diagnostic)
						stmt, err := db.Prepare(tc.query)
						if err != nil {
							t.Fatal("semantic error at Prepare", err)
						}
						defer stmt.Close()
						result, err = stmt.Exec()
						if result != nil {
							t.Fatal("failed prepared Exec exposed a result")
						}
						fixture.AssertDiagnostic(t, err, step.Diagnostic)
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
					}
					sentinel := fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil)
					if contextual {
						fixture.RunStep(t, p.Ctx, db, recorder, sentinel)
					} else {
						rows, err := db.Query(sentinel.Query)
						fixture.DeleteReferenceConsume(t, db == p.Postgres, rows, err, sentinel)
					}
				}
				for _, step := range []fixture.Step{
					fixture.Exec("CREATE INDEX retry ON t(n)", 0),
					fixture.Exec("CREATE INDEX ON t(n)", 0), fixture.Exec("CREATE INDEX ON t(n)", 0),
					fixture.Select("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='t_n_idx' OR relname='t_n_idx1' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"t_n_idx", "i", int64(1)}, {"t_n_idx1", "i", int64(1)}}),
				} {
					if contextual {
						fixture.RunStep(t, p.Ctx, db, recorder, step)
					} else if step.SelectRows {
						rows, err := db.Query(step.Query)
						fixture.DeleteReferenceConsume(t, db == p.Postgres, rows, err, step)
					} else {
						result, err := db.Exec(step.Query)
						fixture.DeleteReferenceCount(t, result, err, step.Affected)
					}
				}
				const laterQuery = "CREATE INDEX held ON later(n)"
				var held *sql.Stmt
				var err error
				if contextual {
					held, err = db.PrepareContext(p.Ctx, laterQuery)
				} else {
					held, err = db.Prepare(laterQuery)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				if contextual {
					executor := fixture.HeldStatement{db, held, laterQuery}
					step := fixture.ImplicitError(laterQuery, fixture.Diagnostic{Code: "42P01", Message: `relation "later" does not exist`})
					step.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, executor, recorder, step)
					fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec("CREATE TABLE later(n INT)", 0))
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec(laterQuery, 0))
				} else {
					result, err := held.Exec()
					if result != nil {
						t.Fatal("failed held Exec exposed a result")
					}
					want := fixture.ControlsReferenceDiagnostic("42P01", `relation "later" does not exist`)
					fixture.AssertDiagnostic(t, err, &want)
					result, err = db.Exec("CREATE TABLE later(n INT)")
					fixture.DeleteReferenceCount(t, result, err, 0)
					result, err = held.Exec()
					fixture.DeleteReferenceCount(t, result, err, 0)
				}
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT relname,relnatts FROM pg_catalog.pg_class WHERE relname='held'", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"held", int64(1)}}))
			})
		})
	}
}
func TestReferenceBtreeIndexControls(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonContext", true: "context"}[contextual], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, step := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0),
				fixture.Exec("INSERT INTO t VALUES(2,'b'),(NULL,NULL),(2,'b')", 3),
				fixture.Exec("CREATE INDEX idx ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
				fixture.Exec("ALTER TABLE t RENAME n TO key", 0), fixture.Exec("ALTER TABLE t RENAME TO renamed", 0),
			} {
				for _, b := range p.Backends() {
					if contextual {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
					} else {
						result, err := b.DB.Exec(step.Query)
						fixture.DeleteReferenceCount(t, result, err, step.Affected)
					}
				}
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(dump.String(), `CREATE INDEX "idx" ON "public"."renamed" USING btree ("key" DESC NULLS LAST, "s" ASC NULLS FIRST);`) {
				t.Fatal(dump.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			absent := fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil)
			if contextual {
				fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, absent)
			} else {
				rows, err := p.Memory.Query(absent.Query)
				fixture.DeleteReferenceConsume(t, false, rows, err, absent)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			if p.Postgres != nil {
				result, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE renamed")
				fixture.DeleteReferenceCount(t, result, err, 0)
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("real PostgreSQL dump replay", err)
				}
			}
			for _, step := range []fixture.Step{
				fixture.Exec("INSERT INTO renamed VALUES(3,'c')", 1),
				fixture.Select("SELECT key,s FROM renamed ORDER BY key DESC NULLS LAST", []string{"key", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {nil, nil}}),
				fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}),
			} {
				if contextual {
					p.Step(t, step)
					continue
				}
				for _, b := range p.Backends() {
					if step.SelectRows {
						rows, err := b.DB.Query(step.Query)
						fixture.DeleteReferenceConsume(t, b.PG, rows, err, step)
					} else {
						result, err := b.DB.Exec(step.Query)
						fixture.DeleteReferenceCount(t, result, err, step.Affected)
					}
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE INDEX broken ON renamed(missing);")); err == nil {
				t.Fatal("bad late index replaced destination")
			}
			for _, step := range []fixture.Step{
				fixture.Select("SELECT key FROM renamed ORDER BY key DESC NULLS LAST", []string{"key"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(2)}, {nil}}),
				fixture.Select("SELECT relkind FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}),
			} {
				if contextual {
					p.Step(t, step)
					continue
				}
				for _, b := range p.Backends() {
					rows, err := b.DB.Query(step.Query)
					fixture.DeleteReferenceConsume(t, b.PG, rows, err, step)
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
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(n INT)", 0))
			referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
				tx, err := db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if db == p.Memory {
					step := fixture.Prepared(fixture.ImplicitError(head+" idx ON t(n)", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
					step.ErrorStage = "execute"
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, recorder, step)
				} else {
					step := fixture.Exec(head+" idx ON t(n)", 0)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, recorder, step)
					fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
				fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil))
				tx, err = db.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, recorder, fixture.Exec("SET TRANSACTION READ ONLY", 0))
				sqlReadOnly := fixture.ImplicitError(head+" idx ON missing(n)", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"})
				sqlReadOnly.Prepared = prepared
				sqlReadOnly.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, tx, recorder, sqlReadOnly)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				tx, err = db.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				step := fixture.Prepared(fixture.ImplicitError(head+" idx ON missing(n)", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"}))
				step.ErrorStage = "execute"
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, recorder, step)
			})

		})
	}
}

// Preserve the original non-Context consumers alongside the recorded lanes.
func TestReferenceBtreeIndexMaintenanceNonContext(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0),
				fixture.Exec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
				fixture.Exec("CREATE INDEX ni ON t(n DESC NULLS LAST,s ASC NULLS FIRST)", 0),
				fixture.Exec("CREATE INDEX si ON t USING btree(s)", 0),
				fixture.Exec("INSERT INTO t VALUES(2,'b')", 1),
			} {
				result, err := b.DB.Exec(s.Query)
				fixture.DeleteReferenceCount(t, result, err, s.Affected)
			}
			stmt, err := b.DB.Prepare("INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, args := range [][]any{{0, ""}, {nil, nil}} {
				result, err := stmt.Exec(args...)
				fixture.DeleteReferenceCount(t, result, err, 1)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			result, err := b.DB.Exec("INSERT INTO t SELECT n,s FROM t WHERE n=3")
			fixture.DeleteReferenceCount(t, result, err, 1)
			s := fixture.Select("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), ""}, {int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {int64(3), "c"}, {nil, nil}, {nil, nil}})
			rows, err := b.DB.Query(s.Query)
			fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
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
				held, err := b.DB.Prepare(tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s := fixture.Select(tc.q, []string{"n", "s"}, []string{"INT4", "TEXT"}, tc.want)
				s.Bag = true
				rows, err := held.Query(tc.arg)
				fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
				if err := held.Close(); err != nil {
					t.Fatal(err)
				}
			}
			result, err = b.DB.Exec("UPDATE t SET n=10 WHERE n=1")
			fixture.DeleteReferenceCount(t, result, err, 1)
			result, err = b.DB.Exec("DELETE FROM t WHERE n=3")
			fixture.DeleteReferenceCount(t, result, err, 2)
			s = fixture.Select("SELECT n FROM t ORDER BY n ASC NULLS FIRST", []string{"n"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(0)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(10)}})
			rows, err = b.DB.Query(s.Query)
			fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
		})
	}
}

func TestReferenceBtreeIndexTransactionNonContext(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			result, err := b.DB.Exec("CREATE TABLE t(n INT)")
			fixture.DeleteReferenceCount(t, result, err, 0)
			for _, readOnly := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					tx, err := b.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if readOnly {
						result, err := tx.Exec("SET TRANSACTION READ ONLY")
						fixture.DeleteReferenceCount(t, result, err, 0)
					}
					q := "CREATE INDEX idx ON t(n)"
					want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
					if readOnly {
						q = "CREATE INDEX idx ON missing(n)"
						want = fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
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
					if b.PG && !readOnly {
						fixture.DeleteReferenceCount(t, result, err, 0)
						s := fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}})
						rows, err := tx.Query(s.Query)
						fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
					} else {
						if result != nil {
							t.Fatal("failed transaction Exec exposed a result")
						}
						fixture.AssertDiagnostic(t, err, &want)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					for _, s := range []fixture.Step{
						fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil),
						fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil),
					} {
						rows, err := b.DB.Query(s.Query)
						fixture.DeleteReferenceConsume(t, b.PG, rows, err, s)
					}
				}
			}
		})
	}
}
