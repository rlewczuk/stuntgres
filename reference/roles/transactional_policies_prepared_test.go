package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalPoliciesPrepared(t *testing.T) {
	for _, mode := range []string{"sql", "go"} {
		t.Run(mode, func(t *testing.T) {
			transactionalPolicyCases(t, func(s *transactionalPolicies) {
				s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2) WITH CHECK(allowed)")
				oid := s.oid("t", "p")
				metadataStep := pgPolicyRow("t", "p", "*", true, "tp_reader", "(id <= 2)", "allowed")
				metadata, err := s.c.PrepareContext(s.p.Ctx, metadataStep.Query)
				if err != nil {
					t.Fatal("held metadata Prepare", err)
				}
				defer metadata.Close()
				readQ := "SELECT id FROM t ORDER BY id"
				insertQ := "INSERT INTO t VALUES($1,$2)"
				updateQ := "UPDATE t SET allowed=TRUE WHERE id=$1"
				read, err := s.c.PrepareContext(s.p.Ctx, readQ)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				insert, err := s.c.PrepareContext(s.p.Ctx, insertQ)
				if err != nil {
					t.Fatal(err)
				}
				defer insert.Close()
				update, err := s.c.PrepareContext(s.p.Ctx, updateQ)
				if err != nil {
					t.Fatal(err)
				}
				defer update.Close()
				for _, commit := range []bool{false, true} {
					finish := s.begin(mode)
					r, i, u := read, insert, update
					m := metadata
					// Tx.StmtContext is a transaction-bound view of the same prepared
					// SQL, not a fresh Prepare replacing the lifetime witness.
					// SQL ownership permits direct reuse; Go ownership binds handles below.
					if mode == "go" {
						tx := s.x.(*sql.Tx)
						r = tx.StmtContext(s.p.Ctx, read)
						i = tx.StmtContext(s.p.Ctx, insert)
						u = tx.StmtContext(s.p.Ctx, update)
						m = tx.StmtContext(s.p.Ctx, metadata)
						defer m.Close()
						defer r.Close()
						defer i.Close()
						defer u.Close()
					}
					insideMetadata, err := s.x.PrepareContext(s.p.Ctx, metadataStep.Query)
					if err != nil {
						t.Fatal("inside metadata Prepare", err)
					}
					defer insideMetadata.Close()
					heldRead := func(ids ...int64) {
						rows := make([][]any, 0, len(ids))
						for _, id := range ids {
							rows = append(rows, []any{id})
						}
						fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: r, SQL: readQ}, s.b.Recorder, fixture.Select(readQ, []string{"id"}, []string{"INT4"}, rows))
					}
					heldMetadata := func(using string, sameRoles, present bool) {
						step := pgPolicyRow("t", "p", "*", true, "tp_reader", using, "allowed")
						step.Rows[0][5] = sameRoles
						if !present {
							step.Rows = [][]any{}
						}
						for _, statement := range []*sql.Stmt{m, insideMetadata} {
							fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: statement, SQL: step.Query}, s.b.Recorder, step)
						}
					}
					heldMetadata("(id <= 2)", true, true)
					s.exec("SET ROLE tp_reader")
					heldRead(1, 2)
					s.exec("RESET ROLE")
					s.ddl("ALTER POLICY p ON t USING(id=1)")
					heldMetadata("(id = 1)", true, true)
					s.exec("SET ROLE tp_reader")
					heldRead(1)
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: u, SQL: updateQ}, s.b.Recorder, fixture.Exec(updateQ, 0, int64(2)))
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: i, SQL: insertQ}, s.b.Recorder, fixture.Exec(insertQ, 1, int64(4), true))
					s.exec("RESET ROLE")
					s.ddl("ALTER POLICY p ON t TO tp_other")
					heldMetadata("(id = 1)", false, true)
					s.exec("SET ROLE tp_reader")
					heldRead()
					s.exec("RESET ROLE")
					s.ddl("DROP POLICY p ON t")
					heldMetadata("", true, false)
					s.exec("SET ROLE tp_reader")
					heldRead()
					s.exec("SAVEPOINT denied")
					step := fixture.ImplicitError(insertQ, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`}, int64(5), true)
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.x, Statement: i, SQL: insertQ}, s.b.Recorder, step)
					s.exec("ROLLBACK TO denied")
					s.exec("RESET ROLE")
					s.ddl("CREATE POLICY p ON t TO tp_reader USING(id=2) WITH CHECK(allowed)")
					if s.oid("t", "p") == oid {
						t.Fatal("DROP/recreate reused policy identity")
					}
					heldMetadata("(id = 2)", true, true)
					s.exec("SET ROLE tp_reader")
					heldRead(2)
					s.exec("RESET ROLE")
					finish(commit)
					if commit {
						s.ids(2)
						s.facts("t", "p", "*", true, "tp_reader", "(id = 2)", "allowed")
					} else {
						s.sameOID("t", "p", oid)
						s.ids(1, 2)
						s.facts("t", "p", "*", true, "tp_reader", "(id <= 2)", "allowed")
					}
					s.exec("SET ROLE tp_reader")
					want := [][]any{{int64(1)}, {int64(2)}}
					affected := int64(1)
					if commit {
						want = [][]any{{int64(2)}}
						affected = 0
					}
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: read, SQL: readQ}, s.b.Recorder, fixture.Select(readQ, []string{"id"}, []string{"INT4"}, want))
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: update, SQL: updateQ}, s.b.Recorder, fixture.Exec(updateQ, affected, int64(1)))
					fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: insert, SQL: insertQ}, s.b.Recorder, fixture.ImplicitError(insertQ, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`}, int64(6), false))
					s.exec("RESET ROLE")
				}
			})
		})
	}
}
