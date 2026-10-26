package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
					uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k "+c.typ+",extra INT,v INT,CONSTRAINT u UNIQUE(k,extra))", "CREATE TABLE t(k "+c.typ+",extra INT,v INT)", "CREATE UNIQUE INDEX u ON t(k,extra)")
					run(fixture.Exec("INSERT INTO t VALUES("+c.seed+",1,10)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Select("SELECT v FROM t FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
					fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='10ms'", 0)
					outcome := fixture.RowExecute(p.Ctx, b, c.update, true)
					if c.blocked {
						want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						// Physical heap coordinates have no native equivalent.
						if backend.PG {
							action := "updating"
							if c.name == "delete" {
								action = "deleting"
							}
							want.Context = "while " + action + " tuple (0,1) in relation \"t\""
						}
						fixture.AssertDiagnostic(t, outcome.Err, &want)
					} else {
						fixture.State(t, outcome.Err, "")
						fixture.LockAffected(t, outcome.Result, 1)
					}
					run(fixture.Exec("ROLLBACK", 0))
					fixture.LockExec(t, p.Ctx, b, "SET lock_timeout=0", 0)
					if outcome.Err != nil {
						fixture.LockExec(t, p.Ctx, b, c.update, 1)
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
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
						uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
						if operation != "insert" {
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						}
						run(fixture.Exec("BEGIN", 0))
						switch operation {
						case "insert":
							run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						case "delete":
							run(fixture.Exec("DELETE FROM t WHERE k=1", 1))
						case "update":
							run(fixture.Exec("UPDATE t SET k=2 WHERE k=1", 1))
						}
						// Independent disjoint key and plain reader must finish while A is active.
						fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(9)", 1))
						expected := [][]any{{int64(9)}}
						if operation != "insert" {
							expected = [][]any{{int64(1)}, {int64(9)}}
						}
						fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, expected))
						if !backend.PG {
							// Native has no physical wait catalog. A bounded SQL timeout
							// independently proves equal-key blocking before peer outcome.
							fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='10ms'", 0)
							timeout := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
							fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", timeout)))
							fixture.LockExec(t, p.Ctx, b, "SET lock_timeout=0", 0)
						}
						// PostgreSQL's external wait catalogs only establish the synchronization;
						// the SQL effect and assertions remain the same dual-mode scenario.
						var ap, bp int
						if backend.PG {
							if err := a.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&ap); err != nil {
								t.Fatal(err)
							}
							if err := b.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&bp); err != nil {
								t.Fatal(err)
							}
						}
						done := fixture.RowAsync(t, p.Ctx, b, "INSERT INTO t VALUES(1)", true, false)
						if backend.PG {
							fixture.RowWait(t, p.Ctx, bp, ap)
						}
						if commit {
							run(fixture.Exec("COMMIT", 0))
						} else {
							run(fixture.Exec("ROLLBACK", 0))
						}
						result := fixture.LockResult(t, done)
						collision := (operation == "insert") == commit
						if collision {
							want := fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")
							fixture.AssertDiagnostic(t, result.Err, &want)
						} else {
							fixture.State(t, result.Err, "")
							fixture.LockAffected(t, result.Result, 1)
						}
						final := [][]any{{int64(1)}, {int64(9)}}
						if operation == "update" && commit {
							final = [][]any{{int64(1)}, {int64(2)}, {int64(9)}}
						}
						run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, final))
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
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
			uniqueIndexReferenceCreate(run, standalone, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", "CREATE TABLE t(k INT)", "CREATE UNIQUE INDEX u ON t(k)")
			run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
			run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{}))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
