package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceTimeDumpRestoreAndPostgresReplay(t *testing.T) {
	p := newReferenceScenario(t)
	referenceTemporalRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE clocks(id INTEGER,v TIME(0),fine TIME(6),plain TIME)", 0))
	referenceTemporalRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO clocks VALUES(1,'00:00','00:00','allballs'),(2,'24:00','24:00','24:00'),(3,'04:05 PM','04:05:06.12',NULL)", 3))
	referenceTemporalRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE copied(LIKE clocks)", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"TIME(0)", "TIME(6)", "'24:00:00'", "'04:05:06.12'", "NULL"} {
		if !strings.Contains(dump.String(), needle) {
			t.Fatal(needle, dump.String())
		}
	}
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/restored")
	if err != nil {
		t.Fatal(err)
	}
	restored := sql.OpenDB(connector)
	defer restored.Close()
	if err = stuntgres.Restore(p.ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Real PostgreSQL consumes the unmodified native plain-SQL serialization.
	if p.postgres != nil {
		if _, err = p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL rejects native TIME dump", err, dump.String())
		}
	}
	names, types := []string{"id", "v", "fine", "plain"}, []string{"INT4", "TIME", "TIME", "TIME"}
	want := [][]any{{int64(1), "00:00:00", "00:00:00", "00:00:00"}, {int64(2), "24:00:00", "24:00:00", "24:00:00"}, {int64(3), "16:05:00", "04:05:06.12", nil}}
	q := "SELECT id,v,fine,plain FROM clocks ORDER BY id"
	referenceTemporalRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect(q, names, types, want))
	if p.postgres != nil {
		referenceTemporalRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceSelect(q, names, types, referenceTimeRows(t, true, types, want)))
	}
	rows, err := restored.QueryContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	referenceTimeConsume(t, rows, false, referenceSelect(q, names, types, want))
	var again bytes.Buffer
	if err = stuntgres.Dump(p.ctx, restored, &again); err != nil || again.String() != dump.String() {
		t.Fatal("redump differs", err)
	}
	if err = stuntgres.Restore(p.ctx, restored, strings.NewReader(dump.String()+"CREATE TABLE broken(v TIME); INSERT INTO broken VALUES('bad');")); err == nil {
		t.Fatal("malformed Restore succeeded")
	}
	if err = stuntgres.Restore(p.ctx, restored, strings.NewReader(dump.String()+"CREATE TABLE broken(v TIME); INSERT INTO broken VALUES('00:00'),('bad');")); err == nil {
		t.Fatal("later staged failure succeeded")
	}
	rows, err = restored.QueryContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	referenceTimeConsume(t, rows, false, referenceSelect(q, names, types, want))
	var preserved bytes.Buffer
	if err = stuntgres.Dump(p.ctx, restored, &preserved); err != nil || preserved.String() != dump.String() {
		t.Fatal("failed restore changed catalog", err)
	}
}
