package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT uq UNIQUE(a), ADD CONSTRAINT pk PRIMARY KEY(a), ADD CONSTRAINT ex EXCLUDE USING btree(note WITH =)", 0))
				before := validateConstraintSnapshot(t, p, c)
				for _, name := range []string{"absent", "uq", "pk", "ex"} {
					d := fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf(`constraint "%s" of relation "t" does not exist`, name))
					if name != "absent" {
						d.Code = "42809"
						d.Message = fmt.Sprintf(`cannot validate constraint "%s" of relation "t"`, name)
						d.Detail = "This operation is not supported for this type of constraint."
					}
					addConstraintError(t, p, c, "ALTER TABLE t VALIDATE CONSTRAINT "+name, d, prepared, "execute")
				}
				validateConstraintOrigins(t, p, c, before)
				run(fixture.Exec(`CREATE TABLE "t""é"("a\\tail" INT)`, 0))
				run(fixture.Exec(`INSERT INTO "t""é" VALUES(0)`, 1))
				run(fixture.Exec(`ALTER TABLE "t""é" ADD CONSTRAINT "ck""é\\tail" CHECK("a\\tail">0) NOT VALID`, 0))
				d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "ck"é\\tail" of relation "t"é" is violated by some row`)
				d.Schema, d.Table, d.Constraint = "public", `t"é`, `ck"é\\tail`
				addConstraintError(t, p, c, `ALTER TABLE "t""é" VALIDATE CONSTRAINT "ck""é\\tail"`, d, prepared, "execute")
				missing := fixture.ControlsReferenceDiagnostic("42704", `constraint "absent"é\\tail" of relation "t"é" does not exist`)
				addConstraintError(t, p, c, `ALTER TABLE "t""é" VALIDATE CONSTRAINT "absent""é\\tail"`, missing, prepared, "execute")
			})
		})
	}
	for _, tc := range []struct{ q, token string }{
		{"ALTER TABLE t VALIDATE CONSTRAINT", ""},
		{"ALTER TABLE t VALIDATE CONSTRAINT ck garbage", "garbage"},
		{"ALTER TABLE t VALIDATE CONSTRAINT IF EXISTS ck", "EXISTS"},
		{`ALTER TABLE t "validate" CONSTRAINT ck`, `"validate"`},
		{`ALTER TABLE "té" VALIDATE CONSTRAINT ck NOT VALID`, "NOT"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.q+strconv.FormatBool(prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
					d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
					pos := utf8.RuneCountInString(tc.q) + 1
					if tc.token != "" {
						d.Message = `syntax error at or near "` + tc.token + `"`
						pos = utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1
					}
					d.Position = strconv.Itoa(pos)
					phase := "execute"
					if prepared {
						phase = "prepare"
					}
					addConstraintError(t, p, c, tc.q, d, prepared, phase)
					run(validateConstraintFlag("t", "ck", false))
				})
			})
		}
	}
	t.Run("authority_at_execution", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"validate_reader"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("CREATE ROLE validate_reader", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO validate_reader", 0))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT ck")
			run(fixture.Exec("SET ROLE validate_reader", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")))
			run(fixture.Exec("RESET ROLE", 0))
			run(validateConstraintFlag("t", "ck", false))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			// Already valid is still ALTER admission, including read-only transactions.
			run(fixture.Exec("BEGIN READ ONLY", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction")))
			run(fixture.Exec("ROLLBACK", 0))
			run(validateConstraintFlag("t", "ck", true))
			run(fixture.Exec("SET ROLE validate_reader", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")))
			run(fixture.Exec("RESET ROLE", 0))
		})
	})
	t.Run("drop_phase_missing", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t VALIDATE CONSTRAINT ck, DROP CONSTRAINT ck", fixture.ControlsReferenceDiagnostic("42704", `constraint "ck" of relation "t" does not exist`))))
			run(validateConstraintFlag("t", "ck", false))
		})
	})
}

func TestReferenceAlterValidateConstraintEnforcement(t *testing.T) {
	for _, tc := range validateConstraintCases() {
		t.Run(tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'bad')", 1))
				run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
				insert := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,'bad')")
				update := addConstraintHeld(t, p, b, c, "UPDATE t SET a=$1 WHERE note='bad'")
				consumer := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a,note")
				validate := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT "+validateConstraintName(tc))
				for _, valid := range []bool{false, true} {
					if valid {
						validateConstraintHeldRun(t, p, b, validate, fixture.Exec(validate.SQL, 0))
					}
					run(validateConstraintFlag("t", validateConstraintName(tc), valid))
					for _, h := range []fixture.HeldStatement{insert, update} {
						validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(h.SQL, tc.write, tc.bad))
						run(fixture.ErrorDiagnostic(h.SQL, tc.write, tc.bad))
					}
					// The first row cannot survive a later violation in a multi-row write.
					run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,'ok'),($1,'bad')", tc.write, tc.bad))
					validateConstraintHeldRun(t, p, b, consumer, addConstraintRows([]any{int64(1), "bad"}))
					validateConstraintHeldRun(t, p, b, insert, fixture.Exec(insert.SQL, 1, 2))
					run(fixture.Exec("DELETE FROM t WHERE a=2", 1))
					validateConstraintHeldRun(t, p, b, update, fixture.Exec(update.SQL, 1, 1))
					if tc.kind == "f" {
						d := fixture.ControlsReferenceDiagnostic("23503", `update or delete on table "p" violates foreign key constraint "fk" on table "t"`)
						d.Detail, d.Schema, d.Table, d.Constraint = `Key (id)=(1) is still referenced from table "t".`, "public", "t", "fk"
						for _, q := range []string{"UPDATE p SET id=8 WHERE id=1", "DELETE FROM p WHERE id=1"} {
							h := addConstraintHeld(t, p, b, c, q)
							validateConstraintHeldRun(t, p, b, h, fixture.ErrorDiagnostic(q, d))
						}
					}
				}
				validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "bad"})
			})
		})
	}
}
