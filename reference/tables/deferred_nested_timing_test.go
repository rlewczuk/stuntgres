package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintNestedDispatch(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, route := range []string{"dynamic-do", "pl-function", "sql-function"} {
				t.Run(fmt.Sprintf("%s/explicit=%t/prepared=%t", route, explicit, prepared), func(t *testing.T) {
					deferredNestedTimingScenario(t, func(run func(fixture.Step)) {
						deferredNestedTimingTable(run, "unique", false)
						const writes = "INSERT INTO t VALUES(2,1),(3,1); DELETE FROM t WHERE id=3;"
						step := fixture.Exec("DO $$BEGIN EXECUTE 'SET CONSTRAINTS u DEFERRED'; "+writes+" END$$", 0)
						if route != "dynamic-do" {
							language, body := "sql", "SET CONSTRAINTS u DEFERRED; "+writes+" SELECT 7"
							if route == "pl-function" {
								language, body = "plpgsql", "BEGIN SET CONSTRAINTS u DEFERRED; "+writes+" RETURN 7; END"
							}
							run(fixture.Exec("CREATE FUNCTION nested_timing_fn() RETURNS INT LANGUAGE "+language+" VOLATILE AS $$"+body+"$$", 0))
							step = fixture.Select("SELECT nested_timing_fn() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
						}
						if explicit {
							run(fixture.Exec("BEGIN", 0))
						}
						step.Prepared = prepared
						run(step)
						if explicit {
							run(fixture.Exec("INSERT INTO t VALUES(4,1)", 1))
							run(fixture.Exec("DELETE FROM t WHERE id=4", 1))
							run(fixture.Exec("COMMIT", 0))
						}
						run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
						run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,1)", fixture.DeferredUniqueConflict("t", "u", "v", "1")))
					})
				})
			}
		}
	}
}

func TestReferenceDeferredConstraintNestedSet(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, carrier := range []string{"DO", "CALL"} {
			for _, explicit := range []bool{false, true} {
				for _, kind := range []string{"unique", "primary", "exclude", "fk"} {
					t.Run(fmt.Sprintf("%s/%s/explicit=%t/prepared=%t", kind, carrier, explicit, prepared), func(t *testing.T) {
						deferredNestedTimingScenario(t, func(run func(fixture.Step)) {
							deferredNestedTimingTable(run, kind, false)
							run(fixture.Exec("CREATE TABLE audit(v INT)", 0))
							insert, repair, _ := deferredNestedTimingViolation(kind, 1)
							body := "BEGIN SET CONSTRAINTS u DEFERRED; " + insert + "; " + repair + "; INSERT INTO audit VALUES(7); END"
							query, _ := deferredNestedTimingCarrier(run, carrier, body)
							if explicit {
								run(fixture.Exec("BEGIN", 0))
							}
							step := fixture.Exec(query, 0)
							step.Prepared = prepared
							run(step)
							if explicit {
								// The mode belongs to the caller's transaction, not just
								// the nested command or its procedure activation.
								value := 1
								if kind == "fk" {
									value = 9
								}
								run(fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES(4,%d)", value), 1))
								run(fixture.Exec("DELETE FROM t WHERE id=4", 1))
								run(fixture.Exec("COMMIT", 0))
							}
							run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
							run(fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
							// An implicit CALL/DO does not turn its override into the
							// default of the next independent transaction.
							_, _, conflict := deferredNestedTimingViolation(kind, 1)
							if kind == "fk" {
								_, _, conflict = deferredNestedTimingViolation(kind, 9)
								run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,9)", conflict))
							} else {
								run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(4,1)", conflict))
							}
						})
					})
				}
			}
		}
	}
}

func TestReferenceDeferredConstraintNestedImmediate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, carrier := range []string{"DO", "CALL"} {
			for _, explicit := range []bool{false, true} {
				for _, kind := range []string{"unique", "primary", "exclude", "fk"} {
					t.Run(fmt.Sprintf("%s/%s/explicit=%t/prepared=%t", kind, carrier, explicit, prepared), func(t *testing.T) {
						deferredNestedTimingScenario(t, func(run func(fixture.Step)) {
							deferredNestedTimingTable(run, kind, true)
							run(fixture.Exec("CREATE TABLE audit(v INT)", 0))
							insert, _, want := deferredNestedTimingViolation(kind, 1)
							body := "BEGIN INSERT INTO audit VALUES(7); "
							if !explicit {
								body += insert + "; "
							}
							body += "SET CONSTRAINTS u IMMEDIATE; INSERT INTO audit VALUES(8); END"
							query, routine := deferredNestedTimingCarrier(run, carrier, body)
							if explicit {
								run(fixture.Exec("BEGIN", 0))
								run(fixture.Exec("INSERT INTO audit VALUES(5)", 1))
								count := int64(2)
								if kind == "fk" {
									count = 1
								}
								run(fixture.Exec(insert, count))
								run(fixture.Exec("SAVEPOINT recover", 0))
							}
							want.Context = "SQL statement \"SET CONSTRAINTS u IMMEDIATE\"\nPL/pgSQL function " + routine + " line 1 at SQL statement"
							step := fixture.ErrorDiagnostic(query, want)
							step.Prepared, step.ErrorStage = prepared, "execute"
							run(step)
							if explicit {
								run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
								run(fixture.Exec("ROLLBACK TO recover", 0))
								run(fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}))
								count := int64(2)
								if kind == "fk" {
									count = 1
								}
								run(fixture.Exec("DELETE FROM t", count))
								run(fixture.Exec("SET CONSTRAINTS u IMMEDIATE", 0))
								run(fixture.Exec("COMMIT", 0))
							} else {
								run(fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, nil))
							}
							run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
						})
					})
				}
			}
		}
	}
}

func TestReferenceDeferredConstraintProceduralTimingLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, carrier := range []string{"DO", "CALL"} {
			for _, boundary := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
				for _, deferred := range []bool{false, true} {
					for _, kind := range []string{"unique", "primary", "exclude", "fk"} {
						t.Run(fmt.Sprintf("%s/%s/%s/default-deferred=%t/prepared=%t", kind, carrier, boundary, deferred, prepared), func(t *testing.T) {
							deferredNestedTimingScenario(t, func(run func(fixture.Step)) {
								deferredNestedTimingTable(run, kind, deferred)
								run(fixture.Exec("CREATE TABLE audit(v INT)", 0))
								if kind == "fk" {
									run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
								}
								mode := "DEFERRED"
								if deferred {
									mode = "IMMEDIATE"
								}
								insert, repair, want := deferredNestedTimingViolation(kind, 2)
								body := "BEGIN SET CONSTRAINTS u " + mode + "; INSERT INTO t VALUES(1,1); INSERT INTO audit VALUES(7); " + boundary + "; " + insert + "; " + repair + "; INSERT INTO audit VALUES(8); END"
								query, routine := deferredNestedTimingCarrier(run, carrier, body)
								step := fixture.Exec(query, 0)
								if !deferred {
									// A leaked DEFERRED override would allow the post-boundary
									// violation to be repaired and make this call succeed.
									want.Context = "SQL statement \"" + insert + "\"\nPL/pgSQL function " + routine + " line 1 at SQL statement"
									step = fixture.ErrorDiagnostic(query, want)
									step.ErrorStage = "execute"
								}
								step.Prepared = prepared
								run(step)
								var rows, audit [][]any
								if strings.HasPrefix(boundary, "COMMIT") {
									rows = append(rows, []any{int64(1), int64(1)})
									audit = append(audit, []any{int64(7)})
								}
								if deferred {
									// A leaked IMMEDIATE override would reject insert before
									// the fresh definition's deferred repair could run.
									rows = append(rows, []any{int64(2), int64(2)})
									audit = append(audit, []any{int64(8)})
								}
								run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
								run(fixture.Select("SELECT v FROM audit ORDER BY v", []string{"v"}, []string{"INT4"}, audit))
							})
						})
					}
				}
				// Explicit client transactions still forbid procedural rotation;
				// nested timing support must not relax the existing atomic boundary.
				t.Run(fmt.Sprintf("explicit-boundary/%s/%s/prepared=%t", carrier, boundary, prepared), func(t *testing.T) {
					deferredNestedTimingScenario(t, func(run func(fixture.Step)) {
						deferredNestedTimingTable(run, "unique", false)
						query, routine := deferredNestedTimingCarrier(run, carrier, "BEGIN SET CONSTRAINTS u DEFERRED; INSERT INTO t VALUES(1,1); "+boundary+"; END")
						run(fixture.Exec("BEGIN", 0))
						want := fixture.ControlsReferenceDiagnostic("2D000", "invalid transaction termination")
						verb := strings.Fields(boundary)[0]
						want.Context = "PL/pgSQL function " + routine + " line 1 at " + verb
						step := fixture.ErrorDiagnostic(query, want)
						step.Prepared, step.ErrorStage = prepared, "execute"
						run(step)
						run(fixture.Exec("ROLLBACK", 0))
						run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, nil))
					})
				})
			}
		}
	}
}

func deferredNestedTimingTable(run func(fixture.Step), kind string, deferred bool) {
	definition := "CONSTRAINT u UNIQUE(v)"
	switch kind {
	case "primary":
		definition = "CONSTRAINT u PRIMARY KEY(v)"
	case "exclude":
		definition = "CONSTRAINT u EXCLUDE(v WITH =)"
	case "fk":
		run(fixture.Exec("CREATE TABLE p(v INT PRIMARY KEY)", 0))
		definition = "CONSTRAINT u FOREIGN KEY(v) REFERENCES p(v)"
	}
	timing := " DEFERRABLE INITIALLY IMMEDIATE"
	if deferred {
		timing = " DEFERRABLE INITIALLY DEFERRED"
	}
	run(fixture.Exec("CREATE TABLE t(id INT,v INT,"+definition+timing+")", 0))
}

func deferredNestedTimingViolation(kind string, key int) (insert, repair string, want fixture.Diagnostic) {
	value := fmt.Sprint(key)
	insert = "INSERT INTO t VALUES(2," + value + "),(3," + value + ")"
	repair = "DELETE FROM t WHERE id=3"
	want = fixture.DeferredUniqueConflict("t", "u", "v", value)
	if kind == "exclude" {
		want = fixture.ExclusionReferenceConflict("t", "u", "v", value, value)
	}
	if kind == "fk" {
		insert = "INSERT INTO t VALUES(2," + value + ")"
		repair = "INSERT INTO p VALUES(" + value + ")"
		want = fixture.ForeignKeyChild("t", "u", "Key (v)=("+value+") is not present in table \"p\".")
	}
	return
}

func deferredNestedTimingCarrier(run func(fixture.Step), carrier, body string) (query, routine string) {
	if carrier == "CALL" {
		run(fixture.Exec("CREATE PROCEDURE nested_timing() LANGUAGE plpgsql AS $$"+body+"$$", 0))
		return "CALL nested_timing()", "nested_timing()"
	}
	return "DO $$" + body + "$$", "inline_code_block"
}

func deferredNestedTimingScenario(t *testing.T, body func(func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			body(func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, step) })
			if len(notices) != 0 {
				t.Fatalf("nested transaction timing emitted unexpected notices: %+v", notices)
			}
		})
	}
}
