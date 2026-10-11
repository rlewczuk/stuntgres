package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 tablecmds.c dispatches each DROP with its own behavior. dependency.c
// reports the current constraint subject, while the FK depends on its index.
// Previously retired ua must neither acquire ub's dependency nor authorize it.
func TestReferenceAlterAddConstraintMixedDropModes(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		for _, addFirst := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("success_%v/add_first_%v/prepared_%v", succeeds, addFirst, prepared), func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, step) }
						run(fixture.Exec("CREATE TABLE t(a INT,b INT,CONSTRAINT ua UNIQUE(a),CONSTRAINT ub UNIQUE(b))", 0))
						run(fixture.Exec("CREATE TABLE c(b INT CONSTRAINT cfk REFERENCES t(b))", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
						run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
						ua, uaIndex := replacementOID(t, p, c, "ua")
						ub, ubIndex := replacementOID(t, p, c, "ub")
						var cfk int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='cfk'").Scan(&cfk); err != nil || cfk <= 0 {
							t.Fatal("missing actual child FK identity", cfk, err)
						}
						catalog, relations := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
						var dump []byte
						if !b.PG {
							dump = addConstraintDump(t, p)
						}
						parent := addConstraintHeld(t, p, b, c, "INSERT INTO t VALUES($1,$2)")
						child := addConstraintHeld(t, p, b, c, "INSERT INTO c VALUES($1)")
						parentReader := addConstraintHeld(t, p, b, c, "SELECT a,b FROM t ORDER BY a,b")
						childReader := addConstraintHeld(t, p, b, c, "SELECT b FROM c ORDER BY b")
						parentRows := func(values [][]any) {
							fixture.RunStep(t, p.Ctx, parentReader, b.Recorder, fixture.Select(parentReader.SQL, []string{"a", "b"}, []string{"INT4", "INT4"}, values))
						}
						childRows := func(values [][]any) {
							fixture.RunStep(t, p.Ctx, childReader, b.Recorder, fixture.Select(childReader.SQL, []string{"b"}, []string{"INT4"}, values))
						}
						parentRows([][]any{{int64(1), int64(1)}})
						childRows([][]any{{int64(1)}})
						first, second := "RESTRICT", "CASCADE"
						if !succeeds {
							first, second = "CASCADE", "RESTRICT"
						}
						drops := "DROP CONSTRAINT ua " + first + ",DROP CONSTRAINT ub " + second
						add := "ADD CONSTRAINT ua UNIQUE(a)"
						query := "ALTER TABLE t " + drops + "," + add
						if addFirst {
							query = "ALTER TABLE t " + add + "," + drops
						}
						if succeeds {
							step := fixture.Exec(query, 0)
							if prepared {
								step = fixture.Prepared(step)
							}
							run(step)
							newUA, newIndex := replacementOID(t, p, c, "ua")
							if newUA == ua || newIndex == uaIndex {
								t.Fatal("replacement reused original identities", newUA, newIndex)
							}
							run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass::oid ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"ua", "u", "UNIQUE (a)"}}))
							run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
							run(fixture.Select("SELECT relname FROM pg_class WHERE oid=$1 OR oid=$2", []string{"relname"}, []string{"NAME"}, nil, uaIndex, ubIndex))
							fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.ErrorDiagnostic(parent.SQL, fixture.UniqueReferenceDuplicate("t", "ua", "Key (a)=(1) already exists."), 1, 2))
							fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.Exec(parent.SQL, 1, 2, 1))
							fixture.RunStep(t, p.Ctx, child, b.Recorder, fixture.Exec(child.SQL, 1, 9))
							parentRows([][]any{{int64(1), int64(1)}, {int64(2), int64(1)}})
							childRows([][]any{{int64(1)}, {int64(9)}})
						} else {
							want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop constraint ub on table t because other objects depend on it")
							want.Detail = "constraint cfk on table c depends on index ub"
							want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
							addConstraintError(t, p, c, query, want, prepared, "execute")
							addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
							gotUA, gotUAIndex := replacementOID(t, p, c, "ua")
							gotUB, gotUBIndex := replacementOID(t, p, c, "ub")
							if gotUA != ua || gotUAIndex != uaIndex || gotUB != ub || gotUBIndex != ubIndex {
								t.Fatal("failed mixed DROP changed original identities")
							}
							var gotCFK int64
							if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='c'::regclass::oid AND conname='cfk'").Scan(&gotCFK); err != nil || gotCFK != cfk {
								t.Fatal("failed mixed DROP changed child FK identity", gotCFK, err)
							}
							run(fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='c'::regclass::oid", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, [][]any{{"cfk", "f", "FOREIGN KEY (b) REFERENCES t(b)"}}))
							parentRows([][]any{{int64(1), int64(1)}})
							childRows([][]any{{int64(1)}})
							fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, parentReader.SQL, []string{"a", "b"}, []string{"INT4", "INT4"}, []bool{true, true}, 1)
							fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, true, childReader.SQL, []string{"b"}, []string{"INT4"}, []bool{true}, 1)
							fixture.RunStep(t, p.Ctx, child, b.Recorder, fixture.ErrorDiagnostic(child.SQL, addConstraintFK("c", "cfk", "b", "9", "t"), 9))
							fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.ErrorDiagnostic(parent.SQL, fixture.UniqueReferenceDuplicate("t", "ub", "Key (b)=(1) already exists."), 2, 1))
							fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.Exec(parent.SQL, 1, 2, 2))
							fixture.RunStep(t, p.Ctx, child, b.Recorder, fixture.Exec(child.SQL, 1, 2))
							parentRows([][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
							childRows([][]any{{int64(1)}, {int64(2)}})
						}
					})
				})
			}
		}
	}
}
