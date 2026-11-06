package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAddColumnReferencesOptions(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, tc := range []struct{ name, clause, definition string }{
			{"implicit_primary", "CONSTRAINT x REFERENCES p", "FOREIGN KEY (pid) REFERENCES p(id)"},
			{"explicit_simple", "NULL CONSTRAINT x REFERENCES p(id) MATCH SIMPLE", "FOREIGN KEY (pid) REFERENCES p(id)"},
			{"explicit_full", "CONSTRAINT x REFERENCES p(id) MATCH FULL NULL", "FOREIGN KEY (pid) REFERENCES p(id) MATCH FULL"},
			{"delete_then_update", "CONSTRAINT x REFERENCES p ON DELETE CASCADE ON UPDATE SET NULL", "FOREIGN KEY (pid) REFERENCES p(id) ON UPDATE SET NULL ON DELETE CASCADE"},
			{"update_then_delete", "CONSTRAINT x REFERENCES p ON UPDATE SET NULL ON DELETE CASCADE", "FOREIGN KEY (pid) REFERENCES p(id) ON UPDATE SET NULL ON DELETE CASCADE"},
			{"local_before", "DEFAULT 1 NOT NULL CONSTRAINT positive CHECK(pid>0) UNIQUE CONSTRAINT x REFERENCES p", "FOREIGN KEY (pid) REFERENCES p(id)"},
			{"local_after", "CONSTRAINT x REFERENCES p UNIQUE CONSTRAINT positive CHECK(pid>0) NOT NULL DEFAULT 1", "FOREIGN KEY (pid) REFERENCES p(id)"},
			{"local_interleaved", "UNIQUE DEFAULT 1 CONSTRAINT x REFERENCES p NOT NULL CONSTRAINT positive CHECK(pid>0)", "FOREIGN KEY (pid) REFERENCES p(id)"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "control", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(10)", 1))
				run(fixture.Exec("ALTER TABLE t ADD COLUMN pid INT "+tc.clause, 0))
				run(columnReferencesDefinitions("t", []any{"x", "f", tc.definition}))
				run(enforcementFlags("t", "x", true, true))
				local := tc.name == "local_before" || tc.name == "local_after" || tc.name == "local_interleaved"
				var fill any
				if local {
					fill = int64(1)
				}
				run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), fill}}))
				fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, []bool{true, !local}, 1)
				run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(20,99)", addConstraintFK("t", "x", "pid", "99", "p")))
				if local {
					run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"positive", "c", "CHECK ((pid > 0))"}, {"t_pid_key", "u", "UNIQUE (pid)"}, {"t_pid_not_null", "n", "NOT NULL pid"}, {"x", "f", tc.definition}}))
					run(fixture.Select("SELECT pg_get_expr(adbin,adrelid) FROM pg_attrdef WHERE adrelid='t'::regclass", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"1"}}))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(20,NULL)", fixture.PrimaryKeyReferenceNull("t", "pid", "20, null")))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(20,0)", enforcementCheck("t", "positive", false, "20, 0")))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(20,1)", fixture.UniqueReferenceDuplicate("t", "t_pid_key", "Key (pid)=(1) already exists.")))
				} else {
					run(fixture.Exec("INSERT INTO t VALUES(20,NULL)", 1))
				}
				// The explicit table-constraint control has independently specified
				// definition, flags and enforcement, not parity-only assertions.
				run(fixture.Exec("CREATE TABLE control(id INT)", 0))
				run(fixture.Exec("ALTER TABLE control ADD pid INT, ADD CONSTRAINT control_fk FOREIGN KEY(pid) REFERENCES p"+map[bool]string{true: " MATCH FULL", false: ""}[tc.name == "explicit_full"], 0))
				def := "FOREIGN KEY (pid) REFERENCES p(id)"
				if tc.name == "explicit_full" {
					def += " MATCH FULL"
				}
				run(columnReferencesDefinitions("control", []any{"control_fk", "f", def}))
				run(enforcementFlags("control", "control_fk", true, true))
				run(fixture.ErrorDiagnostic("INSERT INTO control VALUES(1,99)", addConstraintFK("control", "control_fk", "pid", "99", "p")))
			})
		}
		for _, tc := range []struct{ name, clause, definition string }{
			{"existing_self", "ADD pid INT REFERENCES t(id)", "FOREIGN KEY (pid) REFERENCES t(id)"},
			{"new_unique_self", "ADD pid INT UNIQUE REFERENCES t(pid)", "FOREIGN KEY (pid) REFERENCES t(pid)"},
			{"later_unique_self", "ADD pid INT REFERENCES t(target), ADD target INT UNIQUE", "FOREIGN KEY (pid) REFERENCES t(target)"},
			{"earlier_unique_self", "ADD target INT UNIQUE, ADD pid INT REFERENCES t(target)", "FOREIGN KEY (pid) REFERENCES t(target)"},
			{"later_table_unique_self", "ADD pid INT REFERENCES t(target), ADD target INT, ADD UNIQUE(target)", "FOREIGN KEY (pid) REFERENCES t(target)"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("ALTER TABLE t "+tc.clause, 0))
				run(columnReferencesDefinitions("t", []any{"t_pid_fkey", "f", tc.definition}))
				if tc.name == "existing_self" {
					run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,99)", addConstraintFK("t", "t_pid_fkey", "pid", "99", "t")))
				} else if tc.name == "new_unique_self" {
					run(fixture.Exec("INSERT INTO t VALUES(1,9)", 1))
				} else {
					run(fixture.Exec("INSERT INTO t(id,pid,target) VALUES(1,9,9)", 1))
					run(fixture.ErrorDiagnostic("INSERT INTO t(id,pid,target) VALUES(2,99,2)", addConstraintFK("t", "t_pid_fkey", "pid", "99", "t")))
				}
			})
		}
	})
}

func TestReferenceAlterAddColumnReferencesActions(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, direction := range []string{"DELETE", "UPDATE"} {
			for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT", "SET NULL (pid)", "SET DEFAULT (pid)"} {
				if direction == "UPDATE" && (action == "SET NULL (pid)" || action == "SET DEFAULT (pid)") {
					continue // Invalid UPDATE lists have exact boundary scenarios below.
				}
				t.Run(direction+"/"+action, func(t *testing.T) {
					columnReferencesClean(t, p, c, "t", "p")
					run := columnReferencesRun(t, p, b, c, prepared)
					run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
					run(fixture.Exec("CREATE TABLE t(id INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(10)", 1))
					other := " ON UPDATE NO ACTION"
					if direction == "UPDATE" {
						other = " ON DELETE NO ACTION"
					}
					run(fixture.Exec("ALTER TABLE t ADD pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON "+direction+" "+action+other, 0))
					def := "FOREIGN KEY (pid) REFERENCES p(id)"
					if action != "NO ACTION" {
						def += " ON " + direction + " " + action
					}
					run(columnReferencesDefinitions("t", []any{"fk", "f", def}))
					code := map[string]string{"NO ACTION": "a", "RESTRICT": "r", "CASCADE": "c", "SET NULL": "n", "SET DEFAULT": "d", "SET NULL (pid)": "n", "SET DEFAULT (pid)": "d"}[action]
					update, delete := "a", code
					if direction == "UPDATE" {
						update, delete = code, "a"
					}
					var subset any
					if action == "SET NULL (pid)" || action == "SET DEFAULT (pid)" {
						subset = "{2}"
					}
					run(fixture.Select("SELECT confupdtype,confdeltype,confdelsetcols::text FROM pg_constraint WHERE conrelid='t'::regclass AND conname='fk'", []string{"confupdtype", "confdeltype", "confdelsetcols"}, []string{"CHAR", "CHAR", "TEXT"}, [][]any{{update, delete, subset}}))
					run(fixture.Select("SELECT pid FROM t", []string{"pid"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Exec("UPDATE t SET pid=2", 1))
					q := "DELETE FROM p WHERE id=2"
					if direction == "UPDATE" {
						q = "UPDATE p SET id=3 WHERE id=2"
					}
					var want any = int64(2)
					rows := [][]any{{int64(10), want}}
					if action == "NO ACTION" || action == "RESTRICT" {
						d := fixture.ForeignKeyParent("p", "t", "fk", `Key (id)=(2) is still referenced from table "t".`)
						if action == "RESTRICT" {
							d = fixture.ForeignKeyError("23001", `update or delete on table "p" violates RESTRICT setting of foreign key constraint "fk" on table "t"`, `Key (id)=(2) is referenced from table "t".`, "t", "fk")
						}
						run(fixture.ErrorDiagnostic(q, d))
						run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					} else {
						run(fixture.Exec(q, 1))
						switch action {
						case "CASCADE":
							want = int64(3)
						case "SET NULL", "SET NULL (pid)":
							want = nil
						default:
							want = int64(1)
						}
						rows = [][]any{{int64(10), want}}
						if action == "CASCADE" && direction == "DELETE" {
							rows = nil
						}
						parent := [][]any{{int64(1)}}
						if direction == "UPDATE" {
							parent = append(parent, []any{int64(3)})
						}
						run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, parent))
					}
					run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, rows))
				})
			}
		}
	})
}

func TestReferenceAlterAddColumnReferencesDeferred(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, tc := range []struct {
			clause               string
			deferrable, deferred bool
		}{
			{"NOT DEFERRABLE", false, false},
			{"INITIALLY IMMEDIATE", false, false},
			{"DEFERRABLE INITIALLY IMMEDIATE", true, false},
			{"INITIALLY DEFERRED", true, true},
		} {
			t.Run("timing/"+tc.clause, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("ALTER TABLE t ADD pid INT CONSTRAINT fk REFERENCES p "+tc.clause, 0))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conrelid='t'::regclass AND contype='f'", []any{"fk", tc.deferrable, tc.deferred}))
				run(fixture.Exec("BEGIN", 0))
				if tc.deferrable {
					run(fixture.Exec("SET CONSTRAINTS fk DEFERRED", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,9)", 1))
					run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT id,pid FROM t", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
				} else {
					run(fixture.ErrorDiagnostic("SET CONSTRAINTS fk DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "fk" is not deferrable`)))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,9)", addConstraintFK("t", "fk", "pid", "9", "p")))
				}
			})
		}
		columnReferencesClean(t, p, c, "t", "p")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE t ADD pid INT CONSTRAINT fk REFERENCES p DEFERRABLE INITIALLY DEFERRED", 0))
		run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conrelid='t'::regclass AND contype='f'", []any{"fk", true, true}))
		run(columnReferencesDefinitions("t", []any{"fk", "f", "FOREIGN KEY (pid) REFERENCES p(id) DEFERRABLE INITIALLY DEFERRED"}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(2,9)", 1))
		run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO t VALUES(3,99)", 1))
		run(fixture.ErrorDiagnostic("COMMIT", addConstraintFK("t", "fk", "pid", "99", "p")))
		run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), int64(9)}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT repair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(3,99)", 1))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS fk IMMEDIATE", addConstraintFK("t", "fk", "pid", "99", "p")))
		run(fixture.Exec("ROLLBACK TO repair", 0))
		run(fixture.Exec("INSERT INTO t VALUES(3,100)", 1))
		run(fixture.Exec("INSERT INTO p VALUES(100)", 1))
		run(fixture.Exec("SET CONSTRAINTS fk IMMEDIATE", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("ALTER TABLE t DROP pid", 0))
		before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ddl", 0))
		addConstraintError(t, p, c, "ALTER TABLE t ADD pid INT DEFAULT 99 CONSTRAINT bad_fk REFERENCES p DEFERRABLE INITIALLY DEFERRED", addConstraintFK("t", "bad_fk", "pid", "99", "p"), prepared, "execute")
		run(fixture.Exec("ROLLBACK TO ddl", 0))
		columnReferencesUnchanged(t, p, b, c, before, attrs)
		run(fixture.Exec("COMMIT", 0))
	})
}

func TestReferenceAlterAddColumnReferencesNotEnforced(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, properties := range []string{"NOT ENFORCED", "NOT DEFERRABLE INITIALLY IMMEDIATE NOT ENFORCED", "NOT ENFORCED INITIALLY IMMEDIATE NOT DEFERRABLE", "ENFORCED", "NOT DEFERRABLE INITIALLY IMMEDIATE ENFORCED"} {
			t.Run(properties, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(10)", 1))
				enforced := properties == "ENFORCED" || properties == "NOT DEFERRABLE INITIALLY IMMEDIATE ENFORCED"
				value := 9
				if enforced {
					value = 1
				}
				run(fixture.Exec(fmt.Sprintf("ALTER TABLE t ADD pid INT DEFAULT %d CONSTRAINT fk REFERENCES p %s", value, properties), 0))
				def := "FOREIGN KEY (pid) REFERENCES p(id)"
				if !enforced {
					def += " NOT ENFORCED"
				}
				oid := enforcementIdentity(t, p, b, c, "t", "fk", def, enforced, enforced)
				enforcementTriggers(t, p, b, c, oid, enforced)
				run(fixture.Select("SELECT id,pid FROM t", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(value)}}))
				if enforced {
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(20,99)", addConstraintFK("t", "fk", "pid", "99", "p")))
				} else {
					run(fixture.Exec("INSERT INTO t VALUES(20,99)", 1))
					run(fixture.Exec("UPDATE t SET pid=100 WHERE id=10", 1))
					run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(100)}, {int64(20), int64(99)}}))
				}
				run(fixture.Select("SELECT constraint_name,constraint_type,is_deferrable,initially_deferred,enforced FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='t' AND constraint_type='FOREIGN KEY'", []string{"constraint_name", "constraint_type", "is_deferrable", "initially_deferred", "enforced"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"fk", "FOREIGN KEY", "NO", "NO", map[bool]string{true: "YES", false: "NO"}[enforced]}}))
				run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_constraint'::regclass AND objid=$1::oid AND deptype IN ('a','n')", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, oid))
			})
		}
	})
}
