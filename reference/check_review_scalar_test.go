package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						conn, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
						expr := provider.expr
						if provider.name == "shared" && b.pg {
							var oid int64
							var prior sql.NullString
							if err := conn.QueryRowContext(p.ctx, "SELECT oid,shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='postgres'").Scan(&oid, &prior); err != nil || oid <= 0 || oid > 1<<32-1 {
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
						run(referenceExec("CREATE TABLE anchor(a INT)", 0))
						run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
						run(referenceExec("CREATE TABLE t(a TEXT DEFAULT 'old' CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
						insertQ, updateQ := "INSERT INTO t VALUES($1)", "UPDATE t SET a=$1"
						insert, err := conn.PrepareContext(p.ctx, insertQ)
						if err != nil {
							t.Fatal(err)
						}
						defer insert.Close()
						update, err := conn.PrepareContext(p.ctx, updateQ)
						if err != nil {
							t.Fatal(err)
						}
						defer update.Close()
						heldInsert := referenceHeldStatement{referenceExecutor: conn, statement: insert, query: insertQ}
						heldUpdate := referenceHeldStatement{referenceExecutor: conn, statement: update, query: updateQ}
						held := func(x referenceExecutor, s referenceStep) { referenceCheckRun(t, p.ctx, x, b.recorder, s) }
						held(conn, referenceExec("INSERT INTO t VALUES('old')", 1))
						held(heldInsert, referenceExec(insertQ, 1, "old"))
						run(referenceExec("COMMENT ON "+provider.target+" IS 'new'", 0))
						held(conn, referenceImplicitError("INSERT INTO t VALUES('old')", referenceCheckViolation("t", "c", "old")))
						held(conn, referencePrepared(referenceImplicitError(insertQ, referenceCheckViolation("t", "c", "old"), "old")))
						held(heldInsert, referenceImplicitError(insertQ, referenceCheckViolation("t", "c", "old"), "old"))
						held(heldInsert, referenceExec(insertQ, 1, "new"))
						held(conn, referenceExec("UPDATE t SET a='new'", 3))
						held(conn, referenceImplicitError("UPDATE t SET a='old'", referenceCheckViolation("t", "c", "old")))
						held(conn, referencePrepared(referenceImplicitError(updateQ, referenceCheckViolation("t", "c", "old"), "old")))
						held(heldUpdate, referenceImplicitError(updateQ, referenceCheckViolation("t", "c", "old"), "old"))
						held(heldUpdate, referenceExec(updateQ, 3, "new"))
						run(referenceExec("INSERT INTO t SELECT 'new'", 1))
						run(referenceExec("INSERT INTO t VALUES('new') ORDER BY 1", 1))
						run(referenceImplicitError("INSERT INTO t SELECT 'old'", referenceCheckViolation("t", "c", "old")))
						run(referenceImplicitError("INSERT INTO t VALUES('old') ORDER BY 1", referenceCheckViolation("t", "c", "old")))
						held(conn, referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1) ORDER BY 1", referenceCheckViolation("t", "c", "old"), "old")))
						s := referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"new"}, {"new"}, {"new"}, {"new"}, {"new"}})
						s.bag = true
						run(s)
						run(referenceExec("COMMENT ON "+provider.target+" IS 'old'", 0))
						held(heldInsert, referenceExec(insertQ, 1, "old"))
						held(heldUpdate, referenceExec(updateQ, 6, "old"))
						run(referenceExec("INSERT INTO t DEFAULT VALUES", 1))
						s = referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}, {"old"}})
						s.bag = true
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
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK("+expr+" IS NULL AND a=1))", 0))
						s := referenceExec("INSERT INTO t DEFAULT VALUES", 1)
						s.prepared = prepared
						run(s)
						if !b.pg {
							referenceCheckRoundtrip(t, p, referenceCheckDump(t, p))
						}
						run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
						run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "2"), 2)))
					})
				}
			})
		}
	}
}

func TestReferenceCheckReviewScalarPrivateCommentBoundary(t *testing.T) {
	for _, target := range []string{"TABLE anchor", "COLUMN anchor.a"} {
		t.Run(target, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					conn, err := b.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					defer conn.ExecContext(p.ctx, "ROLLBACK")
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
					expr := "obj_description('anchor'::regclass,'pg_class')"
					if target == "COLUMN anchor.a" {
						expr = "col_description('anchor'::regclass,1)"
					}
					run(referenceExec("CREATE TABLE anchor(a INT)", 0))
					run(referenceExec("COMMENT ON "+target+" IS 'old'", 0))
					run(referenceExec("CREATE TABLE t(a TEXT CONSTRAINT c CHECK("+expr+" IS NOT DISTINCT FROM a))", 0))
					q := "INSERT INTO t VALUES($1)"
					stmt, err := conn.PrepareContext(p.ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
					run(referenceExec("BEGIN", 0))
					if b.pg {
						run(referenceExec("COMMENT ON "+target+" IS 'new'", 0))
						referenceCheckRun(t, p.ctx, held, b.recorder, referenceExec(q, 1, "new"))
						run(referencePrepared(referenceExec("UPDATE t SET a=$1", 1, "new")))
					} else {
						run(referenceImplicitError("COMMENT ON "+target+" IS 'new'", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
					}
					run(referenceExec("ROLLBACK", 0))
					run(referenceImplicitError("INSERT INTO t VALUES('new')", referenceCheckViolation("t", "c", "new")))
					referenceCheckRun(t, p.ctx, held, b.recorder, referenceExec(q, 1, "old"))
					run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"TEXT"}, [][]any{{"old"}}))
				})
			}
		})
	}
}
