package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

type replacementKind struct{ name, kind, definition, generated string }

func replacementKinds() []replacementKind {
	return []replacementKind{{"unique", "u", "UNIQUE", "t_a_key"}, {"primary", "p", "PRIMARY KEY", "t_pkey"}, {"exclude", "x", "EXCLUDE", "t_a_excl"}}
}
func replacementClause(k replacementKind, column string) string {
	if k.kind == "x" {
		return "EXCLUDE USING btree(" + column + " WITH =)"
	}
	return k.definition + "(" + column + ")"
}
func replacementWrite(k replacementKind, name string) fixture.Diagnostic {
	if k.kind == "x" {
		return fixture.ExclusionReferenceConflict("t", name, "a", "1", "1")
	}
	return fixture.UniqueReferenceDuplicate("t", name, "Key (a)=(1) already exists.")
}
func replacementBuild(k replacementKind, name, column, value string) fixture.Diagnostic {
	code, message, detail := "23505", `could not create unique index "`+name+`"`, "Key ("+column+")=("+value+") is duplicated."
	if k.kind == "x" {
		code, message, detail = "23P01", `could not create exclusion constraint "`+name+`"`, "Key ("+column+")=("+value+") conflicts with key ("+column+")=("+value+")."
	}
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail, d.Schema, d.Table, d.Constraint = detail, "public", "t", name
	return d
}
func replacementOID(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) (int64, int64) {
	t.Helper()
	var constraint, index int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname=$1", name).Scan(&constraint); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace=(SELECT relnamespace FROM pg_class WHERE oid='t'::regclass::oid)", name).Scan(&index); err != nil {
		t.Fatal(err)
	}
	if constraint <= 0 || index <= 0 {
		t.Fatal("missing real constraint/backing identities", constraint, index)
	}
	return constraint, index
}
func replacementSQL(name, add string, addFirst bool) string {
	drop := "DROP CONSTRAINT " + name
	if addFirst {
		return "ALTER TABLE t " + add + ", " + drop
	}
	return "ALTER TABLE t " + drop + ", " + add
}

// Released ALTER passes retire DROP dependencies before indexed ADD name lookup.
// Every source/target kind, explicit/anonymous name, text order and protocol is
// an independent replacement: generated names must reuse the vacated spelling.
func TestReferenceAlterAddConstraintReplacement(t *testing.T) {
	for _, old := range replacementKinds() {
		for _, next := range replacementKinds() {
			for _, anonymous := range []bool{false, true} {
				for _, addFirst := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s_to_%s/anonymous_%v/add_first_%v/prepared_%v", old.name, next.name, anonymous, addFirst, prepared), func(t *testing.T) {
							addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								run := addConstraintSetup(t, p, b, c)
								run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
								name := "occupied"
								if anonymous {
									name = next.generated
								}
								run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+name+" "+replacementClause(old, "a"), 0))
								oldOID, oldIndex := replacementOID(t, p, c, name)
								held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
								reader := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a")
								add := "ADD CONSTRAINT " + name + " " + replacementClause(next, "a")
								if anonymous {
									add = "ADD " + replacementClause(next, "a")
								}
								step := fixture.Exec(replacementSQL(name, add, addFirst), 0)
								if prepared {
									step = fixture.Prepared(step)
								}
								run(step)
								oid, index := replacementOID(t, p, c, name)
								if oid == oldOID || index == oldIndex {
									t.Fatal("replacement reused dropped logical/backing identity", oldOID, oid, oldIndex, index)
								}
								run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid AND (contype='u' OR contype='p' OR contype='x')", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{name, next.kind}}))
								run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE oid=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{name, "i"}}, index))
								if b.PG {
									run(fixture.Select("SELECT conindid FROM pg_constraint WHERE oid=$1", []string{"conindid"}, []string{"OID"}, [][]any{{fmt.Sprint(index)}}, oid))
								}
								fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, replacementWrite(next, name), 1, "bad"))
								fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 2, "new"))
								fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "old"}, {int64(2), "new"}}))
								fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{old.kind != "p" && next.kind != "p", true}, 2)
							})
						})
					}
				}
			}
		}
	}
	for _, old := range replacementKinds() {
		for _, next := range replacementKinds() {
			for _, addFirst := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("late_%s_to_%s/add_first_%v/prepared_%v", old.name, next.name, addFirst, prepared), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := addConstraintSetup(t, p, b, c)
							run(fixture.Exec("INSERT INTO t VALUES(1,'dup'),(2,'dup')", 2))
							run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT occupied "+replacementClause(old, "a"), 0))
							catalog, relations := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
							var dump []byte
							if !b.PG {
								dump = addConstraintDump(t, p)
							}
							held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
							addConstraintError(t, p, c, replacementSQL("occupied", "ADD CONSTRAINT occupied "+replacementClause(next, "note"), addFirst), replacementBuild(next, "occupied", "note", "dup"), prepared, "execute")
							addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
							run(addConstraintRows([]any{int64(1), "dup"}, []any{int64(2), "dup"}))
							fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, "SELECT a,note FROM t ORDER BY a", []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{old.kind != "p", true}, 2)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, replacementWrite(old, "occupied"), 1, "bad"))
							run(fixture.Exec("UPDATE t SET note='fixed' WHERE a=2", 1))
							step := fixture.Exec(replacementSQL("occupied", "ADD CONSTRAINT occupied "+replacementClause(next, "note"), addFirst), 0)
							if prepared {
								step = fixture.Prepared(step)
							}
							run(step)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 3, "new"))
							run(addConstraintRows([]any{int64(1), "dup"}, []any{int64(2), "fixed"}, []any{int64(3), "new"}))
						})
					})
				}
			}
		}
	}
	for _, next := range replacementKinds() {
		for _, addFirst := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("live_collision_%s/add_first_%v/prepared_%v", next.name, addFirst, prepared), func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := addConstraintSetup(t, p, b, c)
						run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
						run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT old UNIQUE(a)", 0))
						run(fixture.Exec("CREATE TABLE occupied(z INT)", 0))
						catalog, relations := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
						var dump []byte
						if !b.PG {
							dump = addConstraintDump(t, p)
						}
						addConstraintError(t, p, c, replacementSQL("old", "ADD CONSTRAINT occupied "+replacementClause(next, "a"), addFirst), fixture.ControlsReferenceDiagnostic("42P07", `relation "occupied" already exists`), prepared, "execute")
						addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
						run(addConstraintRows([]any{int64(1), "old"}))
					})
				})
			}
		}
	}
	for _, addFirst := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("self_cascade/add_first_%v/prepared_%v", addFirst, prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT occupied UNIQUE(a)", 0))
					run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT self_fk FOREIGN KEY(a) REFERENCES t(a)", 0))
					oldOID, oldIndex := replacementOID(t, p, c, "occupied")
					reader := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY note")
					drop, add := "DROP CONSTRAINT occupied CASCADE", "ADD CONSTRAINT occupied UNIQUE(note)"
					q := "ALTER TABLE t " + drop + ", " + add
					if addFirst {
						q = "ALTER TABLE t " + add + ", " + drop
					}
					step := fixture.Exec(q, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					run(step)
					oid, index := replacementOID(t, p, c, "occupied")
					if oid == oldOID || index == oldIndex {
						t.Fatal("self-CASCADE replacement retained old constraint/backing identity")
					}
					run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"occupied", "u"}}))
					run(fixture.Exec("INSERT INTO t VALUES(1,'new')", 1))
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "new"}, {int64(1), "old"}}))
				})
			})
		}
	}
}

// Fresh PK preliminary admission reads original NN validity before every ALTER
// update pass: neither SET promotion nor DROP can erase that prerequisite.
func TestReferenceAlterAddConstraintPrimaryPrerequisite(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		for _, drop := range []bool{false, true} {
			for _, pkFirst := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("dirty_%v/drop_%v/pk_first_%v/prepared_%v", dirty, drop, pkFirst, prepared), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := addConstraintSetup(t, p, b, c)
							run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
							if dirty {
								run(fixture.Exec("INSERT INTO t VALUES(NULL,'bad')", 1))
							}
							run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
							oid := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID")
							catalog, relations := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
							var dump []byte
							if !b.PG {
								dump = addConstraintDump(t, p)
							}
							held := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
							reader := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY note")
							values := [][]any{{int64(1), "old"}}
							if dirty {
								values = [][]any{{nil, "bad"}, {int64(1), "old"}}
							}
							fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, values))
							other := "ALTER a SET NOT NULL"
							if drop {
								other = "DROP CONSTRAINT nn"
							}
							add := "ADD CONSTRAINT pk PRIMARY KEY(a)"
							q := "ALTER TABLE t " + other + ", " + add
							if pkFirst {
								q = "ALTER TABLE t " + add + ", " + other
							}
							d := fixture.ControlsReferenceDiagnostic("55000", `cannot create primary key on column "a"`)
							d.Detail = `The constraint "nn" on column "a" of table "t", marked NOT VALID, is incompatible with a primary key.`
							d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
							addConstraintError(t, p, c, q, d, prepared, "execute")
							addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
							if addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID") != oid {
								t.Fatal("failed PK admission changed original NN identity")
							}
							fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select(reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, values))
							fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, reader.SQL, []string{"a", "note"}, []string{"INT4", "TEXT"}, []bool{false, true}, len(values))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, fixture.PrimaryKeyReferenceNull("t", "a", "null, bad"), nil, "bad"))
							if dirty {
								run(fixture.Exec("UPDATE t SET a=2 WHERE note='bad'", 1))
							}
							step := fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0)
							if prepared {
								step = fixture.Prepared(step)
							}
							run(step)
							var validOID int64
							var definition string
							if err := c.QueryRowContext(p.Ctx, "SELECT oid,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='nn'").Scan(&validOID, &definition); err != nil {
								t.Fatal(err)
							}
							if validOID != oid || definition != "NOT NULL a" {
								t.Fatal("separate validating SET failed to retain/promote original NN", validOID, definition)
							}
							if b.PG {
								run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}, oid))
							}
							step = fixture.Exec("ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a)", 0)
							if prepared {
								step = fixture.Prepared(step)
							}
							run(step)
							replacementOID(t, p, c, "pk")
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, fixture.UniqueReferenceDuplicate("t", "pk", "Key (a)=(1) already exists."), 1, "bad"))
							fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 1, 3, "new"))
							final := [][]any{{int64(1), "old"}, {int64(3), "new"}}
							if dirty {
								final = [][]any{{int64(1), "old"}, {int64(2), "bad"}, {int64(3), "new"}}
							}
							run(fixture.Select("SELECT a,note FROM t ORDER BY a", []string{"a", "note"}, []string{"INT4", "TEXT"}, final))
						})
					})
				}
			}
		}
	}
}
