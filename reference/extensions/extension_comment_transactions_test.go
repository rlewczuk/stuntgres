package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionCommentTransactions(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			t.Run(fmt.Sprintf("held=%v/goTx=%v", held, goTx), func(t *testing.T) {
				extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
					oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
					for _, v := range []struct {
						sql  string
						want any
					}{{"E'private 雪\\ntext'", "private 雪\ntext"}, {"NULL", nil}, {"''", nil}, {"' '", " "}} {
						q := "COMMENT ON EXTENSION fuzzystrmatch IS " + v.sql
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						for _, outcome := range []struct {
							priorSQL string
							prior    any
							commit   bool
						}{{"'old'", "old", false}, {"'old'", "old", true}, {"NULL", nil, false}} {
							commit := outcome.commit
							run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS "+outcome.priorSQL, 0))
							var x fixture.Executor = c
							finish := func() {
								end := "ROLLBACK"
								if commit {
									end = "COMMIT"
								}
								run(fixture.Exec(end, 0))
							}
							if goTx {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								x = tx
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
								run(fixture.Exec("BEGIN", 0))
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							execute := x
							if held {
								execute = fixture.HeldStatement{Executor: x, Statement: stmt, SQL: q}
							}
							fixture.RunStep(t, p.Ctx, execute, b.Recorder, fixture.Exec(q, 0))
							extensionCommentValue(t, p, b, x, oid, v.want)
							extensionCommentValue(t, p, b, peer, oid, outcome.prior)
							finish()
							if current := extensionCommentOID(t, p, peer, "fuzzystrmatch"); current != oid {
								t.Fatal("outer transaction changed existing extension identity", oid, current)
							}
							want := outcome.prior
							if commit {
								want = v.want
							}
							extensionCommentValue(t, p, b, peer, oid, want)
						}
						stmt.Close()
					}
					for _, prior := range []struct {
						sql  string
						want any
					}{{"'earlier'", "earlier"}, {"NULL", nil}} {
						run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS "+prior.sql, 0))
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("SAVEPOINT mark", 0))
						run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'private'", 0))
						if prior.want != nil {
							run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS NULL", 0))
						}
						run(fixture.RoleReferenceError("COMMENT ON EXTENSION missing_comment_ext IS 'bad'", "42704", `extension "missing_comment_ext" does not exist`, "", false, "execute"))
						run(fixture.RoleReferenceError("COMMENT ON EXTENSION fuzzystrmatch IS 'aborted'", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
						run(fixture.Exec("ROLLBACK TO mark", 0))
						extensionCommentValue(t, p, b, c, oid, prior.want)
						run(fixture.Exec("RELEASE mark", 0))
						run(fixture.Exec("COMMIT", 0))
						extensionCommentValue(t, p, b, peer, oid, prior.want)
					}
					run(fixture.Exec("DROP EXTENSION fuzzystrmatch", 0))
					for _, commit := range []bool{false, true} {
						run(fixture.Exec("BEGIN", 0))
						run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
						run(fixture.Exec("COMMENT ON EXTENSION fuzzystrmatch IS 'created privately'", 0))
						privateOID := extensionCommentOID(t, p, c, "fuzzystrmatch")
						extensionCommentValue(t, p, b, c, privateOID, "created privately")
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						run(fixture.Exec(end, 0))
						want := any(nil)
						if commit {
							want = "created privately"
						}
						extensionCommentValue(t, p, b, peer, privateOID, want)
					}
				})
			})
		}
	}
}

func TestReferenceExtensionCommentCancellation(t *testing.T) {
	extensionCommentCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn) {
		if b.PG {
			return // Pre-canceled ordinary Go errors are the native adapter contract.
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		oid := extensionCommentOID(t, p, c, "fuzzystrmatch")
		q := "COMMENT ON EXTENSION fuzzystrmatch IS 'canceled'"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		_, err = c.ExecContext(ctx, q)
		fixture.DriverOrdinaryError(t, err, context.Canceled)
		_, err = stmt.ExecContext(ctx)
		fixture.DriverOrdinaryError(t, err, context.Canceled)
		extensionCommentValue(t, p, b, c, oid, extensionCommentDefault)
		extensionCommentValue(t, p, b, peer, oid, extensionCommentDefault)
	})
}
