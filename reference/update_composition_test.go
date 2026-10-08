package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceUpdateCoreComposition(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "SET bytea_output=hex"); err != nil {
					t.Error(err)
				}
			}()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			// DATE's native string and pq civil time are separately specified. The
			// existing reference recorder represents exported BYTEA as raw strings;
			// expected strings below are independent byte literals, not output encoding.
			var leap any = "2024-02-29"
			if backend.pg {
				leap = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			}
			run(referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,srcd DATE,srcb BYTEA,n INT NOT NULL,f BOOL)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2000-01-01','\x0102','2024-02-29','\x005cff',1,TRUE),(2,'2000-01-02','\x03',NULL,NULL,1,NULL)`, 2))
			names := []string{"id", "d", "b", "n", "f"}
			types := []string{"INT4", "DATE", "BYTEA", "INT4", "BOOL"}
			check := func(date, blob any, flag1, flag2 bool) {
				updateCoreByteRows(t, p.ctx, c, "SELECT b FROM t ORDER BY id", [][][]byte{{{0, 92, 255}}, {nil}})
				run(referenceSelect("SELECT id,d,b,n,f FROM t ORDER BY id", names, types, [][]any{{int64(1), date, blob, int64(1), flag1}, {int64(2), nil, nil, int64(1), flag2}}))
			}
			run(referenceExec("UPDATE t SET d=srcd,b=srcb,f=d IS NOT NULL WHERE b IS DISTINCT FROM srcb", 2))
			check(leap, "\x00\\\xff", true, true)
			old, err := c.PrepareContext(p.ctx, "UPDATE t SET d=srcd,b=srcb,f=d IS NOT NULL")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			for range 2 {
				result, err := old.ExecContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, result, 2)
				check(leap, "\x00\\\xff", true, false)
			}
			for _, prepared := range []bool{false, true} {
				step := referenceExec("UPDATE t SET n=CAST(n AS BIGINT)::INTEGER,f=(b IS NULL) IS FALSE WHERE d IS NOT DISTINCT FROM srcd", 2)
				step.prepared = prepared
				run(step)
				check(leap, "\x00\\\xff", true, false)
			}
			run(referenceExec("UPDATE t SET f=(d IS NULL) IS TRUE WHERE f IS NOT UNKNOWN", 2))
			check(leap, "\x00\\\xff", false, true)
		})
	}
}

func TestReferenceUpdateCoreCompositionAtomicNotNull(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,n INT NOT NULL,nextn INT)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES(1,'2000-01-01','\x01',1,2),(2,'2000-01-01','\x02',1,NULL)`, 2))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, step) }
			run(referenceExec("SET bytea_output=hex", 0))
			q := "UPDATE t SET d=$1,b=$2,n=nextn"
			held, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			var date any = "2000-01-01"
			if backend.pg {
				date = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			}
			check := func() {
				updateCoreByteRows(t, p.ctx, c, "SELECT b FROM t ORDER BY id", [][][]byte{{{1}}, {{2}}})
				run(referenceSelect("SELECT id,d,b,n,nextn FROM t ORDER BY id", []string{"id", "d", "b", "n", "nextn"}, []string{"INT4", "DATE", "BYTEA", "INT4", "INT4"}, [][]any{{int64(1), date, "\x01", int64(1), int64(2)}, {int64(2), date, "\x02", int64(1), nil}}))
			}
			for _, prepared := range []bool{false, true} {
				for _, explicit := range []bool{false, true} {
					check()
					if explicit {
						run(referenceExec("BEGIN", 0))
						run(referenceExec("UPDATE t SET n=7 WHERE id=1", 1))
					}
					var result sql.Result
					var err error
					if prepared {
						result, err = held.ExecContext(p.ctx, "2024-02-29", []byte{0, 92, 255})
					} else {
						result, err = c.ExecContext(p.ctx, q, "2024-02-29", []byte{0, 92, 255})
					}
					if result != nil {
						t.Fatal("atomic failure returned result")
					}
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: `Failing row contains (2, 2024-02-29, \x005cff, null, null).`, Schema: "public", Table: "t", Column: "n"})
					if explicit {
						aborted := referenceImplicitError("SELECT n FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						aborted.selectRows = true
						run(aborted)
						run(referenceExec("ROLLBACK", 0))
					}
					check()
				}
			}
		})
	}
}

// Direct and retained prepared arithmetic mutations preserve independent full rows.
func TestReferenceUpdateCoreCompositionArithmetic(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, step) }
				run(referenceExec("CREATE TABLE t(id INT,a INT,b INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1,7,2),(1,7,2),(2,NULL,4)", 3))
				old, err := backend.db.QueryContext(p.ctx, "SELECT id,a,b FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				q := "UPDATE t SET a=a+b,b=a-b WHERE id+$1=$2"
				execute := func(args ...any) (sql.Result, error) { return c.ExecContext(p.ctx, q, args...) }
				if prepared {
					held, err := c.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { old.Close(); held.Close() }()
					execute = func(args ...any) (sql.Result, error) { return held.ExecContext(p.ctx, args...) }
				}
				for _, frame := range []struct {
					args []any
					n    int64
					rows [][]any
				}{
					{[]any{1, 2}, 2, [][]any{{int64(1), int64(9), int64(5)}, {int64(1), int64(9), int64(5)}, {int64(2), nil, int64(4)}}},
					{[]any{nil, 2}, 0, [][]any{{int64(1), int64(9), int64(5)}, {int64(1), int64(9), int64(5)}, {int64(2), nil, int64(4)}}},
					{[]any{0, 1}, 2, [][]any{{int64(1), int64(14), int64(4)}, {int64(1), int64(14), int64(4)}, {int64(2), nil, int64(4)}}},
					{[]any{0, 2}, 1, [][]any{{int64(1), int64(14), int64(4)}, {int64(1), int64(14), int64(4)}, {int64(2), nil, nil}}},
				} {
					result, err := execute(frame.args...)
					if err != nil {
						t.Fatal(err)
					}
					referenceLockAffected(t, result, frame.n)
					for _, readPrepared := range []bool{false, true} {
						step := referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, frame.rows)
						step.prepared = readPrepared
						step.bag = true
						run(step)
					}
				}
				for _, readPrepared := range []bool{false, true} {
					step := referenceSelect("SELECT id,a,b FROM t WHERE FALSE", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{})
					step.prepared = readPrepared
					run(step)
				}
				updateCoreRetained(t, backend.pg, old, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(2)}, {int64(1), int64(7), int64(2)}, {int64(2), nil, int64(4)}}, true)
			}
		})
	}
}

func TestReferenceUpdateCoreCompositionArithmeticAtomicity(t *testing.T) {
	for _, tc := range []struct {
		input, q, code, message string
		rows                    [][]any
	}{
		{"(1,7,2),(2,0,4),(3,NULL,6)", "UPDATE t SET a=14/a,b=b+1", "22012", "division by zero", [][]any{{int64(1), int64(7), int64(2)}, {int64(2), int64(0), int64(4)}, {int64(3), nil, int64(6)}}},
		{"(1,1,2),(2,2147483647,4)", "UPDATE t SET a=a+1,b=b+1", "22003", "integer out of range", [][]any{{int64(1), int64(1), int64(2)}, {int64(2), int64(2147483647), int64(4)}}},
	} {
		for _, prepared := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(tc.code+map[bool]string{false: "/direct", true: "/prepared"}[prepared]+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							if _, err := c.ExecContext(ctx, "ROLLBACK"); err != nil {
								t.Error(err)
							}
						}()
						run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, step) }
						run(referenceExec("CREATE TABLE t(id INT,a INT,b INT)", 0))
						run(referenceExec("INSERT INTO t VALUES"+tc.input, int64(len(tc.rows))))
						execute := func() (sql.Result, error) { return c.ExecContext(p.ctx, tc.q) }
						if prepared {
							held, err := c.PrepareContext(p.ctx, tc.q)
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							execute = func() (sql.Result, error) { return held.ExecContext(p.ctx) }
						}
						if explicit {
							run(referenceExec("BEGIN", 0))
							run(referenceExec("UPDATE t SET b=99 WHERE id=1", 1))
						}
						result, err := execute()
						if result != nil {
							t.Fatal("failed UPDATE returned a result")
						}
						referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message})
						if explicit {
							run(referenceImplicitError("UPDATE t SET b=0", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
							run(referenceExec("ROLLBACK", 0))
						}
						for _, readPrepared := range []bool{false, true} {
							step := referenceSelect("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, tc.rows)
							step.prepared = readPrepared
							run(step)
						}
					}
				})
			}
		}
	}
}
