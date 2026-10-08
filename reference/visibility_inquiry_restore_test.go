//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceVisibilityInquiryNoPrincipalRestore(t *testing.T) {
	p := newReferencePair(t)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("CREATE TABLE restored(id INT)", 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO restored VALUES(7)", 1))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Replay the actual public native dump on the independent pinned server.
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT id FROM restored", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.step(t, referenceSelect("SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))

	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-independent")
	if err != nil {
		t.Fatal(err)
	}
	target := sql.OpenDB(connector)
	defer target.Close()
	if err := stuntgres.Restore(p.ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var value int64
	if err := target.QueryRowContext(p.ctx, "SELECT id FROM restored").Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	if err := stuntgres.Restore(p.ctx, target, strings.NewReader("CREATE TABLE broken(id INT); INSERT INTO broken VALUES('bad');")); err == nil {
		t.Fatal("invalid restore published")
	}
	if err := target.QueryRowContext(p.ctx, "SELECT id FROM restored").Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	var collation, conversion bool
	if err := target.QueryRowContext(p.ctx, "SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4402)").Scan(&collation, &conversion); err != nil || !collation || !conversion {
		t.Fatal(collation, conversion, err)
	}
}
