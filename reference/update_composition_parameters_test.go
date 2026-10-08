package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func TestReferenceUpdateCoreCompositionParameters(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			var first, second any = "2030-01-02", "2031-02-03"
			if backend.pg {
				first = time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
				second = time.Date(2031, 2, 3, 0, 0, 0, 0, time.UTC)
			}
			run(referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,n INT NOT NULL,f BOOL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1,NULL,NULL,1,FALSE),(2,NULL,NULL,1,TRUE)", 2))
			check := func(date any, blob []byte, flag1, flag2 bool) {
				updateCoreByteRows(t, p.ctx, c, "SELECT b FROM t ORDER BY id", [][][]byte{{blob}, {nil}})
				var normalized any
				if blob != nil {
					normalized = string(blob)
				}
				run(referenceSelect("SELECT id,d,b,n,f FROM t ORDER BY id", []string{"id", "d", "b", "n", "f"}, []string{"INT4", "DATE", "BYTEA", "INT4", "BOOL"}, [][]any{{int64(1), date, normalized, int64(1), flag1}, {int64(2), nil, nil, int64(1), flag2}}))
			}
			q := "UPDATE t SET d=$1,b=$2 WHERE id=$3"
			run(referenceExec(q, 1, "2030-01-02", []byte{65, 0, 255}, 1))
			check(first, []byte{65, 0, 255}, false, true)
			run(referenceExec(q, 1, nil, nil, 1))
			check(nil, nil, false, true)
			held, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			result, err := held.ExecContext(p.ctx, nil, nil, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			check(nil, nil, false, true)
			result, err = held.ExecContext(p.ctx, "2031-02-03", `\q`, 1)
			if err != nil {
				t.Fatal("BYTEA string Bind is raw bytes", err)
			}
			referenceLockAffected(t, result, 1)
			check(second, []byte{92, 113}, false, true)
			result, err = held.ExecContext(p.ctx, "2031-02-03", `\x41`, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			check(second, []byte{92, 120, 52, 49}, false, true)
			result, err = held.ExecContext(p.ctx, "2031-02-03", []byte{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			check(second, []byte{}, false, true)
			payload := []byte{9, 92, 128}
			result, err = held.ExecContext(p.ctx, "2031-02-03", payload, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			payload[0] = 99
			check(second, []byte{9, 92, 128}, false, true)
			dateError := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type date: "bad"`, Context: "unnamed portal parameter $1 = '...'"}
			for _, prepared := range []bool{false, true} {
				var result sql.Result
				if prepared {
					result, err = held.ExecContext(p.ctx, "bad", []byte{42}, 1)
				} else {
					result, err = c.ExecContext(p.ctx, q, "bad", []byte{42}, 1)
				}
				if result != nil {
					t.Fatal("failed parameter returned result")
				}
				referenceAssertDiagnostic(t, err, &dateError)
				check(second, []byte{9, 92, 128}, false, true)
			}
		})
	}
}
func TestReferenceUpdateCoreCompositionLiteralDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			var second any = "2031-02-03"
			if backend.pg {
				second = time.Date(2031, 2, 3, 0, 0, 0, 0, time.UTC)
			}
			run(referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,n INT NOT NULL,f BOOL)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2031-02-03','\x095c80',1,FALSE),(2,NULL,NULL,1,TRUE)`, 2))
			check := func(date, blob any, flag1, flag2 bool) {
				updateCoreByteRows(t, p.ctx, backend.db, "SELECT b FROM t ORDER BY id", [][][]byte{{{9, 92, 128}}, {nil}})
				run(referenceSelect("SELECT id,d,b,n,f FROM t ORDER BY id", []string{"id", "d", "b", "n", "f"}, []string{"INT4", "DATE", "BYTEA", "INT4", "BOOL"}, [][]any{{int64(1), date, blob, int64(1), flag1}, {int64(2), nil, nil, int64(1), flag2}}))
			}
			for _, tc := range []struct {
				q          string
				diagnostic referenceDiagnostic
			}{{"UPDATE t SET d='bad'", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type date: "bad"`, Position: "16"}}, {"UPDATE t SET d='1900-02-29'", referenceDiagnostic{Code: "22008", Message: `date/time field value out of range: "1900-02-29"`, Position: "16"}}, {`UPDATE t SET b='\x0'`, referenceDiagnostic{Code: "22023", Message: "invalid hexadecimal data: odd number of digits", Position: "16"}}, {`UPDATE t SET b='\xGG'`, referenceDiagnostic{Code: "22023", Message: `invalid hexadecimal digit: "G"`, Position: "16"}}, {`UPDATE t SET b='\q'`, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type bytea", Position: "16"}}} {
				for _, prefix := range []string{"", "/*é*/ "} {
					diagnostic := tc.diagnostic
					if prefix != "" {
						diagnostic.Position = "22"
					}
					for _, prepared := range []bool{false, true} {
						step := referenceImplicitError(prefix+tc.q, diagnostic)
						step.prepared = prepared
						step.errorStage = "execute"
						if prepared {
							step.errorStage = "prepare"
						}
						run(step)
						check(second, "\t\\\x80", false, true)
					}
				}
			}
		})
	}
}
