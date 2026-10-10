package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceUniqueRowLockStrength(t *testing.T) {
	uniqueReferenceRowLockStrength(t, false)
}

func TestReferenceUniqueIndexRowLockStrength(t *testing.T) {
	uniqueReferenceRowLockStrength(t, true)
}

func uniqueReferenceRowLockStrength(t *testing.T, standalone bool) {
	cases := []struct {
		name, typ, seed, update string
		blocked                 bool
	}{
		{"payload", "INT", "1", "UPDATE t SET v=20", false},
		{"exact_noop", "INT", "1", "UPDATE t SET k=k", false},
		{"changed_key", "INT", "1", "UPDATE t SET k=2", true},
		{"delete", "INT", "1", "DELETE FROM t", true},
		{"numeric_scale", "NUMERIC", "1.0", "UPDATE t SET k=1.00", true},
		{"numeric_exact", "NUMERIC", "1.0", "UPDATE t SET k=1.0", false},
		{"signed_zero", "DOUBLE PRECISION", "'0'", "UPDATE t SET k='-0'", true},
		{"composite_changed", "INT", "1", "UPDATE t SET extra=2", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
					uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k "+c.typ+",extra INT,v INT,CONSTRAINT u UNIQUE(k,extra))", "CREATE TABLE t(k "+c.typ+",extra INT,v INT)", "CREATE UNIQUE INDEX u ON t(k,extra)")
					run(referenceExec("INSERT INTO t VALUES("+c.seed+",1,10)", 1))
					run(referenceExec("BEGIN", 0))
					run(referenceSelect("SELECT v FROM t FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
					outcome := referenceRowExecute(p.ctx, b, c.update, true)
					if c.blocked {
						want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						// Physical heap coordinates have no native equivalent.
						if backend.pg {
							action := "updating"
							if c.name == "delete" {
								action = "deleting"
							}
							want.Context = "while " + action + " tuple (0,1) in relation \"t\""
						}
						referenceAssertDiagnostic(t, outcome.err, &want)
					} else {
						referenceState(t, outcome.err, "")
						referenceLockAffected(t, outcome.result, 1)
					}
					run(referenceExec("ROLLBACK", 0))
					referenceLockExec(t, p.ctx, b, "SET lock_timeout=0", 0)
					if outcome.err != nil {
						referenceLockExec(t, p.ctx, b, c.update, 1)
					}
				})
			}
		})
	}
}

func TestReferenceUniqueContention(t *testing.T) {
	uniqueReferenceContention(t, false)
}

func TestReferenceUniqueIndexContention(t *testing.T) {
	uniqueReferenceContention(t, true)
}

func uniqueReferenceContention(t *testing.T, standalone bool) {
	for _, operation := range []string{"insert", "delete", "update"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%t", operation, commit), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
						uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
						if operation != "insert" {
							run(referenceExec("INSERT INTO t VALUES(1)", 1))
						}
						run(referenceExec("BEGIN", 0))
						switch operation {
						case "insert":
							run(referenceExec("INSERT INTO t VALUES(1)", 1))
						case "delete":
							run(referenceExec("DELETE FROM t WHERE k=1", 1))
						case "update":
							run(referenceExec("UPDATE t SET k=2 WHERE k=1", 1))
						}
						// Independent disjoint key and plain reader must finish while A is active.
						referenceRunStep(t, p.ctx, b, backend.recorder, referenceExec("INSERT INTO t VALUES(9)", 1))
						expected := [][]any{{int64(9)}}
						if operation != "insert" {
							expected = [][]any{{int64(1)}, {int64(9)}}
						}
						referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, expected))
						if !backend.pg {
							// Native has no physical wait catalog. A bounded SQL timeout
							// independently proves equal-key blocking before peer outcome.
							referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
							timeout := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							referenceRunStep(t, p.ctx, b, backend.recorder, referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", timeout)))
							referenceLockExec(t, p.ctx, b, "SET lock_timeout=0", 0)
						}
						// PostgreSQL's external wait catalogs only establish the synchronization;
						// the SQL effect and assertions remain the same dual-mode scenario.
						var ap, bp int
						if backend.pg {
							if err := a.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
								t.Fatal(err)
							}
							if err := b.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
								t.Fatal(err)
							}
						}
						done := referenceRowAsync(t, p.ctx, b, "INSERT INTO t VALUES(1)", true, false)
						if backend.pg {
							referenceRowWait(t, p.ctx, bp, ap)
						}
						if commit {
							run(referenceExec("COMMIT", 0))
						} else {
							run(referenceExec("ROLLBACK", 0))
						}
						result := referenceLockResult(t, done)
						collision := (operation == "insert") == commit
						if collision {
							want := uniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")
							referenceAssertDiagnostic(t, result.err, &want)
						} else {
							referenceState(t, result.err, "")
							referenceLockAffected(t, result.result, 1)
						}
						final := [][]any{{int64(1)}, {int64(9)}}
						if operation == "update" && commit {
							final = [][]any{{int64(1)}, {int64(2)}, {int64(9)}}
						}
						run(referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, final))
					})
				}
			})
		}
	}
}

func TestReferenceUniqueRepeatableRead(t *testing.T) {
	uniqueReferenceRepeatableRead(t, false)
}

func TestReferenceUniqueIndexRepeatableRead(t *testing.T) {
	uniqueReferenceRepeatableRead(t, true)
}

func uniqueReferenceRepeatableRead(t *testing.T, standalone bool) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
			uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
			run(referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
			run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{}))
			referenceRunStep(t, p.ctx, b, backend.recorder, referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{}))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
