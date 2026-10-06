//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceDateDumpRestoreAndPostgresReplay(t *testing.T) {
	p := newReferencePair(t)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(`CREATE TABLE d(id INTEGER,v "date")`, 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO d VALUES(1,'-infinity'),(2,'4714-11-24 BC'),(3,'0001-02-29 BC'),(4,'epoch'),(5,'epoch'),(6,'5874897-12-31'),(7,'infinity'),(8,NULL)", 8))
	old, err := p.native.QueryContext(p.ctx, "SELECT v FROM d ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"DATE", "'4714-11-24 BC'", "'0001-02-29 BC'", "'1970-01-01'", "'5874897-12-31'", "'-infinity'", "'infinity'", "NULL"} {
		if !strings.Contains(dump.String(), s) {
			t.Fatal(s, dump.String())
		}
	}
	c, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	restored := sql.OpenDB(c)
	defer restored.Close()
	if err = stuntgres.Restore(p.ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err, dump.String())
	}
	native := [][]any{{"-infinity"}, {"4714-11-24 BC"}, {"0001-02-29 BC"}, {"1970-01-01"}, {"1970-01-01"}, {"5874897-12-31"}, {"infinity"}, {nil}}
	pg := [][]any{{"-infinity"}, {time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)}, {time.Date(0, 2, 29, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(5874897, 12, 31, 0, 0, 0, 0, time.UTC)}, {"infinity"}, {nil}}
	q := "SELECT v FROM d ORDER BY id"
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, []string{"v"}, []string{"DATE"}, pg))
	var again bytes.Buffer
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect(q, []string{"v"}, []string{"DATE"}, native))
	rows, err := restored.QueryContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "DATE" {
		t.Fatal(meta, err)
	}
	for _, want := range native {
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var got any
		if err = rows.Scan(&got); err != nil || got != want[0] {
			t.Fatal(got, want, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	if err = stuntgres.Dump(p.ctx, restored, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, "redump differs")
	}
	if err = stuntgres.Restore(p.ctx, restored, strings.NewReader(dump.String()+"CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('bad');")); err == nil {
		t.Fatal("invalid DATE restore succeeded")
	}
	again.Reset()
	if err = stuntgres.Dump(p.ctx, restored, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, "failed restore replaced catalog")
	}
	if err = stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	for _, want := range native {
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var got any
		if err = old.Scan(&got); err != nil || got != want[0] {
			t.Fatal(got, want, err)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
}
