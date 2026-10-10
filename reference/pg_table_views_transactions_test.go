package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferencePGTableViewsTransactions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := referenceLockConnections(t, p.ctx, b.db)
			run := func(db referenceExecutor, q string) { referenceRunStep(t, p.ctx, db, b.recorder, referenceExec(q, 0)) }
			query := "SELECT viewname FROM pg_catalog.pg_views WHERE schemaname='public' AND viewname='pgtv_tx_view'"
			check := func(db referenceExecutor, present bool) {
				var rows [][]any
				if present {
					rows = [][]any{{"pgtv_tx_view"}}
				}
				referenceRunStep(t, p.ctx, db, b.recorder, referenceSelect(query, []string{"viewname"}, []string{"NAME"}, rows))
			}
			run(a, "CREATE SEQUENCE pgtv_tx_sequence")
			run(a, "CREATE VIEW pgtv_tx_view AS SELECT last_value FROM pgtv_tx_sequence")
			run(a, "BEGIN")
			defer a.ExecContext(p.ctx, "ROLLBACK")
			run(a, "SAVEPOINT pgtv_save")
			run(a, "DROP SEQUENCE pgtv_tx_sequence CASCADE")
			check(a, false)
			check(peer, true)
			run(a, "ROLLBACK TO pgtv_save")
			check(a, true)
			run(a, "ROLLBACK")
			check(peer, true)
			run(a, "BEGIN")
			run(a, "DROP SEQUENCE pgtv_tx_sequence CASCADE")
			run(a, "COMMIT")
			check(peer, false)
			run(peer, "CREATE TABLE pgtv_rr_old(n INT)")
			q := "SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname='public' AND (tablename='pgtv_rr_old' OR tablename='pgtv_rr_added') ORDER BY tablename"
			stmt, err := a.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: q}
			rr := func(db referenceExecutor, added bool) {
				rows := [][]any{{"pgtv_rr_old"}}
				if added {
					rows = [][]any{{"pgtv_rr_added"}, {"pgtv_rr_old"}}
				}
				referenceRunStep(t, p.ctx, db, b.recorder, referenceSelect(q, []string{"tablename"}, []string{"NAME"}, rows))
			}
			run(a, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			rr(a, false)
			rr(held, false)
			run(peer, "CREATE TABLE pgtv_rr_added(n INT)")
			rr(a, !b.pg)
			rr(held, !b.pg)
			rr(peer, true)
			run(a, "COMMIT")
			rr(a, true)
			rr(held, true)
		})
	}
}

func TestReferencePGTableViewsAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE pgtv_atomic(n INT)", 0))
			run(referenceExec("INSERT INTO pgtv_atomic VALUES(4)", 1))
			run(referenceExec("CREATE VIEW pgtv_atomic_view AS SELECT n FROM pgtv_atomic", 0))
			before := pgViewSummary(t, p, b, b.db, "public", "pgtv_atomic_view", pgTableViewsOwner(b), "pgtv_atomic")
			run(referenceImplicitError("CREATE OR REPLACE VIEW pgtv_atomic_view AS SELECT n::BIGINT AS n FROM pgtv_atomic", referenceDiagnostic{Code: "42P16", Message: `cannot change data type of view column "n" from integer to bigint`}))
			after := pgViewSummary(t, p, b, b.db, "public", "pgtv_atomic_view", pgTableViewsOwner(b), "pgtv_atomic")
			if after != before {
				t.Fatal("failed replacement changed definition", before, after)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if _, err := b.db.ExecContext(canceled, "CREATE OR REPLACE VIEW pgtv_atomic_view AS SELECT n+1 AS n FROM pgtv_atomic"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if got := pgViewSummary(t, p, b, b.db, "public", "pgtv_atomic_view", pgTableViewsOwner(b), "pgtv_atomic"); got != before {
				t.Fatal("canceled replacement changed definition", got)
			}
			if _, err := b.db.ExecContext(canceled, "CREATE INDEX pgtv_atomic_index ON pgtv_atomic(n)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			pgTableCheck(t, p, b, b.db, "pgtv_atomic", pgTableRow("public", "pgtv_atomic", pgTableViewsOwner(b), false))
			run(referenceSelect("SELECT n FROM pgtv_atomic_view", []string{"n"}, []string{"INT4"}, [][]any{{int64(4)}}))
		})
	}
}
