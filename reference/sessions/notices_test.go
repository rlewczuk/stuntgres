package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Execute the public command routes, retaining original non-Context held
// consumers alongside their recorded Context companions. Preparation is
// effect-free; Query exposes and closes an empty descriptor.
func noticesCommand(t *testing.T, ctx context.Context, c *sql.Conn, record *fixture.Recorder, notices *[]fixture.Diagnostic, mode, query string) error {
	t.Helper()
	ctx = context.WithValue(ctx, fixture.RecorderKey{}, record)
	before := len(*notices)
	nonContext := strings.HasSuffix(mode, "-noncontext")
	var stmt *sql.Stmt
	var err error
	if strings.HasPrefix(mode, "prepared-") {
		stmt, err = c.PrepareContext(ctx, query)
		if err != nil {
			t.Fatal("semantic failure during preparation", err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		if record.RawBackend == fixture.RawPostgres {
			for _, diagnostic := range (*notices)[before:] {
				noticesInternalDebug(t, diagnostic)
			}
		} else if len(*notices) != before {
			t.Fatal("native prepare delivered notice", (*notices)[before:])
		}
	}
	if strings.Contains(mode, "query") {
		var rows *sql.Rows
		if stmt != nil {
			if nonContext {
				rows, err = stmt.Query()
			} else {
				rows, err = stmt.QueryContext(ctx)
			}
		} else {
			rows, err = c.QueryContext(ctx, query)
		}
		if err != nil {
			if rows != nil {
				rows.Close()
				t.Fatal("error returned a cursor", err)
			}
			return err
		}
		defer rows.Close()
		columns, e := rows.Columns()
		types, typeErr := rows.ColumnTypes()
		if e != nil || typeErr != nil || len(columns) != 0 || len(types) != 0 || rows.Next() || rows.Err() != nil {
			t.Fatal(columns, types, e, typeErr, rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("closed command cursor", rows.Err())
		}
		if nonContext {
			// database/sql supplies Background to Query(); the separate
			// Context lane below retains middleware recording/ownership.
			return nil
		}
		record.Mu.Lock()
		op := record.Operations[len(record.Operations)-1]
		recordedError, closed, count := op.Err, op.Closed, len(op.Rows)
		record.Mu.Unlock()
		if recordedError != nil || !closed || count != 0 {
			t.Fatal(recordedError, closed, count)
		}
		return nil
	}
	var result sql.Result
	if stmt != nil {
		if nonContext {
			result, err = stmt.Exec()
		} else {
			result, err = stmt.ExecContext(ctx)
		}
	} else {
		result, err = c.ExecContext(ctx, query)
	}
	if err != nil {
		if result != nil {
			t.Fatal("error returned a result", err)
		}
		return err
	}
	count, e := result.RowsAffected()
	if count != 0 || e != nil {
		t.Fatal(count, e)
	}
	if _, e = result.LastInsertId(); e == nil {
		t.Fatal("LastInsertId unexpectedly supported")
	}
	return nil
}

func noticesRows(t *testing.T, ctx context.Context, c *sql.Conn, record *fixture.Recorder) {
	t.Helper()
	s := fixture.Select("SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var id int
		var note string
		if err := rows.Scan(&id, &note); err != nil || id != 7 || note != "kept" {
			t.Fatal(id, note, err)
		}
	}
	fixture.RunStep(t, ctx, c, record, s)
}

func noticesShow(t *testing.T, ctx context.Context, c *sql.Conn, record *fixture.Recorder, name, value string) {
	t.Helper()
	s := fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{value}})
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].ScanType() != reflect.TypeOf("") {
			t.Fatal(types, err)
		}
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if got, ok := raw.(string); !ok || got != value {
			t.Fatalf("raw SHOW %#v", raw)
		}
		var typed string
		if err := rows.Scan(&typed); err != nil || typed != value {
			t.Fatal(typed, err)
		}
	}
	fixture.RunStep(t, ctx, c, record, s)
}
func TestReferenceNoticesCommands(t *testing.T) {
	for _, mode := range []string{"exec", "query", "prepared-exec", "prepared-query", "prepared-exec-noncontext", "prepared-query-noncontext"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, record, s) }
					run(fixture.Exec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
					run(fixture.Exec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
					if len(notices) != 0 {
						t.Fatal(notices)
					}
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
					for i, query := range []string{"CREATE TABLE IF NOT EXISTS notice_rows(replaced BOOL)", "CREATE TABLE IF NOT EXISTS public.notice_rows(id INT,id TEXT)"} {
						if err := noticesCommand(t, p.Ctx, c, record, &notices, mode, query); err != nil {
							t.Fatal(err)
						}
						if len(notices) != i+1 || notices[i] != want {
							t.Fatal(notices)
						}
						noticesRows(t, p.Ctx, c, record)
					}
					err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS fresh(id INT,id TEXT)")
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42701", Message: `column "id" specified more than once`})
					if len(notices) != 2 {
						t.Fatal("semantic failure emitted notice", notices)
					}
					if err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS fresh(id INT)"); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec("SET client_min_messages TO error", 0))
					err = noticesCommand(t, p.Ctx, c, record, &notices, mode, "CREATE TABLE notice_rows(id INT)")
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "notice_rows" already exists`})
					if err := noticesCommand(t, p.Ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS notice_rows(id INT)"); err != nil {
						t.Fatal(err)
					}
					err = noticesCommand(t, p.Ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS missing_schema.x(id INT)")
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "28"})
					if backend.PG {
						fixture.SessionCancel(t, p.Ctx, c, false)
						if len(notices) != 2 {
							t.Fatal("cancellation emitted notice", notices)
						}
					} else {
						ctx, cancel := context.WithCancel(p.Ctx)
						cancel()
						result, err := c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS canceled(id INT)")
						if result != nil || !errors.Is(err, context.Canceled) || len(notices) != 2 {
							t.Fatal(result, err, notices)
						}
					}
					noticesRows(t, p.Ctx, c, record)
				})
			}
		})
	}
}

// SQL notice witnesses remain exact. PostgreSQL debug tracing is incidental:
// allocator identities, trace text and loader ordering are not SQL contracts.
func referenceNoticeWitnesses(t *testing.T, postgres bool, records []fixture.Diagnostic, want fixture.Diagnostic) []fixture.Diagnostic {
	t.Helper()
	var witnesses []fixture.Diagnostic
	for _, record := range records {
		if record.Code == want.Code && record.Message == want.Message {
			witnesses = append(witnesses, record)
			continue
		}
		if !postgres {
			t.Fatalf("unexpected non-witness diagnostic: %#v (witness %#v)", record, want)
		}
		noticesInternalDebug(t, record)
	}
	return witnesses
}

func noticesWitness(t *testing.T, pg bool, records []fixture.Diagnostic, want fixture.Diagnostic, delivered bool) {
	t.Helper()
	got := referenceNoticeWitnesses(t, pg, records, want)
	if !delivered {
		if len(got) != 0 {
			t.Fatal(got)
		}
		return
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

// Distinguish internal debug tracing from unexpected SQL notices without
// asserting PostgreSQL implementation text, counts or execution ordering.
func noticesInternalDebug(t *testing.T, got fixture.Diagnostic) {
	t.Helper()
	got.Message = ""
	want := fixture.Diagnostic{Severity: "DEBUG", SeverityUnlocalized: "DEBUG", Code: "00000"}
	if got != want {
		t.Fatalf("unexpected non-witness diagnostic: %#v", got)
	}
}

func TestReferenceNoticesPreparedShow(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			stmt, err := c.PrepareContext(p.Ctx, "SHOW client_min_messages")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, want := range []string{"warning", "debug2", "notice"} {
				fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("SET client_min_messages TO "+want, 0))
				var got string
				if err := stmt.QueryRow().Scan(&got); err != nil || got != want {
					t.Fatal(got, err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SHOW client_min_messages"}
				fixture.RunStep(t, p.Ctx, held, record, fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{want}}))
				result, err := stmt.Exec()
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if count != 0 || err != nil {
					t.Fatal(count, err)
				}
			}
		})
	}
}

func TestReferenceNoticesCancellationAndSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("CREATE TABLE cancel_rows(id INT)", 0))
			stmt, err := c.PrepareContext(p.Ctx, "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var result sql.Result
			if backend.PG {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						fixture.SessionCancel(t, p.Ctx, c, prepared, query)
					}
				}
				if len(notices) != 0 {
					t.Fatal("admitted cancellation emitted notice", notices)
				}
			} else {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				result, err = c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
				if result != nil || !errors.Is(err, context.Canceled) || len(notices) != 0 {
					t.Fatal(result, err, notices)
				}
				rows, err := c.QueryContext(ctx, "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
				if rows != nil {
					rows.Close()
					t.Fatal("canceled direct query returned cursor")
				}
				if !errors.Is(err, context.Canceled) || len(notices) != 0 {
					t.Fatal(err, notices)
				}
				for _, query := range []bool{false, true} {
					if query {
						rows, e := stmt.QueryContext(ctx)
						err = e
						if rows != nil {
							rows.Close()
							t.Fatal("canceled query returned cursor")
						}
					} else {
						result, err = stmt.ExecContext(ctx)
						if result != nil {
							t.Fatal("canceled held command returned result")
						}
					}
					if !errors.Is(err, context.Canceled) || len(notices) != 0 {
						t.Fatal(err, notices)
					}
				}
			}
			for _, prepared := range []bool{false, true} {
				q := "CREATE TABLE IF NOT EXISTS syntax_prefix(id INT) garbage"
				if prepared {
					bad, e := c.PrepareContext(p.Ctx, q)
					err = e
					if bad != nil {
						bad.Close()
						t.Fatal("syntax failure returned statement")
					}
				} else {
					result, err = c.ExecContext(p.Ctx, q)
					if result != nil {
						t.Fatal("syntax failure returned result")
					}
				}
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "garbage"`, Position: "50"})
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			}
			fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("CREATE TABLE syntax_prefix(id INT)", 0))
			fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "cancel_rows" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal(notices)
			}
		})
	}
}
