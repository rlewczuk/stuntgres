package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceSyntax(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, token string }{
			{"CREATE TABLESPACE ts_contract_syntax", ""},
			{"CREATE TABLESPACE IF NOT EXISTS ts_contract_syntax LOCATION '/tmp/x'", "NOT"},
			{"CREATE TABLESPACE public.ts_contract_syntax LOCATION '/tmp/x'", "."},
			{"CREATE TABLESPACE ts_contract_syntax LOCATION 1", "1"},
			{"CREATE TABLESPACE ts_contract_syntax LOCATION $1", "$1"},
			{"CREATE TABLESPACE ts_contract_syntax LOCATION '/tmp/x' OWNER CURRENT_USER", "OWNER"},
			{"CREATE TABLESPACE ts_contract_syntax LOCATION '/tmp/x' WITH ()", ")"},
			{"PREPARE ts_contract_prepare AS CREATE TABLESPACE ts_contract_syntax LOCATION '/tmp/x'", "CREATE"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				message := "syntax error at end of input"
				position := len(tc.q) + 1
				if tc.token != "" {
					message = "syntax error at or near \"" + tc.token + "\""
					position = strings.Index(tc.q, tc.token) + 1
				}
				want := tablespaceDiagnostic("42601", message)
				want.Position = fixture.Position(position)
				tablespaceError(t, p, b, c, tc.q, want, true)
			})
		}
		q := "CREATE TABLE ts_contract_unexecuted(a INT); CREATE TABLESPACE ts_contract_syntax LOCATION"
		want := tablespaceDiagnostic("42601", "syntax error at end of input")
		want.Position = fixture.Position(len(q) + 1)
		tablespaceError(t, p, b, c, q, want, true)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='ts_contract_unexecuted'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		// LOCATION remains an ordinary unreserved identifier outside this production.
		tablespaceExec(t, p, b, c, "CREATE TABLE ts_contract_location(location TEXT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ts_contract_location VALUES('kept')", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Select("SELECT location FROM ts_contract_location", []string{"location"}, []string{"TEXT"}, [][]any{{"kept"}})))
	})
}

func tablespaceTransactionChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := tablespaceSQL("ts_contract_transaction", "ts_contract_missing", "relative")
	for _, readonly := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: readonly})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			want := tablespaceDiagnostic("25001", "CREATE TABLESPACE cannot run inside a transaction block")
			if readonly {
				want = tablespaceDiagnostic("25006", "cannot execute CREATE TABLESPACE in a read-only transaction")
			}
			s := fixture.ErrorDiagnostic(q, want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("SELECT 1", tablespaceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}

func TestReferenceTablespaceTransactions(t *testing.T) {
	tablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		tablespaceTransactionChecks(t, p, b, c)
		tablespaceRole(t, p, b, c, "ts_contract_tx_reader", "")
		tablespaceExec(t, p, b, c, "SET ROLE ts_contract_tx_reader")
		tablespaceTransactionChecks(t, p, b, c)
		tablespaceExec(t, p, b, c, "RESET ROLE")
		q := "CREATE TABLE ts_contract_chain(a INT); " + tablespaceSQL("ts_contract_transaction", "ts_contract_missing", "relative")
		s := fixture.ErrorDiagnostic(q, tablespaceDiagnostic("25001", "CREATE TABLESPACE cannot run inside a transaction block"))
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='ts_contract_chain'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		tablespaceExec(t, p, b, c, "SET default_transaction_read_only=on")
		defer c.ExecContext(p.Ctx, "SET default_transaction_read_only=off")
		tablespaceError(t, p, b, c, tablespaceSQL("ts_contract_default_ro", "ts_contract_missing", "relative"), tablespaceDiagnostic("25006", "cannot execute CREATE TABLESPACE in a read-only transaction"), false)
		tablespaceExec(t, p, b, c, "SET default_transaction_read_only=off")
		tablespaceNoUserRows(t, p, b, c)
	})
}
