package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentKinds(t *testing.T) {
	for _, k := range transactionalCommentKinds {
		for _, held := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/held=%v/goTx=%v", k.name, held, goTx), func(t *testing.T) {
					p := transactionalCommentScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							transactionalCommentSetup(base)
							defer transactionalCommentCleanup(t, p, b)
							for _, v := range []struct {
								sql  string
								want any
							}{{"E'new 雪\\ntext'", "new 雪\ntext"}, {"NULL", nil}, {"''", nil}, {"' '", " "}} {
								q := "COMMENT ON " + k.target + " IS " + v.sql
								var statement fixture.HeldStatement
								if held {
									s, err := c.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									defer s.Close()
									statement = fixture.HeldStatement{Statement: s, SQL: q}
								}
								var e fixture.Executor = c
								execute := func() {
									x := e
									if held {
										statement.Executor = e
										x = statement
									}
									fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(q, 0))
									fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(q, []string{}, []string{}, [][]any{}))
								}
								check := func(x fixture.Executor, want any) {
									transactionalCommentValue(t, p, b, x, k, want)
									fixture.RunStep(t, p.Ctx, x, b.Recorder, transactionalCommentCatalogRows(k, want != nil))
								}
								base(fixture.Exec("COMMENT ON "+k.target+" IS 'old'", 0))
								execute()
								check(peer, v.want)
								for _, commit := range []bool{false, true} {
									// Each transaction starts from nonempty committed text;
									// its final mutation is the selected value, including removal.
									base(fixture.Exec("COMMENT ON "+k.target+" IS 'old'", 0))
									check(peer, "old")
									finish := func() {}
									if goTx {
										tx, err := c.BeginTx(p.Ctx, nil)
										if err != nil {
											t.Fatal(err)
										}
										defer tx.Rollback()
										e = tx
										finish = func() {
											var err error
											if commit {
												err = tx.Commit()
											} else {
												err = tx.Rollback()
											}
											if err != nil {
												t.Fatal(err)
											}
										}
									} else {
										base(fixture.Exec("BEGIN", 0))
										defer c.ExecContext(p.Ctx, "ROLLBACK")
										e = c
										finish = func() {
											end := "ROLLBACK"
											if commit {
												end = "COMMIT"
											}
											base(fixture.Exec(end, 0))
										}
									}
									execute()
									check(e, v.want)
									check(peer, "old")
									finish()
									want := any("old")
									if commit {
										want = v.want
									}
									check(peer, want)
								}
							}
							base(fixture.Select("SELECT a,b FROM tx_comment", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "sentinel"}}))
							base(fixture.Select("SELECT relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned,relacl IS NULL AS no_acl FROM pg_class WHERE oid='tx_comment'::regclass", []string{"owned", "no_acl"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
						})
					}
				})
			}
		}
	}
}

func TestReferenceTransactionalCommentSavepoints(t *testing.T) {
	for _, k := range transactionalCommentKinds {
		t.Run(k.name, func(t *testing.T) {
			p := transactionalCommentScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					transactionalCommentSetup(run)
					defer transactionalCommentCleanup(t, p, b)
					q := "COMMENT ON " + k.target + " IS 'earlier'"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					for _, commit := range []bool{false, true} {
						run(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						run(fixture.Exec("INSERT INTO tx_comment_rows VALUES(7)", 1))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
						run(fixture.Exec("SAVEPOINT mark", 0))
						for range 2 {
							run(fixture.Exec("COMMENT ON "+k.target+" IS NULL", 0))
							run(fixture.Exec("INSERT INTO tx_comment_rows VALUES(8)", 1))
							run(fixture.RoleReferenceError("COMMENT ON COLUMN tx_comment.missing IS 'bad'", "42703", `column "missing" of relation "tx_comment" does not exist`, "", false, "execute"))
							run(fixture.RoleReferenceError("COMMENT ON "+k.target+" IS 'aborted'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
							run(fixture.Exec("ROLLBACK TO mark", 0))
							transactionalCommentValue(t, p, b, c, k, "earlier")
							run(fixture.Select("SELECT a FROM tx_comment_rows", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						}
						run(fixture.Exec("RELEASE mark", 0))
						transactionalCommentValue(t, p, b, peer, k, "old")
						if commit {
							fixture.LockExec(t, p.Ctx, peer, "INSERT INTO tx_comment_rows VALUES(9)", 1)
							run(fixture.Exec("COMMIT", 0))
							transactionalCommentValue(t, p, b, peer, k, "earlier")
							run(fixture.Select("SELECT a FROM tx_comment_rows ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}))
						} else {
							run(fixture.Exec("ROLLBACK", 0))
							transactionalCommentValue(t, p, b, peer, k, "old")
							run(fixture.Select("SELECT a FROM tx_comment_rows", []string{"a"}, []string{"INT4"}, [][]any{}))
						}
					}
					run(fixture.Exec("COMMENT ON "+k.target+" IS NULL", 0))
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("SAVEPOINT absent", 0))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
					transactionalCommentValue(t, p, b, c, k, "earlier")
					transactionalCommentValue(t, p, b, peer, k, nil)
					run(fixture.Exec("ROLLBACK TO absent", 0))
					transactionalCommentValue(t, p, b, c, k, nil)
					run(fixture.Exec("RELEASE absent", 0))
					run(fixture.Exec("COMMIT", 0))
					transactionalCommentValue(t, p, b, peer, k, nil)
				})
			}
		})
	}
}

func transactionalCommentCatalogRows(k transactionalCommentKind, present bool) fixture.Step {
	count := int64(0)
	if present {
		count = 1
	}
	oid, class, sub := "'tx_comment'::regclass", "'pg_class'::regclass", "0"
	switch k.name {
	case "column":
		sub = "1"
	case "foreign", "foreign_column":
		oid = "'tx_comment_foreign'::regclass"
		if k.name == "foreign_column" {
			sub = "1"
		}
	case "schema":
		oid, class = "'tx_comment_schema'::regnamespace", "'pg_namespace'::regclass"
	case "sequence":
		oid = "'tx_comment_sequence'::regclass"
	case "role":
		return fixture.Select("SELECT count(*) FROM pg_shdescription WHERE classoid=1260 AND objoid=(SELECT oid FROM pg_roles WHERE rolname='tx_comment_role')", []string{"count"}, []string{"INT8"}, [][]any{{count}})
	}
	return fixture.Select("SELECT count(*) FROM pg_description WHERE classoid="+class+" AND objoid="+oid+" AND objsubid="+sub, []string{"count"}, []string{"INT8"}, [][]any{{count}})
}
