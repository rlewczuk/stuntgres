package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyProgressCounters(t *testing.T) {
	for _, tc := range []struct {
		name, options, record        string
		processed, excluded, skipped int64
	}{
		{"ignore_text", "WITH(ON_ERROR ignore)", "1\nbad\n2\n", 2000, 0, 1000},
		{"ignore_csv", "WITH(FORMAT csv, ON_ERROR ignore)", "1\nbad\n2\n", 2000, 0, 1000},
		{"where_false_null", "WHERE CASE WHEN id=3 THEN NULL ELSE id>0 END", "1\n-1\n3\n2\n", 2000, 2000, 0},
		{"conversion_before_where", "WITH(ON_ERROR ignore) WHERE id>0", "1\nbad\n-1\n2\n", 2000, 1000, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_counters(id INT)")
				id := copyProgressIdentify(t, p, worker, "copy_progress_counters")
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_counters FROM STDIN "+tc.options)
				copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				payload := bytes.Repeat([]byte(tc.record), 1000)
				run.input.send(t, p, payload)
				s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool {
					return ok && s.processed.Int64 == tc.processed && s.excluded.Int64 == tc.excluded && s.skipped.Int64 == tc.skipped
				})
				copyProgressVisible(t, s, id, "COPY FROM")
				if s.bytes.Int64 > int64(len(payload)) {
					t.Fatalf("read beyond supplied bytes %+v", s)
				}
				run.finish(t, tc.processed)
				copyProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT count(*) AS n FROM copy_progress_counters", []string{"n"}, []string{"INT8"}, [][]any{{tc.processed}}))
			})
		})
	}
	for _, tc := range []struct {
		name, options, payload string
		want                   fixture.Diagnostic
	}{
		{"stop", "", "1\nbad\n", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Context: "COPY copy_progress_failure, line 2, column id: \"bad\""}},
		{"reject_limit", "WITH(ON_ERROR ignore, REJECT_LIMIT 1)", "bad\nbad\n", fixture.Diagnostic{Code: "22P02", Message: "skipped more than REJECT_LIMIT (1) rows due to data type incompatibility", Context: "COPY copy_progress_failure, line 2, column id: \"bad\""}},
		{"framing", "WITH(FORMAT csv, ON_ERROR ignore)", "\"unclosed\n", fixture.Diagnostic{Code: "22P04", Message: "unterminated CSV quoted field", Context: "COPY copy_progress_failure, line 1: \"\"unclosed\n\""}},
		{"constraint", "WITH(ON_ERROR ignore)", "99\n", fixture.Diagnostic{Code: "23505", Message: "duplicate key value violates unique constraint \"copy_progress_failure_pkey\"", Detail: "Key (id)=(99) already exists.", Context: "COPY copy_progress_failure, line 1", Schema: "public", Table: "copy_progress_failure", Constraint: "copy_progress_failure_pkey"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_failure(id INT PRIMARY KEY)")
				copyProgressExec(t, p.Ctx, observer, "INSERT INTO copy_progress_failure VALUES(99)")
				id := copyProgressIdentify(t, p, worker, "copy_progress_failure")
				result, err := fixture.CopyFrom(p.Ctx, worker, b.PG, "COPY copy_progress_failure FROM STDIN "+tc.options, strings.NewReader(tc.payload))
				if result.Count != 0 || result.CommandTag != "" {
					t.Fatalf("error returned successful result %+v", result)
				}
				want := tc.want
				want.Severity = "ERROR"
				want.SeverityUnlocalized = "ERROR"
				fixture.AssertDiagnostic(t, err, &want)
				copyProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM copy_progress_failure", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
			})
		})
	}
}

func TestReferenceCopyProgressTriggers(t *testing.T) {
	for _, kind := range []string{"trigger", "partition_unique"} {
		t.Run(kind, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				if kind == "trigger" {
					copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_trigger(id INT)")
					copyProgressExec(t, p.Ctx, observer, "CREATE FUNCTION copy_progress_suppress() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN CASE WHEN NEW.id=2 THEN NULL ELSE NEW END; END$$")
					copyProgressExec(t, p.Ctx, observer, "CREATE TRIGGER suppress BEFORE INSERT ON copy_progress_trigger FOR EACH ROW EXECUTE FUNCTION copy_progress_suppress()")
				} else {
					copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_trigger(id INT PRIMARY KEY) PARTITION BY RANGE(id)")
					copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_low PARTITION OF copy_progress_trigger FOR VALUES FROM (0) TO (2000)")
					copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_high PARTITION OF copy_progress_trigger FOR VALUES FROM (2000) TO (4000)")
				}
				id := copyProgressIdentify(t, p, worker, "copy_progress_trigger")
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_trigger FROM STDIN")
				copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				payload := copyProgressIntegers(1000, 2000)
				count := int64(2000)
				if kind == "trigger" {
					payload = bytes.Repeat([]byte("1\n2\n3\n"), 1000)
				}
				run.input.send(t, p, payload)
				s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok && s.processed.Int64 == count })
				copyProgressVisible(t, s, id, "COPY FROM")
				if s.skipped.Int64 != 0 || s.excluded.Int64 != 0 {
					t.Fatalf("suppression/routing misclassified %+v", s)
				}
				run.finish(t, count)
				copyProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT count(*) AS n FROM copy_progress_trigger", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				if kind == "trigger" {
					var suppressed int64
					if err := observer.QueryRowContext(p.Ctx, "SELECT count(*) FROM copy_progress_trigger WHERE id=2").Scan(&suppressed); err != nil || suppressed != 0 {
						t.Fatal(fmt.Sprintf("suppressed row count=%d err=%v", suppressed, err))
					}
				}
			})
		})
	}
}
