//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceUpdateCoreComposition(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", true, p.postgres, p.postgresRecord}, {"native", false, p.memory, p.memoryRecord}} {
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
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.record, s) }
			// DATE's native string and pq civil time are separately specified. The
			// existing reference recorder represents exported BYTEA as raw strings;
			// expected strings below are independent byte literals, not output encoding.
			var leap, first, second any = "2024-02-29", "2030-01-02", "2031-02-03"
			if backend.pg {
				leap = time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
				first = time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
				second = time.Date(2031, 2, 3, 0, 0, 0, 0, time.UTC)
			}
			run(referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,srcd DATE,srcb BYTEA,n INT NOT NULL,f BOOL)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2000-01-01','\x0102','2024-02-29','\x005cff',1,TRUE),(2,'2000-01-02','\x03',NULL,NULL,1,NULL)`, 2))
			names := []string{"id", "d", "b", "n", "f"}
			types := []string{"INT4", "DATE", "BYTEA", "INT4", "BOOL"}
			check := func(date, blob any, flag1, flag2 bool) {
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
			q := "UPDATE t SET d=$1,b=$2 WHERE id=$3"
			run(referenceExec(q, 1, "2030-01-02", []byte{65, 0, 255}, 1))
			check(first, "A\x00\xff", false, true)
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
			check(second, `\q`, false, true)
			result, err = held.ExecContext(p.ctx, "2031-02-03", `\x41`, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			check(second, `\x41`, false, true)
			result, err = held.ExecContext(p.ctx, "2031-02-03", []byte{}, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			check(second, "", false, true)
			payload := []byte{9, 92, 128}
			result, err = held.ExecContext(p.ctx, "2031-02-03", payload, 1)
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			payload[0] = 99
			check(second, "\t\\\x80", false, true)
			// Raw BYTEA parameters are not textual bytea encodings; malformed textual
			// bytea is exercised as a literal, while DATE bind has portal provenance.
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
			dateError := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: `invalid input syntax for type date: "bad"`}
			if backend.pg {
				dateError.Context = "unnamed portal parameter $1 = '...'"
			}
			for _, prepared := range []bool{false, true} {
				var err error
				if prepared {
					_, err = held.ExecContext(p.ctx, "bad", []byte{42}, 1)
				} else {
					_, err = c.ExecContext(p.ctx, q, "bad", []byte{42}, 1)
				}
				referenceAssertDiagnostic(t, err, &dateError)
				check(second, "\t\\\x80", false, true)
			}
			run(referenceExec("CREATE TABLE nn(id INT,d DATE,b BYTEA,n INT NOT NULL)", 0))
			run(referenceExec(`INSERT INTO nn VALUES(1,'2000-01-01','\x01',1)`, 1))
			failed, err := c.PrepareContext(p.ctx, "UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer failed.Close()
			for _, output := range []struct{ name, detail string }{{"hex", `Failing row contains (1, 2024-02-29, \x005cff, null).`}, {"escape", `Failing row contains (1, 2024-02-29, \000\\\377, null).`}} {
				run(referenceExec("SET bytea_output="+output.name, 0))
				want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: output.detail, Schema: "public", Table: "nn", Column: "n"}
				for _, prepared := range []bool{false, true} {
					var err error
					if prepared {
						_, err = failed.ExecContext(p.ctx, []byte{0, 92, 255}, "2024-02-29")
					} else {
						_, err = c.ExecContext(p.ctx, "UPDATE nn SET b=$1,d=$2,n=NULL WHERE id=1", []byte{0, 92, 255}, "2024-02-29")
					}
					referenceAssertDiagnostic(t, err, &want)
				}
				var original any = "2000-01-01"
				if backend.pg {
					original = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				run(referenceSelect("SELECT id,d,b,n FROM nn", []string{"id", "d", "b", "n"}, []string{"INT4", "DATE", "BYTEA", "INT4"}, [][]any{{int64(1), original, "\x01", int64(1)}}))
			}
		})
	}
}

func TestReferenceUpdateCoreCompositionAtomicNotNull(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT,d DATE,b BYTEA,n INT NOT NULL,nextn INT)", 0))
	p.step(t, referenceExec(`INSERT INTO t VALUES(1,'2000-01-01','\x01',1,2),(2,'2000-01-01','\x02',1,NULL)`, 2))
	for _, backend := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"postgres", true, p.postgres, p.postgresRecord}, {"native", false, p.memory, p.memoryRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "ROLLBACK")
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, backend.record, step) }
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
				run(referenceSelect("SELECT id,d,b,n,nextn FROM t ORDER BY id", []string{"id", "d", "b", "n", "nextn"}, []string{"INT4", "DATE", "BYTEA", "INT4", "INT4"}, [][]any{{int64(1), date, "\x01", int64(1), int64(2)}, {int64(2), date, "\x02", int64(1), nil}}))
			}
			for _, prepared := range []bool{false, true} {
				for _, explicit := range []bool{false, true} {
					if explicit {
						run(referenceExec("BEGIN", 0))
						run(referenceExec("UPDATE t SET n=7 WHERE id=1", 1))
					}
					var err error
					if prepared {
						_, err = held.ExecContext(p.ctx, "2024-02-29", []byte{0, 92, 255})
					} else {
						_, err = c.ExecContext(p.ctx, q, "2024-02-29", []byte{0, 92, 255})
					}
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: `Failing row contains (2, 2024-02-29, \x005cff, null, null).`, Schema: "public", Table: "t", Column: "n"})
					if explicit {
						run(referenceImplicitError("SELECT n FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						run(referenceExec("ROLLBACK", 0))
					}
					check()
				}
			}
		})
	}
}
