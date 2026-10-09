//go:build reference

package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"
)

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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := referenceAdmin.ExecContext(ctx, "REVOKE ALL ON SCHEMA public FROM "+role+"; DROP ROLE "+role)
		if err != nil {
			t.Error(err)
		}
	}()
	if _, err := c.ExecContext(p.ctx, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT); INSERT INTO notice_rows VALUES(7,'kept'); SET ROLE "+role); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
			t.Error(err)
		}
	}()
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
