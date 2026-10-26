package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterValidateConstraintKinds(t *testing.T) {
	validateConstraintLogicalCases(t)
	for _, tc := range validateConstraintCases() {
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				t.Run(tc.name+map[bool]string{false: "/direct", true: "/held"}[prepared]+map[bool]string{false: "/rows", true: "/empty"}[empty], func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := addConstraintSetup(t, p, b, c)
						var want [][]any
						if !empty {
							run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
							want = [][]any{{int64(1), "one"}, {int64(2), "two"}}
							if tc.kind != "n" {
								run(fixture.Exec("INSERT INTO t VALUES(NULL,'unknown')", 1))
								want = append(want, []any{nil, "unknown"})
							}
						}
						run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
						n, def := validateConstraintName(tc), validateConstraintDefinition(tc)
						oid := addConstraintInvalid(t, p, b, c, "t", n, def+" NOT VALID")
						before := validateConstraintSnapshot(t, p, c)
						q := "ALTER TABLE t VALIDATE CONSTRAINT " + n
						held := addConstraintHeld(t, p, b, c, q)
						// Preparing is effect-free even with real historical rows.
						run(validateConstraintFlag("t", n, false))
						consumer := addConstraintHeld(t, p, b, c, "SELECT a,note FROM t ORDER BY a,note")
						validateConstraintHeldRun(t, p, b, consumer, addConstraintRows(want...))
						detached := validateConstraintDetached(t, p, b, c, tc.kind != "n", want)
						if prepared {
							validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
						} else {
							run(fixture.Exec(q, 0))
						}
						validateConstraintIdentity(t, p, b, c, oid, true, def)
						validateConstraintOrigins(t, p, c, before)
						validateConstraintRows(t, p, b, c, tc.kind != "n", want...)
						detached()
						validateConstraintHeldRun(t, p, b, consumer, addConstraintRows(want...))
						validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
						validateConstraintIdentity(t, p, b, c, oid, true, def)
					})
				})
			}
		}
	}
	for _, tc := range validateConstraintCases() {
		for _, reverse := range []bool{false, true} {
			t.Run("same_command/"+tc.name+map[bool]string{false: "/add_first", true: "/validate_first"}[reverse], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
					a, v := "ADD "+tc.clause+" NOT VALID", "VALIDATE CONSTRAINT "+validateConstraintName(tc)
					if reverse {
						a, v = v, a
					}
					run(fixture.Prepared(fixture.Exec("ALTER TABLE t "+a+", "+v, 0)))
					run(validateConstraintFlag("t", validateConstraintName(tc), true))
					// DROP is earlier than misc validation; replacement, not stale identity, wins.
					var old int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conrelid='t'::regclass AND conname=$1", validateConstraintName(tc)).Scan(&old); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT "+validateConstraintName(tc)+", DROP CONSTRAINT "+validateConstraintName(tc)+", ADD "+tc.clause+" NOT VALID", 0))
					run(validateConstraintFlag("t", validateConstraintName(tc), true))
					run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, old))
				})
			})
		}
	}
	t.Run("sparse_composite_cross_width", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(x BIGINT,y INT,PRIMARY KEY(y,x))", 0))
			run(fixture.Exec("INSERT INTO p VALUES(1,2)", 1))
			run(fixture.Exec("CREATE TABLE t(dead TEXT,a INT,b SMALLINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('discard',1,2),(NULL,NULL,NULL)", 2))
			run(fixture.Exec("ALTER TABLE t DROP COLUMN dead", 0))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(b,a) REFERENCES p(y,x) MATCH FULL NOT VALID", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT fk", 0)))
			run(fixture.Select("SELECT a,b FROM t ORDER BY a NULLS LAST", []string{"a", "b"}, []string{"INT4", "INT2"}, [][]any{{int64(1), int64(2)}, {nil, nil}}))
			run(validateConstraintFlag("t", "fk", true))
		})
	})
}

func TestReferenceAlterValidateConstraintHistorical(t *testing.T) {
	for _, tc := range validateConstraintCases() {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES($1,'bad')", 1, tc.bad))
					run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
					n, def := validateConstraintName(tc), validateConstraintDefinition(tc)
					oid := addConstraintInvalid(t, p, b, c, "t", n, def+" NOT VALID")
					q := "ALTER TABLE t VALIDATE CONSTRAINT " + n
					held := addConstraintHeld(t, p, b, c, q)
					before, relations := validateConstraintSnapshot(t, p, c), addConstraintRelations(t, p, c)
					detached := validateConstraintDetached(t, p, b, c, tc.kind != "n", [][]any{{tc.bad, "bad"}})
					var dump []byte
					if !b.PG {
						dump = addConstraintDump(t, p)
					}
					if prepared {
						validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(q, tc.build))
					} else {
						addConstraintError(t, p, c, q, tc.build, false, "execute")
					}
					detached()
					if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) || !reflect.DeepEqual(relations, addConstraintRelations(t, p, c)) {
						t.Fatal("failed validation changed metadata/backings")
					}
					if !b.PG && !bytes.Equal(dump, addConstraintDump(t, p)) {
						t.Fatal("failed validation changed dump")
					}
					validateConstraintRows(t, p, b, c, tc.kind != "n", []any{tc.bad, "bad"})
					validateConstraintIdentity(t, p, b, c, oid, false, def+" NOT VALID")
					run(fixture.Exec("UPDATE t SET a=1", 1))
					validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
					validateConstraintIdentity(t, p, b, c, oid, true, def)
					validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "bad"})
				})
			})
		}
	}
	t.Run("match_full_partial_null", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", 0))
			run(fixture.Exec("CREATE TABLE t(a INT,b INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,NULL)", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p MATCH FULL NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT fk")
			d := addConstraintFK("t", "fk", "a, b", "1, null", "p")
			d.Detail = "MATCH FULL does not allow mixing of null and nonnull key values."
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, d))
			run(validateConstraintFlag("t", "fk", false))
			run(fixture.Exec("DELETE FROM t", 1))
			run(fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, nil}}))
		})
	})
	t.Run("selected_only", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT clean CHECK(a>0) NOT VALID, ADD CONSTRAINT dirty CHECK(a>9) NOT VALID, ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t VALIDATE CONSTRAINT clean", 0)))
			run(fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"clean", true}, {"dirty", false}, {"fk", false}}))
			run(addConstraintRows([]any{int64(1), "old"}))
		})
	})
}

func TestReferenceAlterValidateConstraintNoOp(t *testing.T) {
	for _, tc := range validateConstraintCases() {
		t.Run(tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				run(fixture.Exec("ALTER TABLE t ADD "+tc.clause+" NOT VALID", 0))
				q := "ALTER TABLE t VALIDATE CONSTRAINT " + validateConstraintName(tc)
				held := addConstraintHeld(t, p, b, c, q)
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
				before, relations := validateConstraintSnapshot(t, p, c), addConstraintRelations(t, p, c)
				var dump []byte
				if !b.PG {
					dump = addConstraintDump(t, p)
				}
				run(fixture.Exec(q, 0))
				validateConstraintHeldRun(t, p, b, held, fixture.Exec(q, 0))
				if !reflect.DeepEqual(before, validateConstraintSnapshot(t, p, c)) || !reflect.DeepEqual(relations, addConstraintRelations(t, p, c)) {
					t.Fatal("no-op changed metadata")
				}
				if !b.PG && !bytes.Equal(dump, addConstraintDump(t, p)) {
					t.Fatal("no-op changed dump")
				}
				validateConstraintRows(t, p, b, c, tc.kind != "n", []any{int64(1), "one"})
			})
		})
	}
	t.Run("observable_check_scan", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("CREATE SEQUENCE scans", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(2,'two')", 2))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ck CHECK(nextval('scans')>0) NOT VALID", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t VALIDATE CONSTRAINT ck")
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(fixture.Select("SELECT last_value FROM scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
			run(fixture.Select("SELECT last_value FROM scans", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
}
