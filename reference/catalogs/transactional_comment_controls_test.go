package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentClusterControls(t *testing.T) {
	t.Run("cluster-visibility", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tx_comment_role"})
		databases := []string{"tx_comment_other"}
		fixture.DatabaseReferenceCleanup(t, &databases)
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(q string) { fixture.LockExec(t, p.Ctx, c, q, 0) }
				run("CREATE DATABASE tx_comment_other")
				run("CREATE ROLE tx_comment_role")
				run("COMMENT ON ROLE tx_comment_role IS 'old'")
				other, _, rec := fixture.DatabaseScenarioPool(t, p, b.PG, "tx_comment_other", "")
				k := transactionalCommentKinds[5]
				check := func(want any) {
					fixture.RunStep(t, p.Ctx, other, rec, fixture.Select("SELECT "+k.probe+" AS text", []string{"text"}, []string{"TEXT"}, [][]any{{want}}))
				}
				q := "COMMENT ON ROLE tx_comment_role IS 'private'"
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				for _, commit := range []bool{false, true} {
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if _, err := s.ExecContext(p.Ctx); err != nil {
						t.Fatal(err)
					}
					transactionalCommentValue(t, p, b, tx, k, "private")
					transactionalCommentValue(t, p, b, peer, k, "old")
					check("old")
					fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT shared", 0)
					fixture.LockExec(t, p.Ctx, tx, "COMMENT ON ROLE tx_comment_role IS NULL", 0)
					check("old")
					fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO shared", 0)
					transactionalCommentValue(t, p, b, tx, k, "private")
					fixture.LockExec(t, p.Ctx, tx, "RELEASE shared", 0)
					if commit {
						if err := tx.Commit(); err != nil {
							t.Fatal(err)
						}
						check("private")
					} else {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						check("old")
					}
				}
			})
		}
	})
	for _, replacement := range []string{"reset", "restore", "rollback-to", "failed-restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := transactionalCommentScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(q string) { fixture.LockExec(t, p.Ctx, c, q, 0) }
					for _, q := range []string{"CREATE ROLE tx_comment_role", "CREATE TABLE tx_comment(a INT)", "CREATE SEQUENCE tx_comment_sequence", "COMMENT ON TABLE tx_comment IS 'old'", "COMMENT ON ROLE tx_comment_role IS 'old'"} {
						run(q)
					}
					fixture.LockExec(t, p.Ctx, c, "INSERT INTO tx_comment VALUES(7)", 1)
					var dump string
					if !b.PG {
						dump = commentInquiryDump(t, p)
					}
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					for _, q := range []string{"COMMENT ON TABLE tx_comment IS 'private local'", "COMMENT ON ROLE tx_comment_role IS 'private shared'", "SAVEPOINT private"} {
						fixture.LockExec(t, p.Ctx, tx, q, 0)
					}
					transactionalCommentValue(t, p, b, tx, transactionalCommentKinds[0], "private local")
					transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "old")
					transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[5], "old")
					if !b.PG {
						if got := commentInquiryDump(t, p); got != dump || strings.Contains(got, "private") || strings.Contains(got, "COMMENT ON ROLE") {
							t.Fatal("Dump leaked transaction", got)
						}
						if replacement == "failed-restore" {
							err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"COMMENT ON COLUMN tx_comment.missing IS 'bad';"))
							want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" of relation "tx_comment" does not exist`)
							fixture.AssertDiagnostic(t, err, &want)
							if err := tx.Commit(); err != nil {
								t.Fatal(err)
							}
							transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "private local")
							transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[5], "private shared")
							committed := commentInquiryDump(t, p)
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(committed)); err != nil {
								t.Fatal(err)
							}
							transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "private local")
							transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[5], "private shared")
							return
						}
						if replacement == "reset" {
							err = stuntgres.Reset(p.Ctx, p.Native)
						} else {
							err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump))
						}
						if err != nil {
							t.Fatal(err)
						}
						if replacement == "rollback-to" {
							_, err = tx.ExecContext(p.Ctx, "ROLLBACK TO private")
							fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
						} else {
							fixture.TransactionOrdinary(t, tx.Commit(), "stuntgres: catalog replaced before publication")
						}
						transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[5], "old")
						if replacement == "reset" {
							if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
								t.Fatal(err)
							}
						}
					} else {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "old")
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT a FROM tx_comment", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
