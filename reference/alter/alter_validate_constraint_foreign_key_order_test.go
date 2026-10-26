package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintForeignKeyOrder(t *testing.T) {
	for _, route := range []string{"reverse_declaration", "add_before_validate", "validate_before_add"} {
		t.Run(route, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(9,10)", 1))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk_a FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
				aOID := addConstraintInvalid(t, p, b, c, "t", "fk_a", "FOREIGN KEY (a) REFERENCES p(id) NOT VALID")
				var bOID int64
				q := "ALTER TABLE t VALIDATE CONSTRAINT fk_b, VALIDATE CONSTRAINT fk_a"
				flags := [][]any{{"fk_a", false}}
				if route == "reverse_declaration" {
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p NOT VALID", 0))
					bOID = addConstraintInvalid(t, p, b, c, "t", "fk_b", "FOREIGN KEY (b) REFERENCES p(id) NOT VALID")
					flags = append(flags, []any{"fk_b", false})
				} else {
					add, validate := "ADD CONSTRAINT fk_b FOREIGN KEY(b) REFERENCES p", "VALIDATE CONSTRAINT fk_a"
					if route == "validate_before_add" {
						add, validate = validate, add
					}
					q = "ALTER TABLE t " + add + ", " + validate
				}
				held := addConstraintHeld(t, p, b, c, q)
				rows := fixture.Select("SELECT a,b FROM t ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(10)}})
				assertFlags := func(want [][]any) {
					run(fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, want))
				}
				assertFailure := func(want fixture.Diagnostic, heldRoute bool) {
					before, relations := validateConstraintSnapshot(t, p, c), addConstraintRelations(t, p, c)
					var dump []byte
					if !b.PG {
						dump = addConstraintDump(t, p)
					}
					if heldRoute {
						validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(q, want))
					} else {
						run(fixture.ErrorDiagnostic(q, want))
					}
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) || !reflect.DeepEqual(relations, addConstraintRelations(t, p, c)) {
						t.Fatal("failed ordered FK worklist changed identities, validity or backings")
					}
					if !b.PG && !bytes.Equal(dump, addConstraintDump(t, p)) {
						t.Fatal("failed ordered FK worklist changed native dump")
					}
					assertFlags(flags)
					validateConstraintIdentity(t, p, b, c, aOID, false, "FOREIGN KEY (a) REFERENCES p(id) NOT VALID")
					if bOID != 0 {
						validateConstraintIdentity(t, p, b, c, bOID, false, "FOREIGN KEY (b) REFERENCES p(id) NOT VALID")
					}
					run(rows)
				}

				// Explicit MISC validations retain action order. In mixed commands,
				// ordinary ADD history is queued before MISC in either textual order.
				assertFailure(addConstraintFK("t", "fk_b", "b", "10", "p"), false)
				assertFailure(addConstraintFK("t", "fk_b", "b", "10", "p"), true)
				run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				run(fixture.Exec("INSERT INTO p VALUES(10)", 1))
				// Repair only the first scheduled failure. The same held command must
				// now report fk_a, with no private fk_b promotion/addition leaking.
				assertFailure(addConstraintFK("t", "fk_a", "a", "9", "p"), true)
				run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(10)}}))
				run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
				assertFlags([][]any{{"fk_a", true}, {"fk_b", true}})
				validateConstraintIdentity(t, p, b, c, aOID, true, "FOREIGN KEY (a) REFERENCES p(id)")
				if bOID == 0 {
					if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='fk_b'").Scan(&bOID); err != nil {
						t.Fatal(err)
					}
					if bOID <= 0 || bOID == aOID {
						t.Fatal("added FK lacks its own positive identity", bOID, aOID)
					}
				}
				validateConstraintIdentity(t, p, b, c, bOID, true, "FOREIGN KEY (b) REFERENCES p(id)")
				run(rows)
				run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}, {int64(10)}}))
				for _, prepared := range []bool{false, true} {
					fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, rows.Query, rows.Columns, rows.Types, []bool{true, true}, 1)
				}
			})
		})
	}
}
