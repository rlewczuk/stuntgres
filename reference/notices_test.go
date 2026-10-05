//go:build reference

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func referenceNoticeExecutionError(t *testing.T, ctx context.Context, c *sql.Conn, record *referenceRecorder, notices *[]referenceDiagnostic, s referenceStep) {
	t.Helper()
	s.errorStage = "execute"
	before := len(*notices)
	if !s.prepared {
		referenceRunStep(t, ctx, c, record, s)
	} else {
		ctx = context.WithValue(ctx, referenceRecorderKey{}, record)
		stmt, err := c.PrepareContext(ctx, s.query)
		if err != nil {
			t.Fatal("semantic failure during preparation", err)
		}
		defer stmt.Close()
		if len(*notices) != before {
			t.Fatal("preparation emitted notice", (*notices)[before:])
		}
		_, err = stmt.ExecContext(ctx)
		referenceCheckDiagnostic(t, err, s, "execute")
		referenceState(t, err, s.state)
	}
	if len(*notices) != before {
		t.Fatal("semantic error emitted notice", (*notices)[before:])
	}
}
func TestReferenceNoticesCommands(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			for _, postgres := range []bool{false, true} {
				t.Run(fmt.Sprint(postgres), func(t *testing.T) {
					var notices []referenceDiagnostic
					_, c, record := noticeReferencePool(t, p, postgres, &notices)
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, record, s) }
					run(referenceExec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
					run(referenceExec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
					if len(notices) != 0 {
						t.Fatal(notices)
					}
					want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
					for _, query := range []string{"CREATE TABLE IF NOT EXISTS notice_rows(replaced BOOL)", "CREATE TABLE IF NOT EXISTS public.notice_rows(id INT,id TEXT)"} {
						run(referenceExec(query, 0))
						if notices[len(notices)-1] != want {
							t.Fatal(notices)
						}
						run(referenceSelect("SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
					}
					if len(notices) != 2 {
						t.Fatal(notices)
					}
					failure := referenceImplicitError("CREATE TABLE IF NOT EXISTS fresh(id INT,id TEXT)", referenceDiagnostic{Code: "42701", Message: `column "id" specified more than once`})
					failure.prepared = prepared
					referenceNoticeExecutionError(t, p.ctx, c, record, &notices, failure)
					run(referenceExec("CREATE TABLE IF NOT EXISTS fresh(id INT)", 0))
					run(referenceExec("SET client_min_messages TO error", 0))
					run(referenceExec("CREATE TABLE IF NOT EXISTS notice_rows(id INT)", 0))
					run(referenceImplicitError("CREATE TABLE notice_rows(id INT)", referenceDiagnostic{Code: "42P07", Message: `relation "notice_rows" already exists`}))
					if len(notices) != 2 {
						t.Fatal(notices)
					}
				})
			}
		})
	}
}

// Select the independently specified feature witness without claiming that the
// native engine produces PostgreSQL's transaction/parser DEBUG diagnostics.
// Keep every received record in the caller's capture and report allowed extras;
// any other diagnostic remains a failure, including a malformed witness.
func referenceNoticeWitnesses(t *testing.T, postgres bool, records []referenceDiagnostic, want referenceDiagnostic) []referenceDiagnostic {
	t.Helper()
	var witnesses []referenceDiagnostic
	for _, record := range records {
		if record.Code == want.Code && record.Message == want.Message {
			witnesses = append(witnesses, record)
			continue
		}
		if !postgres || record.Severity != "DEBUG" || record.SeverityUnlocalized != "DEBUG" || record.Code != "00000" {
			t.Fatalf("unexpected non-witness diagnostic: %#v (witness %#v)", record, want)
		}
		t.Logf("PostgreSQL internal DEBUG diagnostic: %#v", record)
	}
	return witnesses
}
func TestReferenceNoticesFiltering(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, postgres, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			run(referenceSelect("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}}))
			run(referenceExec("CREATE TABLE filtered(id INT)", 0))
			for _, threshold := range []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"} {
				run(referenceExec("SET client_min_messages TO "+threshold, 0))
				before := len(notices)
				original := strings.Repeat("a", 64)
				clipped := strings.Repeat("a", 63)
				run(referencePrepared(referenceExec("SET application_name TO '"+original+"'", 0)))
				run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{clipped}}))
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
				witnesses := referenceNoticeWitnesses(t, postgres, notices[before:], want)
				if threshold == "warning" || threshold == "error" {
					if len(witnesses) != 0 {
						t.Fatal(threshold, witnesses)
					}
				} else if len(witnesses) != 1 || witnesses[0] != want {
					t.Fatal(threshold, witnesses)
				}
				before = len(notices)
				run(referenceExec("CREATE TABLE IF NOT EXISTS filtered(other TEXT)", 0))
				want = referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "filtered" already exists, skipping`}
				witnesses = referenceNoticeWitnesses(t, postgres, notices[before:], want)
				if threshold == "warning" || threshold == "error" {
					if len(witnesses) != 0 {
						t.Fatal(threshold, witnesses)
					}
				} else if len(witnesses) != 1 || witnesses[0] != want {
					t.Fatal(threshold, witnesses)
				}
				before = len(notices)
				run(referenceImplicitError("CREATE TABLE filtered(other TEXT)", referenceDiagnostic{Code: "42P07", Message: `relation "filtered" already exists`}))
				errorWitness := referenceDiagnostic{Code: "42P07", Message: `relation "filtered" already exists`}
				if witnesses := referenceNoticeWitnesses(t, postgres, notices[before:], errorWitness); len(witnesses) != 0 {
					t.Fatal("error as notice", witnesses)
				}
			}
			// Independently check both execution paths and unchanged effective
			// state before any subsequent assignment on each backend.
			run(referenceExec("SET client_min_messages TO warning", 0))
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("SET client_min_messages TO bogus", referenceDiagnostic{Code: "22023", Message: `invalid value for parameter "client_min_messages": "bogus"`, Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."})
				failure.prepared = prepared
				referenceNoticeExecutionError(t, p.ctx, c, record, &notices, failure)
				run(referenceSelect("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"warning"}}))
			}
			run(referenceExec("SET client_min_messages TO debug", 0))
			run(referenceSelect("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"debug2"}}))
			run(referenceExec("SET client_min_messages TO DEFAULT", 0))
		})
	}
}
func TestReferenceNoticesPayload(t *testing.T) {
	p := newReferencePair(t)
	var notices []referenceDiagnostic
	_, c, _ := noticeReferencePool(t, p, true, &notices)
	query := `DO $$ BEGIN RAISE WARNING USING ERRCODE='01000', MESSAGE='field witness', DETAIL='facts', HINT='advice', SCHEMA='s', TABLE='t', COLUMN='c', DATATYPE='d', CONSTRAINT='k'; END $$;`
	if _, err := c.ExecContext(p.ctx, query); err != nil {
		t.Fatal(err)
	}
	want := []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: "field witness", Detail: "facts", Hint: "advice", Context: "PL/pgSQL function inline_code_block line 1 at RAISE", Schema: "s", Table: "t", Column: "c", DataType: "d", Constraint: "k"}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatalf("got %#v want %#v", notices, want)
	}
	notices = nil
	_, err := c.ExecContext(p.ctx, `DO $$ BEGIN RAISE NOTICE USING ERRCODE='01000', MESSAGE='before error'; RAISE EXCEPTION 'after notice'; END $$;`)
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0001", Message: "after notice", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"})
	want = []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "01000", Message: "before error", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices)
	}
}
func TestReferenceNoticesSeverity(t *testing.T) {
	p := newReferencePair(t)
	var notices []referenceDiagnostic
	_, c, _ := noticeReferencePool(t, p, true, &notices)
	thresholds := []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"}
	ranks := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 3}
	for i, threshold := range thresholds {
		if _, err := c.ExecContext(p.ctx, "SET client_min_messages TO "+threshold); err != nil {
			t.Fatal(err)
		}
		for _, level := range []struct {
			name, severity string
			rank           int
		}{{"DEBUG", "DEBUG", 4}, {"LOG", "LOG", 5}, {"INFO", "INFO", 6}, {"NOTICE", "NOTICE", 7}, {"WARNING", "WARNING", 8}} {
			notices = nil
			_, err := c.ExecContext(p.ctx, `DO $$ BEGIN RAISE `+level.name+` USING ERRCODE='01000', MESSAGE='filter witness'; END $$;`)
			if err != nil {
				t.Fatal(err)
			}
			deliver := level.rank == 6 || level.rank >= ranks[i]
			witness := referenceDiagnostic{Severity: level.severity, SeverityUnlocalized: level.severity, Code: "01000", Message: "filter witness", Context: "PL/pgSQL function inline_code_block line 1 at RAISE"}
			want := []referenceDiagnostic(nil)
			if deliver {
				want = []referenceDiagnostic{witness}
			}
			got := referenceNoticeWitnesses(t, true, notices, witness)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s: %#v want %#v", threshold, level.name, got, want)
			}
		}
	}
}
func TestReferenceNoticesPermission(t *testing.T) {
	p := newReferencePair(t)
	var notices []referenceDiagnostic
	_, c, _ := noticeReferencePool(t, p, true, &notices)
	role := "notices_no_create"
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE ROLE "+role+" NOLOGIN; GRANT USAGE ON SCHEMA public TO "+role); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, err := referenceAdmin.ExecContext(context.Background(), "REVOKE ALL ON SCHEMA public FROM "+role+"; DROP ROLE "+role)
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err := c.ExecContext(p.ctx, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT); INSERT INTO notice_rows VALUES(7,'kept'); SET ROLE "+role); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(context.Background(), "RESET ROLE")
	_, err := c.ExecContext(p.ctx, "CREATE TABLE IF NOT EXISTS notice_rows(other INT)")
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema public", Position: "28"})
	if len(notices) != 0 {
		t.Fatal(notices)
	}
	if _, err = c.ExecContext(p.ctx, "RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, c, &referenceRecorder{}, referenceSelect(
		"SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"},
		[][]any{{int64(7), "kept"}}))
}

func TestReferenceNoticesQuery(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, postgres, &notices)
			referenceRunStep(t, p.ctx, c, record, referenceExec("CREATE TABLE query_notice(id INT)", 0))
			for _, prepared := range []bool{false, true} {
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
				query := "CREATE TABLE IF NOT EXISTS query_notice(other TEXT)"
				before := len(notices)
				var rows *sql.Rows
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = c.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if stmt != nil {
							if err := stmt.Close(); err != nil {
								t.Error(err)
							}
						}
					}()
					if len(notices) != before {
						t.Fatal("prepare notice")
					}
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, query)
				}
				if rows != nil {
					defer rows.Close()
				}
				if err != nil {
					t.Fatal(err)
				}
				columns, e := rows.Columns()
				if e != nil || len(columns) != 0 || rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal(columns, e, rows.Err())
				}
				if err = rows.Close(); err != nil {
					t.Fatal(err)
				}
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					stmt = nil
				}
				want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "query_notice" already exists, skipping`}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal(notices)
				}
				record.mu.Lock()
				op := record.operations[len(record.operations)-1]
				recordedError, closed, count := op.Err, op.Closed, len(op.Rows)
				record.mu.Unlock()
				if recordedError != nil || !closed || count != 0 {
					t.Fatal(recordedError, closed, count)
				}
			}
		})
	}
}

func TestReferenceNoticesControls(t *testing.T) {
	p := newReferencePair(t)
	var dump bytes.Buffer
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "IF NOT EXISTS") {
		t.Fatal(dump.String())
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect("SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE TABLE IF NOT EXISTS notice_rows(other BOOL);")); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT * FROM notice_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
}

func TestReferenceNoticesApplicationName(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, postgres, &notices)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ original, clipped string }{{strings.Repeat("a", 64), strings.Repeat("a", 63)}, {strings.Repeat("a", 62) + "é", strings.Repeat("a", 62)}} {
					before := len(notices)
					q := "SET application_name TO '" + tc.original + "'"
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
					var rows *sql.Rows
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = c.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if stmt != nil {
								if err := stmt.Close(); err != nil {
									t.Error(err)
								}
							}
						}()
						if len(notices) != before {
							t.Fatal("prepare notice")
						}
						rows, err = stmt.QueryContext(ctx)
					} else {
						rows, err = c.QueryContext(ctx, q)
					}
					if rows != nil {
						defer rows.Close()
					}
					if err != nil {
						t.Fatal(err)
					}
					columns, e := rows.Columns()
					if e != nil || len(columns) != 0 || rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal(columns, e, rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if stmt != nil {
						if err := stmt.Close(); err != nil {
							t.Fatal(err)
						}
						stmt = nil
					}
					want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.original + `" will be truncated to "` + tc.clipped + `"`}
					if len(notices) != before+1 || notices[before] != want {
						t.Fatal(notices)
					}
					referenceRunStep(t, p.ctx, c, record, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{tc.clipped}}))
				}
			}
		})
	}
}
