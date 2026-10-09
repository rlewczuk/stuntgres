package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Execute the public command routes, retaining original non-Context held
// consumers alongside their recorded Context companions. Preparation is
// effect-free; Query exposes and closes an empty descriptor.
func noticesCommand(t *testing.T, ctx context.Context, c *sql.Conn, record *referenceRecorder, notices *[]referenceDiagnostic, mode, query string, thresholds ...string) error {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, record)
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
		if record.rawBackend == referenceRawPostgres {
			expected := 0
			if len(thresholds) != 0 {
				switch thresholds[0] {
				case "debug5":
					expected = 3
				case "debug4", "debug3", "debug2", "debug":
					expected = 1
				}
			}
			captured := (*notices)[before:]
			if len(captured) != expected {
				t.Fatalf("PostgreSQL prepare diagnostics got %#v want %d records", captured, expected)
			}
			if expected != 0 && !strings.HasPrefix(captured[0].Message, "parse ") {
				t.Fatal("missing Prepare parser diagnostic", captured)
			}
			if expected == 3 {
				if captured[1].Message != "StartTransaction(1) name: unnamed; blockState: DEFAULT; state: INPROGRESS, xid/subid/cid: 0/1/0" ||
					captured[2].Message != "CommitTransaction(1) name: unnamed; blockState: STARTED; state: INPROGRESS, xid/subid/cid: 0/1/0" {
					t.Fatal("Prepare transaction diagnostic order", captured)
				}
			}
			for _, diagnostic := range (*notices)[before:] {
				noticesInternalDebug(t, diagnostic, query)
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
		record.mu.Lock()
		op := record.operations[len(record.operations)-1]
		recordedError, closed, count := op.Err, op.Closed, len(op.Rows)
		record.mu.Unlock()
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

func noticesRows(t *testing.T, ctx context.Context, c *sql.Conn, record *referenceRecorder) {
	t.Helper()
	s := referenceSelect("SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}})
	s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var id int
		var note string
		if err := rows.Scan(&id, &note); err != nil || id != 7 || note != "kept" {
			t.Fatal(id, note, err)
		}
	}
	referenceRunStep(t, ctx, c, record, s)
}

func noticesShow(t *testing.T, ctx context.Context, c *sql.Conn, record *referenceRecorder, name, value string) {
	t.Helper()
	s := referenceSelect("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{value}})
	s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
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
	referenceRunStep(t, ctx, c, record, s)
}
func TestReferenceNoticesCommands(t *testing.T) {
	for _, mode := range []string{"exec", "query", "prepared-exec", "prepared-query", "prepared-exec-noncontext", "prepared-query-noncontext"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
					run(referenceExec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
					run(referenceExec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
					if len(notices) != 0 {
						t.Fatal(notices)
					}
					want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
					for i, query := range []string{"CREATE TABLE IF NOT EXISTS notice_rows(replaced BOOL)", "CREATE TABLE IF NOT EXISTS public.notice_rows(id INT,id TEXT)"} {
						if err := noticesCommand(t, p.ctx, c, record, &notices, mode, query); err != nil {
							t.Fatal(err)
						}
						if len(notices) != i+1 || notices[i] != want {
							t.Fatal(notices)
						}
						noticesRows(t, p.ctx, c, record)
					}
					err := noticesCommand(t, p.ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS fresh(id INT,id TEXT)")
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42701", Message: `column "id" specified more than once`})
					if len(notices) != 2 {
						t.Fatal("semantic failure emitted notice", notices)
					}
					if err := noticesCommand(t, p.ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS fresh(id INT)"); err != nil {
						t.Fatal(err)
					}
					run(referenceExec("SET client_min_messages TO error", 0))
					err = noticesCommand(t, p.ctx, c, record, &notices, mode, "CREATE TABLE notice_rows(id INT)")
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "notice_rows" already exists`})
					if err := noticesCommand(t, p.ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS notice_rows(id INT)"); err != nil {
						t.Fatal(err)
					}
					err = noticesCommand(t, p.ctx, c, record, &notices, mode, "CREATE TABLE IF NOT EXISTS missing_schema.x(id INT)")
					referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "28"})
					if backend.pg {
						referenceSessionCancel(t, p.ctx, c, false)
						if len(notices) != 2 {
							t.Fatal("cancellation emitted notice", notices)
						}
					} else {
						ctx, cancel := context.WithCancel(p.ctx)
						cancel()
						result, err := c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS canceled(id INT)")
						if result != nil || !errors.Is(err, context.Canceled) || len(notices) != 2 {
							t.Fatal(result, err, notices)
						}
					}
					noticesRows(t, p.ctx, c, record)
				})
			}
		})
	}
}

// Internal DEBUG diagnostics are a real PostgreSQL-only companion to the
// independently specified feature notice; never normalize malformed notices.
func referenceNoticeWitnesses(t *testing.T, postgres bool, records []referenceDiagnostic, want referenceDiagnostic, queries ...string) []referenceDiagnostic {
	t.Helper()
	var witnesses []referenceDiagnostic
	loaderStage := 0
	for _, record := range records {
		if record.Code == want.Code && record.Message == want.Message {
			witnesses = append(witnesses, record)
			continue
		}
		if !postgres {
			t.Fatalf("unexpected non-witness diagnostic: %#v (witness %#v)", record, want)
		}
		noticesInternalDebug(t, record, queries...)
		switch record.Message {
		case `find_in_path: trying "/usr/lib/postgresql/18/lib/plpgsql"`:
			if loaderStage != 0 {
				t.Fatal("duplicate or reordered PL/pgSQL loader record", records)
			}
			loaderStage = 1
		case `find_in_path: trying "/usr/lib/postgresql/18/lib/plpgsql.so"`:
			if loaderStage != 1 {
				t.Fatal("PL/pgSQL .so lookup preceded base-path lookup", records)
			}
			loaderStage = 2
		}
	}
	if loaderStage == 1 {
		t.Fatal("incomplete pinned PL/pgSQL loader sequence", records)
	}
	return witnesses
}

func noticesWitness(t *testing.T, pg bool, records []referenceDiagnostic, want referenceDiagnostic, delivered bool, queries ...string) {
	t.Helper()
	got := referenceNoticeWitnesses(t, pg, records, want, queries...)
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

// PostgreSQL18.0 emits these exact internal records at DEBUG5/DEBUG2.
// The protocol statement name is a generated positive decimal identity, not
// a portable allocator constant. SQL text and every other field remain exact.
func noticesInternalDebug(t *testing.T, got referenceDiagnostic, queries ...string) {
	t.Helper()
	message := got.Message
	switch message {
	case "StartTransaction(1) name: unnamed; blockState: DEFAULT; state: INPROGRESS, xid/subid/cid: 0/1/0",
		"CommitTransaction(1) name: unnamed; blockState: STARTED; state: INPROGRESS, xid/subid/cid: 0/1/0",
		`find_in_path: trying "/usr/lib/postgresql/18/lib/plpgsql"`,
		`find_in_path: trying "/usr/lib/postgresql/18/lib/plpgsql.so"`:
	default:
		valid := false
		if tail, ok := strings.CutPrefix(message, "parse "); ok {
			name, query, found := strings.Cut(tail, ": ")
			id, err := strconv.ParseUint(name, 10, 64)
			for _, expected := range queries {
				if found && err == nil && id > 0 && strconv.FormatUint(id, 10) == name && query == expected {
					valid = true
				}
			}
		} else if name, ok := strings.CutPrefix(message, "bind <unnamed> to "); ok {
			id, err := strconv.ParseUint(name, 10, 64)
			valid = err == nil && id > 0 && strconv.FormatUint(id, 10) == name
		}
		if !valid {
			t.Fatalf("unexpected PostgreSQL internal diagnostic: %#v", got)
		}
	}
	want := referenceDiagnostic{Severity: "DEBUG", SeverityUnlocalized: "DEBUG", Code: "00000", Message: message}
	if got != want {
		t.Fatalf("PostgreSQL internal diagnostic fields got %#v want %#v", got, want)
	}
}

func TestReferenceNoticesPreparedShow(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
			stmt, err := c.PrepareContext(p.ctx, "SHOW client_min_messages")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, want := range []string{"warning", "debug2", "notice"} {
				referenceRunStep(t, p.ctx, c, record, referenceExec("SET client_min_messages TO "+want, 0))
				var got string
				if err := stmt.QueryRow().Scan(&got); err != nil || got != want {
					t.Fatal(got, err)
				}
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: "SHOW client_min_messages"}
				referenceRunStep(t, p.ctx, held, record, referenceSelect("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{want}}))
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
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
			referenceRunStep(t, p.ctx, c, record, referenceExec("CREATE TABLE cancel_rows(id INT)", 0))
			stmt, err := c.PrepareContext(p.ctx, "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var result sql.Result
			if backend.pg {
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						referenceSessionCancel(t, p.ctx, c, prepared, query)
					}
				}
				if len(notices) != 0 {
					t.Fatal("admitted cancellation emitted notice", notices)
				}
			} else {
				ctx, cancel := context.WithCancel(p.ctx)
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
					bad, e := c.PrepareContext(p.ctx, q)
					err = e
					if bad != nil {
						bad.Close()
						t.Fatal("syntax failure returned statement")
					}
				} else {
					result, err = c.ExecContext(p.ctx, q)
					if result != nil {
						t.Fatal("syntax failure returned result")
					}
				}
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "garbage"`, Position: "50"})
				if len(notices) != 0 {
					t.Fatal(notices)
				}
			}
			referenceRunStep(t, p.ctx, c, record, referenceExec("CREATE TABLE syntax_prefix(id INT)", 0))
			referenceRunStep(t, p.ctx, c, record, referenceExec("CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "cancel_rows" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal(notices)
			}
		})
	}
}
