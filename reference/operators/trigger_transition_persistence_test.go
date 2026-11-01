package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func transitionPersistence(t *testing.T, smoke bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "tt_saved")
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE SCHEMA tt_saved")
				transitionSetup(t, p, b, c, "tt_saved.")
				if smoke {
					transitionFailingInsert(t, p, b, c, "tt_saved.")
				}
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				insert := "INSERT INTO tt_saved.tt(id,v) VALUES($1,$2) RETURNING id,v,g"
				update := "UPDATE tt_saved.tt SET v=v+$1 WHERE id=$2 RETURNING id,v,g"
				remove := "DELETE FROM tt_saved.tt WHERE id=$1 RETURNING id,v,g"
				held := make(map[string]*sql.Stmt)
				for _, q := range []string{insert, update, remove} {
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held[q] = stmt
				}
				run(transitionAudit("tt_saved."))
				run(transitionCounts("tt_saved."))
				run(fixture.Exec("INSERT INTO tt_saved.tt(id,v) VALUES(1,10),(2,20)", 2))
				data := [][]any{{int64(1), int64(11), int64(22)}, {int64(2), int64(21), int64(42)}}
				audit := transitionSeedAudit()
				counts := [][]any{{"INSERT", nil, int64(2)}}
				if smoke {
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("TRUNCATE tt_saved.tt_audit,tt_saved.tt_count", 0))
					run(fixture.Exec("UPDATE tt_saved.tt SET v=v+5", 2))
					run(transitionRows("SELECT id,v,g FROM tt_saved.tt ORDER BY id", []any{int64(1), int64(16), int64(32)}, []any{int64(2), int64(26), int64(52)}))
					run(fixture.Exec("DELETE FROM tt_saved.tt", 2))
					run(transitionRows("UPDATE tt_saved.tt SET v=v WHERE false RETURNING id,v,g"))
					want := [][]any{{"UPDATE", "OLD", int64(1), int64(11), int64(22)}, {"UPDATE", "OLD", int64(2), int64(21), int64(42)}, {"UPDATE", "NEW", int64(1), int64(16), int64(32)}, {"UPDATE", "NEW", int64(2), int64(26), int64(52)}, {"DELETE", "OLD", int64(1), int64(16), int64(32)}, {"DELETE", "OLD", int64(2), int64(26), int64(52)}}
					run(transitionAudit("tt_saved.", want...))
					run(fixture.Exec("SAVEPOINT before_failure", 0))
					failure := fixture.ErrorDiagnostic("INSERT INTO tt_saved.tt(id,v) VALUES(-1,10),(-2,20) RETURNING id,v,g", transitionFailure("tt_saved."))
					failure.SelectRows = true
					run(failure)
					run(fixture.Exec("ROLLBACK TO SAVEPOINT before_failure", 0))
					run(transitionRows("SELECT id,v,g FROM tt_saved.tt"))
					run(transitionAudit("tt_saved.", want...))
					run(transitionCounts("tt_saved.", []any{"UPDATE", int64(2), int64(2)}, []any{"DELETE", int64(2), nil}, []any{"UPDATE", int64(0), int64(0)}))
					run(fixture.Exec("ROLLBACK", 0))
				}
				// Eager native rows and original writers deliberately outlive both
				// successful replacements. Streaming PostgreSQL has no such control.
				var eager *sql.Rows
				if !b.PG {
					var err error
					eager, err = c.QueryContext(p.Ctx, "SELECT id,v,g FROM tt_saved.tt ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer eager.Close()
				}
				check := func(conn *sql.Conn, backend fixture.Backend) {
					apply := func(s fixture.Step) { afterStep(t, p, backend, conn, prepared, s) }
					apply(transitionRows("SELECT id,v,g FROM tt_saved.tt ORDER BY id", data...))
					apply(transitionAudit("tt_saved.", audit...))
					apply(transitionCounts("tt_saved.", counts...))
					transitionMetadata(t, p, backend, conn, prepared, "tt_saved.")
					apply(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='tt_saved'::regnamespace AND relkind IN('r','v','m') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"tt"}, {"tt_audit"}, {"tt_count"}}))
					cases := []struct {
						q      string
						args   []any
						row    []any
						suffix [][]any
						count  []any
						route  string
					}{
						{insert, []any{99, 40}, []any{int64(99), int64(41), int64(82)}, [][]any{{"INSERT", "NEW", int64(99), int64(41), int64(82)}}, []any{"INSERT", nil, int64(1)}, "held"},
						{insert, []any{98, 50}, []any{int64(98), int64(51), int64(102)}, [][]any{{"INSERT", "NEW", int64(98), int64(51), int64(102)}}, []any{"INSERT", nil, int64(1)}, "prepared"},
						{insert, []any{97, 60}, []any{int64(97), int64(61), int64(122)}, [][]any{{"INSERT", "NEW", int64(97), int64(61), int64(122)}}, []any{"INSERT", nil, int64(1)}, "direct"},
						{update, []any{5, 1}, []any{int64(1), int64(16), int64(32)}, [][]any{{"UPDATE", "OLD", int64(1), int64(11), int64(22)}, {"UPDATE", "NEW", int64(1), int64(16), int64(32)}}, []any{"UPDATE", int64(1), int64(1)}, "held"},
						{update, []any{7, 2}, []any{int64(2), int64(28), int64(56)}, [][]any{{"UPDATE", "OLD", int64(2), int64(21), int64(42)}, {"UPDATE", "NEW", int64(2), int64(28), int64(56)}}, []any{"UPDATE", int64(1), int64(1)}, "prepared"},
						{update, []any{9, 1}, []any{int64(1), int64(20), int64(40)}, [][]any{{"UPDATE", "OLD", int64(1), int64(11), int64(22)}, {"UPDATE", "NEW", int64(1), int64(20), int64(40)}}, []any{"UPDATE", int64(1), int64(1)}, "direct"},
						{remove, []any{2}, []any{int64(2), int64(21), int64(42)}, [][]any{{"DELETE", "OLD", int64(2), int64(21), int64(42)}}, []any{"DELETE", int64(1), nil}, "held"},
						{remove, []any{1}, []any{int64(1), int64(11), int64(22)}, [][]any{{"DELETE", "OLD", int64(1), int64(11), int64(22)}}, []any{"DELETE", int64(1), nil}, "prepared"},
						{remove, []any{2}, []any{int64(2), int64(21), int64(42)}, [][]any{{"DELETE", "OLD", int64(2), int64(21), int64(42)}}, []any{"DELETE", int64(1), nil}, "direct"},
						{update, []any{1, 999}, nil, nil, []any{"UPDATE", int64(0), int64(0)}, "held"},
					}
					for _, tc := range cases {
						apply(fixture.Exec("BEGIN", 0))
						s := transitionRows(tc.q)
						s.Args = tc.args
						if tc.row != nil {
							s.Rows = [][]any{tc.row}
						}
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							fixture.DriverBasicMetadata(t, rows, backend.PG, s.Types, []bool{false, true, true})
						}
						if tc.route == "held" && conn == c {
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held[tc.q], SQL: tc.q}, backend.Recorder, s)
						} else {
							if tc.route != "direct" {
								s = fixture.Prepared(s)
							}
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}
						want := append(append([][]any(nil), audit...), tc.suffix...)
						apply(transitionAudit("tt_saved.", want...))
						wantCounts := append(append([][]any(nil), counts...), tc.count)
						apply(transitionCounts("tt_saved.", wantCounts...))
						apply(fixture.Exec("ROLLBACK", 0))
						apply(transitionAudit("tt_saved.", audit...))
					}
				}
				parametersReplay(t, p, b, c, "tt_saved", check)
				if b.PG {
					return
				}
				transitionDrainRows(t, eager, data)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				// The live destination differs from the source of every failed import.
				run(fixture.Exec("INSERT INTO tt_saved.tt(id,v) VALUES(3,30)", 1))
				data = append(data, []any{int64(3), int64(31), int64(62)})
				audit = append(audit, []any{"INSERT", "NEW", int64(3), int64(31), int64(62)})
				counts = append(counts, []any{"INSERT", nil, int64(1)})
				failures := []struct {
					name     string
					restore  func() error
					ordinary error
				}{
					{"late-transition", func() error {
						return stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+`; CREATE TRIGGER bad AFTER INSERT ON tt_saved.tt REFERENCING OLD TABLE bad_rows EXECUTE FUNCTION tt_saved.missing();`))
					}, nil},
					{"reader", func() error { return stuntgres.Restore(p.Ctx, p.Native, &plpgsqlFailReader{data: dump.Bytes()}) }, io.ErrUnexpectedEOF},
					{"canceled", func() error {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						return stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes()))
					}, context.Canceled},
				}
				for _, failure := range failures {
					old, err := c.QueryContext(p.Ctx, "SELECT id,v,g FROM tt_saved.tt ORDER BY id")
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					err = failure.restore()
					if err == nil {
						t.Fatalf("%s restore succeeded", failure.name)
					}
					if failure.ordinary != nil {
						fixture.LexicalOrdinary(t, err, failure.ordinary)
					} else {
						d := fixture.ControlsReferenceDiagnostic("42P17", "OLD TABLE can only be specified for a DELETE or UPDATE trigger")
						fixture.AssertDiagnostic(t, err, &d)
					}
					check(c, b)
					transitionDrainRows(t, old, data)
				}
			})
		})
	}
}

func transitionDrainRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	for _, row := range want {
		if !rows.Next() {
			t.Fatal("lost held transition result", rows.Err())
		}
		var id, v, g int64
		if err := rows.Scan(&id, &v, &g); err != nil || id != row[0] || v != row[1] || g != row[2] {
			t.Fatal(id, v, g, row, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("held transition result suffix", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceTriggerTransitionPersistence(t *testing.T)  { transitionPersistence(t, false) }
func TestReferenceTriggerTransitionFeatureSmoke(t *testing.T) { transitionPersistence(t, true) }
