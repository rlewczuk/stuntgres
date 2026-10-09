package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNoticesControls(t *testing.T) {
	p := newReferenceScenario(t)
	var dump bytes.Buffer
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, record, s) }
			if !backend.pg {
				run(referenceExec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
				run(referenceExec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
			} else {
				// Consume the unchanged native owner-bearing stream, as the
				// legacy union did; PostgreSQL has no host Reset/Restore API.
				if _, err := c.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal(notices)
			}
			if !backend.pg {
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "IF NOT EXISTS") {
					t.Fatal(dump.String())
				}
			}
			run(referenceExec("SET client_min_messages TO warning", 0))
			if !backend.pg {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(dump.String()+"CREATE TABLE IF NOT EXISTS notice_rows(x BOOL);")); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bad(id INT NOT NULL); INSERT INTO bad VALUES(NULL);")); err == nil {
					t.Fatal("restore succeeded")
				}
			} else {
				run(referenceExec("CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", 0))
			}
			noticesRows(t, p.ctx, c, record)
			noticesShow(t, p.ctx, c, record, "client_min_messages", "warning")
			if len(notices) != 1 || notices[0] != want {
				t.Fatal("staging delivered notice", notices)
			}
			run(referenceExec("SET client_min_messages TO notice", 0))
			run(referenceExec("CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", 0))
			if len(notices) != 2 || notices[1] != want {
				t.Fatal("notice configuration lost", notices)
			}
		})
	}
}

func TestReferenceNoticesRetainedSnapshot(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, backend.pg, &notices)
			referenceRunStep(t, p.ctx, c, record, referenceExec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
			referenceRunStep(t, p.ctx, c, record, referenceExec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
			snapshot, err := c.QueryContext(ctx, "SELECT * FROM notice_rows")
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			var peerNotices []referenceDiagnostic
			_, peer, peerRecord := noticeReferencePool(t, p, backend.pg, &peerNotices)
			referenceRunStep(t, p.ctx, peer, peerRecord, referenceExec("CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", 0))
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
			if len(peerNotices) != 1 || peerNotices[0] != want || len(notices) != 0 {
				t.Fatal(peerNotices, notices)
			}
			if !backend.pg {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			}
			columns, err := snapshot.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"id", "note"}) {
				t.Fatal(columns, err)
			}
			columns[0] = "mutated"
			columns, err = snapshot.Columns()
			expectedColumns := []string{"id", "note"}
			if backend.pg {
				expectedColumns[0] = "mutated"
			}
			if err != nil || !reflect.DeepEqual(columns, expectedColumns) {
				t.Fatal("backend column buffer contract", columns, err)
			}
			// pq exposes its descriptor buffer; restore the original label
			// before checking the retained consumer's remaining metadata.
			if backend.pg {
				columns[0] = "id"
			}
			types, err := snapshot.ColumnTypes()
			if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal(types, err)
			}
			intType := reflect.TypeOf(int64(0))
			if backend.pg {
				intType = reflect.TypeOf(int32(0))
			}
			if types[0].ScanType() != intType || types[1].ScanType() != reflect.TypeOf("") {
				t.Fatal(types)
			}
			if !snapshot.Next() {
				t.Fatal("snapshot erased", snapshot.Err())
			}
			var rawID, rawNote any
			if err := snapshot.Scan(&rawID, &rawNote); err != nil {
				t.Fatal(err)
			}
			if rawID != int64(7) || rawNote != "kept" {
				t.Fatal(rawID, rawNote)
			}
			var id int
			var note string
			if err := snapshot.Scan(&id, &note); err != nil || id != 7 || note != "kept" {
				t.Fatal(id, note, err)
			}
			if snapshot.Next() || snapshot.Err() != nil {
				t.Fatal("extra snapshot rows", snapshot.Err())
			}
			if snapshot.Next() || snapshot.Err() != nil {
				t.Fatal("unstable EOF", snapshot.Err())
			}
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
			record.mu.Lock()
			op := record.operations[len(record.operations)-1]
			closed, recordedRows, recordedError, closeErr := op.Closed, op.Rows, op.Err, op.CloseErr
			record.mu.Unlock()
			if !closed || recordedError != nil || closeErr != nil || !reflect.DeepEqual(recordedRows, [][]driver.Value{{int64(7), "kept"}}) {
				t.Fatal(closed, recordedRows, recordedError, closeErr)
			}
		})
	}
}
