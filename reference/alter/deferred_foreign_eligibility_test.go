package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredForeignKeyEventEligibility(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, update := range []string{"UPDATE c SET note=1", "UPDATE c SET pid=pid,note=1"} {
			for _, action := range []struct{ name, sql string }{
				{"add", "ALTER TABLE c ADD COLUMN extra TEXT"},
				{"set-default", "ALTER TABLE c ALTER COLUMN note SET DEFAULT 7"},
				{"drop-default", "ALTER TABLE c ALTER COLUMN note DROP DEFAULT"},
				{"absent-drop-default", "ALTER TABLE c ALTER COLUMN pid DROP DEFAULT"},
			} {
				t.Run(fmt.Sprintf("committed-unchanged/%s/%s/prepared=%t", update, action.name, prepared), func(t *testing.T) {
					deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED,note INT DEFAULT 0)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
						run(fixture.Exec("INSERT INTO c VALUES(1,0)", 1))
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec(update, 1))
						// A committed unchanged predecessor creates no UPDATE event;
						// do not consume events before the admission witness.
						run(fixture.Exec(action.sql, 0))
						run(fixture.Exec("COMMIT", 0))
						run(fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
					})
				})
			}
		}
		for _, key := range []struct {
			name, match, values string
			a, b                any
		}{
			{"simple-all-null", "SIMPLE", "a=NULL,b=NULL", nil, nil},
			{"simple-left-null", "SIMPLE", "a=NULL,b=9", nil, int64(9)},
			{"simple-right-null", "SIMPLE", "a=9,b=NULL", int64(9), nil},
			{"full-all-null", "FULL", "a=NULL,b=NULL", nil, nil},
		} {
			t.Run(fmt.Sprintf("null-exempt-update/%s/prepared=%t", key.name, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(a INT,b INT,UNIQUE(a,b))", 0))
					run(fixture.Exec("CREATE TABLE c(a INT,b INT,note INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p(a,b) MATCH "+key.match+" INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1,1)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1,1,0)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("UPDATE c SET "+key.values+",note=1", 1))
					run(fixture.Exec("ALTER TABLE c ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("ALTER TABLE c ALTER COLUMN note SET DEFAULT 2", 0))
					run(fixture.Exec("ALTER TABLE c ALTER COLUMN note DROP DEFAULT", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT a,b,note,extra FROM c", []string{"a", "b", "note", "extra"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{key.a, key.b, int64(1), nil}}))
				})
			})
		}
		for _, old := range []string{"NULL,NULL", "NULL,1", "1,NULL"} {
			for _, update := range []bool{false, true} {
				t.Run(fmt.Sprintf("parent-old-null/%s/update=%t/prepared=%t", old, update, prepared), func(t *testing.T) {
					deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(a INT,b INT,marker INT,UNIQUE(a,b))", 0))
						run(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p(a,b) INITIALLY DEFERRED)", 0))
						run(fixture.Exec("INSERT INTO p VALUES("+old+",0),(9,9,1)", 2))
						run(fixture.Exec("BEGIN", 0))
						mutation := "DELETE FROM p WHERE marker=0"
						var want [][]any
						if update {
							mutation = "UPDATE p SET a=2,b=2 WHERE marker=0"
							want = [][]any{{int64(2), int64(2), nil}}
						}
						run(fixture.Exec(mutation, 1))
						run(fixture.Exec("ALTER TABLE p ADD COLUMN extra TEXT", 0))
						run(fixture.Exec("ALTER TABLE p ALTER COLUMN marker SET DEFAULT 0", 0))
						run(fixture.Exec("ALTER TABLE p ALTER COLUMN marker DROP DEFAULT", 0))
						run(fixture.Exec("COMMIT", 0))
						run(fixture.Select("SELECT a,b,extra FROM p WHERE marker=0", []string{"a", "b", "extra"}, []string{"INT4", "INT4", "TEXT"}, want))
						run(fixture.Select("SELECT a,b FROM p WHERE marker=1", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
					})
				})
			}
		}
		for _, predecessor := range []string{"inserted", "changed", "unchanged-own-update"} {
			t.Run(fmt.Sprintf("own-predecessor/%s/prepared=%t", predecessor, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED,note INT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					if predecessor != "inserted" {
						run(fixture.Exec("INSERT INTO c VALUES(1,0)", 1))
					}
					run(fixture.Exec("BEGIN", 0))
					pid := int64(1)
					switch predecessor {
					case "inserted":
						run(fixture.Exec("INSERT INTO c VALUES(1,1)", 1))
					case "changed":
						run(fixture.Exec("UPDATE c SET pid=2,note=1", 1))
						pid = 2
					default:
						run(fixture.Exec("UPDATE c SET note=1", 1))
					}
					// Consume earlier obligations: only the replacement UPDATE can
					// account for the next pending-event rejection.
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("SET CONSTRAINTS fk DEFERRED", 0))
					run(fixture.Exec("UPDATE c SET note=2", 1))
					deferredForeignKeyEligibilityPending(run, "c")
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE c ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT pid,note,extra FROM c", []string{"pid", "note", "extra"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{pid, int64(2), nil}}))
				})
			})
		}
		for _, pid := range []int{2, 9} {
			t.Run(fmt.Sprintf("changed-child-key/%d/prepared=%t", pid, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED,note INT)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					run(fixture.Exec("INSERT INTO c VALUES(1,0)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(fmt.Sprintf("UPDATE c SET pid=%d,note=1", pid), 1))
					deferredForeignKeyEligibilityPending(run, "c")
					if pid == 9 {
						run(fixture.Exec("SAVEPOINT due", 0))
						run(fixture.ErrorDiagnostic("SET CONSTRAINTS fk IMMEDIATE", fixture.ForeignKeyChild("c", "fk", `Key (pid)=(9) is not present in table "p".`)))
						run(fixture.Exec("ROLLBACK TO due", 0))
						run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					}
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE c ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT pid,note,extra FROM c", []string{"pid", "note", "extra"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(pid), int64(1), nil}}))
				})
			})
		}
		for _, tc := range []struct {
			name, match, values string
			update, partial     bool
			a, b                any
		}{
			{"insert-valid", "SIMPLE", "1,1", false, false, int64(1), int64(1)},
			{"insert-simple-all-null", "SIMPLE", "NULL,NULL", false, false, nil, nil},
			{"insert-simple-partial-null", "SIMPLE", "1,NULL", false, false, int64(1), nil},
			{"insert-full-all-null", "FULL", "NULL,NULL", false, false, nil, nil},
			{"insert-full-partial-null", "FULL", "1,NULL", false, true, int64(1), int64(1)},
			{"update-full-partial-null", "FULL", "", true, true, int64(1), int64(1)},
		} {
			t.Run(fmt.Sprintf("insert-and-full-controls/%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(a INT,b INT,UNIQUE(a,b))", 0))
					run(fixture.Exec("CREATE TABLE c(a INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p(a,b) MATCH "+tc.match+" INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1,1)", 1))
					if tc.update {
						run(fixture.Exec("INSERT INTO c VALUES(1,1)", 1))
					}
					run(fixture.Exec("BEGIN", 0))
					if tc.update {
						run(fixture.Exec("UPDATE c SET b=NULL", 1))
					} else {
						run(fixture.Exec("INSERT INTO c VALUES("+tc.values+")", 1))
					}
					// INSERT queues even a NULL-exempt key, unlike UPDATE.
					deferredForeignKeyEligibilityPending(run, "c")
					if tc.partial {
						run(fixture.Exec("SAVEPOINT due", 0))
						run(fixture.ErrorDiagnostic("SET CONSTRAINTS fk IMMEDIATE", fixture.ForeignKeyChild("c", "fk", "MATCH FULL does not allow mixing of null and nonnull key values.")))
						run(fixture.Exec("ROLLBACK TO due", 0))
						run(fixture.Exec("UPDATE c SET b=1", 1))
					}
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE c ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT a,b,extra FROM c", []string{"a", "b", "extra"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{tc.a, tc.b, nil}}))
				})
			})
		}
		for _, repair := range []string{"UPDATE c SET pid=NULL", "UPDATE c SET pid=1", "DELETE FROM c"} {
			t.Run(fmt.Sprintf("retained-original-event/%s/prepared=%t", repair, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO c VALUES(9)", 1))
					run(fixture.Exec(repair, 1))
					// Admission filtering must not erase an already queued event,
					// even if its row was repaired, superseded or deleted.
					deferredForeignKeyEligibilityPending(run, "c")
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE c ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("COMMIT", 0))
					var want [][]any
					switch repair {
					case "UPDATE c SET pid=NULL":
						want = [][]any{{nil, nil}}
					case "UPDATE c SET pid=1":
						want = [][]any{{int64(1), nil}}
					}
					run(fixture.Select("SELECT pid,extra FROM c", []string{"pid", "extra"}, []string{"INT4", "TEXT"}, want))
				})
			})
		}
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("parent-nonnull-control/update=%t/prepared=%t", update, prepared), func(t *testing.T) {
				deferredForeignKeyEligibilityScenario(t, prepared, func(run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p INITIALLY DEFERRED)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					mutation := "DELETE FROM p"
					if update {
						mutation = "UPDATE p SET id=2"
					}
					run(fixture.Exec(mutation, 1))
					deferredForeignKeyEligibilityPending(run, "p")
					run(fixture.Exec("SAVEPOINT due", 0))
					run(fixture.ErrorDiagnostic("SET CONSTRAINTS fk IMMEDIATE", fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(1) is still referenced from table "c".`)))
					run(fixture.Exec("ROLLBACK TO due", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
					run(fixture.Exec("ALTER TABLE p ADD COLUMN extra TEXT", 0))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT p.id,c.pid,p.extra FROM p JOIN c ON p.id=c.pid", []string{"id", "pid", "extra"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), nil}}))
				})
			})
		}
	}
}

func deferredForeignKeyEligibilityPending(run func(fixture.Step), table string) {
	run(fixture.Exec("SAVEPOINT admission", 0))
	run(fixture.ErrorDiagnostic("ALTER TABLE "+table+" ADD COLUMN extra TEXT", fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "`+table+`" because it has pending trigger events`)))
	run(fixture.Exec("ROLLBACK TO admission", 0))
	run(fixture.Exec("RELEASE admission", 0))
}

func deferredForeignKeyEligibilityScenario(t *testing.T, prepared bool, body func(func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.ConcurrentIndexConn(t, p, backend)
			body(func(step fixture.Step) {
				switch step.Query {
				case "BEGIN", "COMMIT", "SAVEPOINT admission", "ROLLBACK TO admission", "RELEASE admission", "SAVEPOINT due", "ROLLBACK TO due":
				default:
					if prepared {
						step = fixture.Prepared(step)
					}
				}
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
			})
		})
	}
}
