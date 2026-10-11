package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorHoldCancellation(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		holdRun(t, p, b, c, "CREATE TABLE hold_private(i INTEGER)")
		holdRun(t, p, b, c, holdDeclare("old"))
		holdRun(t, p, b, c, "BEGIN")
		sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_private VALUES(9)", 1))
		if b.PG {
			holdRun(t, p, b, c, "DECLARE canceled NO SCROLL CURSOR WITH HOLD FOR SELECT i,pg_sleep(600) FROM cursor_rows ORDER BY i")
			// Existing helper observes the actual COMMIT in pg_stat_activity, signals
			// pg_cancel_backend and joins the bounded worker; no elapsed-time admission.
			sqlCursorCancelDemand(t, p, c, "COMMIT")
			holdRun(t, p, b, c, "ROLLBACK")
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_private", []string{"i"}, []string{"INT4"}, [][]any{}))
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM canceled", sqlCursorMissing("canceled")))
			holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
		} else {
			// Public precancellation is separate from admitted native commit proof,
			// which the production round's private authority/sequence-wait unit owns.
			holdRun(t, p, b, c, holdDeclare("canceled"))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := c.ExecContext(ctx, "COMMIT")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled COMMIT: %v %v", result, err)
			}
			holdRun(t, p, b, c, "ROLLBACK")
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_private", []string{"i"}, []string{"INT4"}, [][]any{}))
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM canceled", sqlCursorMissing("canceled")))
			holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
		}
	})
}
func TestReferenceSQLCursorHoldSessionAndControls(t *testing.T) {
	t.Run("pool_return", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			b.DB.SetMaxOpenConns(1)
			b.DB.SetMaxIdleConns(1)
			sqlCursorSetup(t, p, b, c)
			holdRun(t, p, b, c, holdDeclare("pool_h"))
			holdBatch(t, p, b, c, "FETCH 1 FROM pool_h", []any{int64(1), "one"})
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			borrower, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer borrower.Close()
			holdBatch(t, p, b, borrower, "FETCH 1 FROM pool_h", []any{int64(2), nil})
			holdRun(t, p, b, borrower, "CLOSE pool_h")
		})
	})
	t.Run("physical", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			holdRun(t, p, b, c, holdDeclare("h"))
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			sqlCursorStep(t, p, b, peer, fixture.ErrorDiagnostic("FETCH ALL FROM h", sqlCursorMissing("h")))
			holdRun(t, p, b, peer, "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT 99 AS peer")
			rows, err := c.QueryContext(p.Ctx, "FETCH 1 FROM h")
			if err != nil {
				t.Fatal(err)
			}
			fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4", "TEXT"}, []bool{false, true})
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			holdBatch(t, p, b, c, "FETCH 1 FROM h", []any{int64(2), nil})
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
				t.Fatalf("physical discard: %v", err)
			}
			replacement, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			sqlCursorStep(t, p, b, replacement, fixture.ErrorDiagnostic("FETCH ALL FROM h", sqlCursorMissing("h")))
			sqlCursorStep(t, p, b, peer, fixture.Select("FETCH ALL FROM h", []string{"peer"}, []string{"INT4"}, [][]any{{int64(99)}}))
		})
	})
	t.Run("discard", func(t *testing.T) {
		sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorSetup(t, p, b, c)
			holdRun(t, p, b, c, holdDeclare("h"))
			holdRun(t, p, b, c, "DISCARD PLANS")
			holdBatch(t, p, b, c, "FETCH 1 FROM h", []any{int64(1), "one"})
			holdRun(t, p, b, c, "DISCARD ALL")
			sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM h", sqlCursorMissing("h")))
			holdRun(t, p, b, c, "CREATE TEMP TABLE hold_temp(i INTEGER) ON COMMIT DELETE ROWS")
			holdRun(t, p, b, c, "BEGIN")
			sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_temp VALUES(1),(2)", 2))
			holdRun(t, p, b, c, "DECLARE temp_h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM hold_temp ORDER BY i")
			holdRun(t, p, b, c, "COMMIT")
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_temp", []string{"i"}, []string{"INT4"}, [][]any{}))
			holdRun(t, p, b, c, "DISCARD TEMP")
			sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM temp_h", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	})
	for _, replacement := range []string{"reset", "restore", "failed-restore"} {
		t.Run(replacement, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				holdRun(t, p, b, c, holdDeclare("control_h"))
				if b.PG {
					holdRun(t, p, b, c, "BEGIN")
					holdRun(t, p, b, c, "ROLLBACK")
					holdBatch(t, p, b, c, "FETCH 1 FROM control_h", []any{int64(1), "one"})
					return
				}
				query := "FETCH 1 FROM control_h"
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				rows, err := c.QueryContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				frozen := fixture.SequenceFrozenRows{Executor: c, Rows: rows, Query: query}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "control_h") || strings.Contains(dump.String(), "DECLARE") {
					t.Fatalf("Dump contains cursor execution: %s", dump.String())
				}
				switch replacement {
				case "reset":
					err = stuntgres.Reset(p.Ctx, p.Native)
				case "restore":
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()))
				case "failed-restore":
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(i INTEGER); INSERT INTO bad VALUES($1)"))
					want := sqlCursorDiagnostic("42P02", "parameters are not allowed in restore")
					want.Position = "53"
					fixture.AssertDiagnostic(t, err, &want)
					err = nil
				}
				if err != nil {
					t.Fatal(err)
				}
				holdBatch(t, p, b, frozen, query, []any{int64(1), "one"})
				if replacement == "failed-restore" {
					holdBatch(t, p, b, c, query, []any{int64(2), nil})
				} else {
					result, err := c.ExecContext(p.Ctx, "FETCH ALL FROM control_h")
					if result != nil {
						t.Fatal("fenced FETCH exposed result")
					}
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				}
			})
		})
	}
}
