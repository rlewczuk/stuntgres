package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

func loggingStdout(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = file
	t.Cleanup(func() {
		os.Stdout = previous
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	return path
}

func loggingRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("invalid JSONL %q: %v", line, err)
		}
		stamp, ok := record["tstamp"].(string)
		if !ok {
			t.Fatalf("missing tstamp: %v", record)
		}
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"time", "level", "msg"} {
			if _, ok := record[key]; ok {
				t.Fatalf("unrenamed standard attribute %q: %v", key, record)
			}
		}
		records = append(records, record)
	}
	return records
}

func TestServerLogging(t *testing.T) {
	t.Run("split mirrors modes and append", func(t *testing.T) {
		stdout := loggingStdout(t)
		dir := filepath.Join(t.TempDir(), "logs")
		logs, err := openLogs(dir, false, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logs.Close() })
		ctx := context.Background()
		if err := logs.operational(ctx, slog.LevelDebug, "hidden"); err != nil {
			t.Fatal(err)
		}
		if err := logs.operational(ctx, slog.LevelInfo, "start", "connection_id", 7, "database", "app", "user", "alice"); err != nil {
			t.Fatal(err)
		}
		query := "bad statement;\n-- full source ☃"
		for _, code := range []string{"42601", "0A000", "23505"} {
			if err := logs.sqlError(ctx, query, &sqlstate.Error{Code: code, Message: "primary"}, nil, "connection_id", 7); err != nil {
				t.Fatal(err)
			}
		}
		if err := logs.Close(); err != nil {
			t.Fatal(err)
		}
		if err := logs.Close(); err != nil {
			t.Fatal(err)
		}
		if err := logs.operational(ctx, slog.LevelInfo, "after close"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed sink: %v", err)
		}
		operations := loggingRecords(t, filepath.Join(dir, "server.log"))
		if len(operations) != 1 || operations[0]["message"] != "start" || operations[0]["severity"] != "INFO" || operations[0]["user"] != "alice" {
			t.Fatal(operations)
		}
		sql := loggingRecords(t, filepath.Join(dir, "error.log"))
		if len(sql) != 3 {
			t.Fatal(sql)
		}
		for i, kind := range []string{"SYNTAX", "UNSUPPORTED", "SQL"} {
			r := sql[i]
			if r["kind"] != kind || r["severity"] != "ERROR" || r["query"] != query || r["errmsg"] != "primary" || r["message"] != "primary" || r["error"] != []string{"42601", "0A000", "23505"}[i] || !reflect.DeepEqual(r["params"], []any{}) {
				t.Fatal(r)
			}
		}
		mirrors := loggingRecords(t, stdout)
		want := append(operations, sql...)
		if !reflect.DeepEqual(mirrors, want) {
			t.Fatalf("mirror mismatch: %v != %v", mirrors, want)
		}
		for _, entry := range []struct {
			path string
			mode os.FileMode
		}{{dir, 0700}, {filepath.Join(dir, "server.log"), 0600}, {filepath.Join(dir, "error.log"), 0600}} {
			info, err := os.Stat(entry.path)
			if err != nil || info.Mode().Perm() != entry.mode {
				t.Fatalf("mode %s: %v, %v", entry.path, info, err)
			}
		}
		logs, err = openLogs(dir, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := logs.operational(ctx, slog.LevelDebug, "debug"); err != nil {
			t.Fatal(err)
		}
		if err := logs.Close(); err != nil {
			t.Fatal(err)
		}
		operations = loggingRecords(t, filepath.Join(dir, "server.log"))
		if len(operations) != 2 || operations[1]["severity"] != "DEBUG" {
			t.Fatal(operations)
		}
	})
	t.Run("stdout only is not owned", func(t *testing.T) {
		stdout := loggingStdout(t)
		logs, err := openLogs("", true, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := logs.operational(context.Background(), slog.LevelDebug, "debug"); err != nil {
			t.Fatal(err)
		}
		if err := logs.sqlError(context.Background(), "source", errors.New("unexpected"), nil); err != nil {
			t.Fatal(err)
		}
		if err := logs.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stdout.Write(nil); err != nil {
			t.Fatalf("stdout closed: %v", err)
		}
		r := loggingRecords(t, stdout)
		if len(r) != 2 || r[0]["severity"] != "DEBUG" || r[1]["error"] != "XX000" {
			t.Fatal(r)
		}
	})
	t.Run("query log is file only", func(t *testing.T) {
		stdout := loggingStdout(t)
		dir := filepath.Join(t.TempDir(), "logs")
		if logs, err := openLogs("", false, true); err == nil || logs != nil {
			t.Fatalf("query log without directory accepted: %v %v", logs, err)
		}
		logs, err := openLogs(dir, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if logs.queries != nil {
			t.Fatal("query log enabled by default")
		}
		if err := logs.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "query.log")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("query.log created without the option: %v", err)
		}
		for i := range 2 {
			logs, err = openLogs(dir, false, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := logs.query(context.Background(), "cycle", i+1, "query", "SELECT 1"); err != nil {
				t.Fatal(err)
			}
			if err := logs.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if err := logs.query(context.Background(), "query", "late"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed query sink: %v", err)
		}
		records := loggingRecords(t, filepath.Join(dir, "query.log"))
		if len(records) != 2 || records[0]["cycle"] != float64(1) || records[1]["cycle"] != float64(2) || records[1]["message"] != "query" || records[1]["severity"] != "INFO" || records[1]["query"] != "SELECT 1" {
			t.Fatal(records)
		}
		if info, err := os.Stat(filepath.Join(dir, "query.log")); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("query.log mode: %v, %v", info, err)
		}
		if len(loggingRecords(t, stdout)) != 0 || len(loggingRecords(t, filepath.Join(dir, "server.log"))) != 0 {
			t.Fatal("query records mirrored")
		}
	})
	t.Run("open failures are visible", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "error.log"), 0700); err != nil {
			t.Fatal(err)
		}
		if logs, err := openLogs(dir, false, false); err == nil || logs != nil {
			t.Fatalf("missing error sink accepted: %v %v", logs, err)
		}
		if logs, err := openLogs(filepath.Join(dir, "error.log", "..", "server.log", "child"), false, false); err == nil || logs != nil {
			t.Fatalf("invalid log directory accepted: %v %v", logs, err)
		}
	})
	t.Run("write and close errors", func(t *testing.T) {
		first, second := errors.New("first"), errors.New("second")
		var mirror bytes.Buffer
		logs := &serverLogs{ops: logHandler(logMirrors{loggingFailedWriter{first}, &mirror, loggingFailedWriter{second}}, false)}
		err := logs.operational(context.Background(), slog.LevelInfo, "visible")
		if !errors.Is(err, first) || !errors.Is(err, second) || !strings.Contains(mirror.String(), "visible") {
			t.Fatalf("lost write errors or mirror: %v %q", err, mirror.String())
		}
		logs.sql = logHandler(loggingFailedWriter{second}, false)
		if err := logs.sqlError(context.Background(), "source", errors.New("sql"), nil); !errors.Is(err, second) {
			t.Fatalf("SQL sink failure lost: %v", err)
		}
		if _, err := (logMirrors{loggingShortWriter{}}).Write([]byte("record")); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
		one, two := &loggingFailedCloser{err: first}, &loggingFailedCloser{err: second}
		logs.files = []io.Closer{one, two}
		for range 2 {
			err := logs.Close()
			if !errors.Is(err, first) || !errors.Is(err, second) {
				t.Fatal(err)
			}
		}
		if one.calls != 1 || two.calls != 1 {
			t.Fatalf("close counts: %d %d", one.calls, two.calls)
		}
	})
	t.Run("concurrent records", func(t *testing.T) {
		var output bytes.Buffer
		logs := &serverLogs{ops: logHandler(&output, true), sql: logHandler(&output, true)}
		var workers sync.WaitGroup
		for i := range 32 {
			workers.Add(1)
			go func(id int) {
				defer workers.Done()
				if err := logs.operational(context.Background(), slog.LevelInfo, "connection", "connection_id", id); err != nil {
					t.Error(err)
				}
				if err := logs.sqlError(context.Background(), "source", errors.New("error"), nil, "connection_id", id); err != nil {
					t.Error(err)
				}
			}(i)
		}
		workers.Wait()
		lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte{'\n'})
		if len(lines) != 64 {
			t.Fatal(len(lines))
		}
		for _, line := range lines {
			if !json.Valid(line) {
				t.Fatalf("interleaved record: %q", line)
			}
		}
	})
	t.Run("diagnostic preservation", func(t *testing.T) {
		native := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax", Detail: "detail", Hint: "hint", Position: "4", InternalPosition: "2", InternalQuery: "internal", Context: "context", Schema: "schema", Table: "table", Column: "column", DataType: "type", Constraint: "constraint", File: "file", Line: "1", Routine: "routine"}
		if got := diagnostic(fmt.Errorf("wrapper: %w", native)); got != native {
			t.Fatalf("native diagnostic replaced: %+v", got)
		}
		foreign := diagnostic(fmt.Errorf("wrapper: %w", loggingStateError{}))
		if !reflect.DeepEqual(foreign, &sqlstate.Error{Code: "22012", Message: "foreign primary"}) {
			t.Fatalf("invented foreign fields: %+v", foreign)
		}
		for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
			got := diagnostic(fmt.Errorf("wrapper: %w", err))
			if got.Code != "57014" || got.Message != "wrapper: "+err.Error() || got.Severity != "" {
				t.Fatal(got)
			}
		}
		if got := diagnostic(errors.New("unexpected")); !reflect.DeepEqual(got, &sqlstate.Error{Code: "XX000", Message: "unexpected"}) {
			t.Fatal(got)
		}
		if diagnostic(nil) != nil {
			t.Fatal("nil diagnostic")
		}
	})
}

type loggingFailedWriter struct{ err error }

func (w loggingFailedWriter) Write([]byte) (int, error) { return 0, w.err }

type loggingShortWriter struct{}

func (loggingShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type loggingFailedCloser struct {
	err   error
	calls int
}

func (c *loggingFailedCloser) Close() error {
	c.calls++
	return c.err
}

type loggingStateError struct{}

func (loggingStateError) Error() string    { return "foreign primary" }
func (loggingStateError) SQLState() string { return "22012" }
