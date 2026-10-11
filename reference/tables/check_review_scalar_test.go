package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckReviewScalarCommentDML(t *testing.T) {
	for _, provider := range []struct{ name, expr, target string }{
		{"object", "obj_description('anchor'::regclass,'pg_class')", "TABLE anchor"},
		{"legacy", "obj_description('anchor'::regclass)", "TABLE anchor"},
		{"column", "col_description('anchor'::regclass,1)", "COLUMN anchor.a"},
		{"shared", "shobj_description(10,'pg_authid')", "ROLE postgres"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", provider.name, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
						expr := provider.expr
						if provider.name == "shared" && b.PG {
							var oid int64
							var prior sql.NullString
							if err := conn.QueryRowContext(p.Ctx, "SELECT oid,shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='postgres'").Scan(&oid, &prior); err != nil || oid <= 0 || oid > 1<<32-1 {
								t.Fatal(oid, err)
							}
							expr = fmt.Sprintf("shobj_description(%d,'pg_authid')", oid)
							defer func() {
								value := "NULL"
								if prior.Valid {
									value = "'" + strings.ReplaceAll(prior.String, "'", "''") + "'"
								}
								if _, err := conn.ExecContext(context.Background(), "COMMENT ON ROLE postgres IS "+value); err != nil {
									t.Error(err)
								}
							}()
						}
						run(fixture.Exec("CREATE TABLE anchor(a INT)", 0))
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(fixture.Exec("CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
						insertQ, updateQ := "INSERT INTO t VALUES($1)", "UPDATE t SET a=$1"
						insert, err := conn.PrepareContext(p.Ctx, insertQ)
						if err != nil {
							t.Fatal(err)
						}
						defer insert.Close()
						update, err := conn.PrepareContext(p.Ctx, updateQ)
						if err != nil {
							t.Fatal(err)
						}
						defer update.Close()
						heldInsert := fixture.HeldStatement{Executor: conn, Statement: insert, SQL: insertQ}
						heldUpdate := fixture.HeldStatement{Executor: conn, Statement: update, SQL: updateQ}
						held := func(x fixture.Executor, s fixture.Step) { fixture.CheckRun(t, p.Ctx, x, b.Recorder, s) }
						held(conn, fixture.Exec("INSERT INTO t VALUES('old')", 1))
						held(heldInsert, fixture.Exec(insertQ, 1, "old"))
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'new'", 0))
						held(conn, fixture.ImplicitError("INSERT INTO t VALUES('old')", fixture.CheckViolation("t", "c", "old")))
						held(conn, fixture.Prepared(fixture.ImplicitError(insertQ, fixture.CheckViolation("t", "c", "old"), "old")))
						held(heldInsert, fixture.ImplicitError(insertQ, fixture.CheckViolation("t", "c", "old"), "old"))
						held(heldInsert, fixture.Exec(insertQ, 1, "new"))
						held(conn, fixture.Exec("UPDATE t SET a='new'", 3))
						held(conn, fixture.ImplicitError("UPDATE t SET a='old'", fixture.CheckViolation("t", "c", "old")))
						held(conn, fixture.Prepared(fixture.ImplicitError(updateQ, fixture.CheckViolation("t", "c", "old"), "old")))
						held(heldUpdate, fixture.ImplicitError(updateQ, fixture.CheckViolation("t", "c", "old"), "old"))
						held(heldUpdate, fixture.Exec(updateQ, 3, "new"))
						run(fixture.Exec("INSERT INTO t SELECT 'new'", 1))
						run(fixture.Exec("INSERT INTO t VALUES('new') ORDER BY 1", 1))
						run(fixture.ImplicitError("INSERT INTO t SELECT 'old'", fixture.CheckViolation("t", "c", "old")))
						run(fixture.ImplicitError("INSERT INTO t VALUES('old') ORDER BY 1", fixture.CheckViolation("t", "c", "old")))
						held(conn, fixture.Prepared(fixture.ImplicitError("INSERT INTO t VALUES($1) ORDER BY 1", fixture.CheckViolation("t", "c", "old"), "old")))
						s := fixture.Select("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"new"}, {"new"}, {"new"}, {"new"}, {"new"}})
						s.Bag = true
						run(s)
						run(fixture.Exec("COMMENT ON "+provider.target+" IS 'old'", 0))
						held(heldInsert, fixture.Exec(insertQ, 1, "old"))
						held(heldUpdate, fixture.Exec(updateQ, 6, "old"))
						run(fixture.Exec("INSERT INTO t DEFAULT VALUES", 1))
						s = fixture.Select("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}})
						s.Bag = true
						run(s)
					})
				}
			})
		}
	}
}

func TestReferenceCheckReviewScalarNonNullMissingOID(t *testing.T) {
	for _, expr := range []string{"obj_description(0,'pg_class')", "obj_description(0)", "col_description(0,1)", "shobj_description(0,'pg_authid')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", expr, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))", 0))
						s := fixture.Exec("INSERT INTO t DEFAULT VALUES", 1)
						s.Prepared = prepared
						run(s)
						if !b.PG {
							fixture.CheckRoundtrip(t, p, fixture.CheckDump(t, p))
						}
						run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
						run(fixture.Prepared(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "c", "2"), 2)))
					})
				}
			})
		}
	}
}

func TestReferenceCheckReviewScalarPrivateCommentBoundary(t *testing.T) {
	for _, target := range []string{"TABLE anchor", "COLUMN anchor.a"} {
		t.Run(target, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.Ctx, "ROLLBACK")
					run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
					expr := "obj_description('anchor'::regclass,'pg_class')"
					if target == "COLUMN anchor.a" {
						expr = "col_description('anchor'::regclass,1)"
					}
					run(fixture.Exec("CREATE TABLE anchor(a INT)", 0))
					run(fixture.Exec("COMMENT ON "+target+" IS 'old'", 0))
					run(fixture.Exec("CREATE TABLE t(a TEXT CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
					q := "INSERT INTO t VALUES($1)"
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					run(fixture.Exec("BEGIN", 0))
					if b.PG {
						run(fixture.Exec("COMMENT ON "+target+" IS 'new'", 0))
						fixture.CheckRun(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, "new"))
						run(fixture.Prepared(fixture.Exec("UPDATE t SET a=$1", 1, "new")))
					} else {
						run(fixture.ImplicitError("COMMENT ON "+target+" IS 'new'", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.ImplicitError("INSERT INTO t VALUES('new')", fixture.CheckViolation("t", "c", "new")))
					fixture.CheckRun(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, "old"))
					run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}}))
				})
			}
		})
	}
}
