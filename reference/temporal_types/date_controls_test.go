package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDateDumpRestoreAndPostgresReplay(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.DateRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(`CREATE TABLE d(id INTEGER,v "date")`, 0))
	fixture.DateRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO d VALUES(1,'-infinity'),(2,'4714-11-24 BC'),(3,'0001-02-29 BC'),(4,'epoch'),(5,'epoch'),(6,'5874897-12-31'),(7,'infinity'),(8,NULL)", 8))
	old, err := p.Native.QueryContext(p.Ctx, "SELECT v FROM d ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
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
	if err = stuntgres.Restore(p.Ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err = p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal(err, dump.String())
		}
	}
	native := [][]any{{"-infinity"}, {"4714-11-24 BC"}, {"0001-02-29 BC"}, {"1970-01-01"}, {"1970-01-01"}, {"5874897-12-31"}, {"infinity"}, {nil}}
	pg := [][]any{{"-infinity"}, {time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC)}, {time.Date(0, 2, 29, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(5874897, 12, 31, 0, 0, 0, 0, time.UTC)}, {"infinity"}, {nil}}
	q := "SELECT v FROM d ORDER BY id"
	if p.Postgres != nil {
		fixture.DateRunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(q, []string{"v"}, []string{"DATE"}, pg))
	}
	var again bytes.Buffer
	fixture.DateRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select(q, []string{"v"}, []string{"DATE"}, native))
	rows, err := restored.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	fixture.DateConsume(t, rows, false, fixture.Select(q, []string{"v"}, []string{"DATE"}, native))
	if err = stuntgres.Dump(p.Ctx, restored, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, "redump differs")
	}
	if err = stuntgres.Restore(p.Ctx, restored, strings.NewReader(dump.String()+"CREATE TABLE broken(v DATE); INSERT INTO broken VALUES('bad');")); err == nil {
		t.Fatal("invalid DATE restore succeeded")
	}
	again.Reset()
	if err = stuntgres.Dump(p.Ctx, restored, &again); err != nil || again.String() != dump.String() {
		t.Fatal(err, "failed restore replaced catalog")
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.DateConsume(t, old, false, fixture.Select(q, []string{"v"}, []string{"DATE"}, native))
}
