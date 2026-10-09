package reference_test

import (
	"bytes"
	"database/sql/driver"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCharacterRetainedControlMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(id INT,c CHAR(4),v VARCHAR(20),u BPCHAR,w VARCHAR,t TEXT)", 0))
	p.step(t, referenceExec("CREATE TABLE empty(c CHAR(2),v VARCHAR(3),u BPCHAR)", 0))
	escaped := "O'Brien\n\\tail  "
	p.step(t, referenceExec("INSERT INTO c VALUES(1,'é🙂','ok  ',$1,$2,$3),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3, escaped, escaped, escaped))
	want := [][]any{{int64(1), "é🙂  ", "ok  ", escaped, escaped, escaped}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT * FROM c ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		names := held.(driver.RowsColumnTypeDatabaseTypeName)
		lengths := held.(driver.RowsColumnTypeLength)
		check := func() {
			for i, typ := range []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"} {
				if names.ColumnTypeDatabaseTypeName(i) != typ {
					t.Fatal("retained type changed", i)
				}
				length, known := lengths.ColumnTypeLength(i)
				if i == 0 {
					if known {
						t.Fatal("integer string length")
					}
				} else if !known || length != []int64{0, 4, 20, math.MaxInt64, math.MaxInt64, math.MaxInt64}[i] {
					t.Fatal(i, length, known)
				}
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		if !strings.Contains(dump.String(), "BPCHAR") {
			t.Fatal("unlimited bpchar lost", dump.String())
		}
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO c VALUES(4,'new','new','new','new','new')", 1))
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal(redump.String(), err)
		}
		p.step(t, referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "u", "w", "t"}, []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"}, want))
		for _, backend := range p.backends() {
			referenceCharacterMetadata(t, p, backend.db, "SELECT * FROM empty", false, !backend.pg, []string{"c", "v", "u"}, []string{"BPCHAR", "VARCHAR", "BPCHAR"}, []int64{2, 3, -1}, []referenceCharacterNullability{{true, true}, {true, true}, {true, true}})
		}
		check()
		dst := make([]driver.Value, 6)
		for _, row := range want {
			if err := held.Next(dst); err != nil {
				return err
			}
			for i, value := range dst {
				if !reflect.DeepEqual(value, row[i]) {
					t.Fatal(dst, row)
				}
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		check()
		if err := held.Close(); err != nil {
			return err
		}
		check()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReferenceCharacterRestoreFailures(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(id INT,c CHAR(4),v VARCHAR(20),u BPCHAR,w VARCHAR,t TEXT)", 0))
	p.step(t, referenceExec("CREATE TABLE empty(c CHAR(2),v VARCHAR(3),u BPCHAR)", 0))
	escaped := "O'Brien\n\\tail  "
	p.step(t, referenceExec("INSERT INTO c VALUES(1,'é🙂','ok  ',$1,$2,$3),(2,'','','','',''),(3,NULL,NULL,NULL,NULL,NULL)", 3, escaped, escaped, escaped))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := referenceSelect("SELECT * FROM c ORDER BY id", []string{"id", "c", "v", "u", "w", "t"}, []string{"INT4", "BPCHAR", "VARCHAR", "BPCHAR", "VARCHAR", "TEXT"}, [][]any{{int64(1), "é🙂  ", "ok  ", escaped, escaped, escaped}, {int64(2), "    ", "", "", "", ""}, {int64(3), nil, nil, nil, nil, nil}})
	for _, script := range []string{"CREATE TABLE staged(c CHAR(2)); INSERT INTO staged VALUES('ok'),('bad')", "CREATE TABLE staged(v VARCHAR(2)); INSERT INTO staged VALUES('ok'),('é🙂x')", "CREATE TABLE staged(c CHAR(2)); INSERT INTO staged VALUES(CAST($1 AS char(2)))"} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		p.step(t, want)
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &unchanged); err != nil || unchanged.String() != dump.String() {
			t.Fatal("failed restore changed catalog/data", unchanged.String(), err)
		}
	}
}

func TestReferenceCharacterRestoreNestedParameterDiagnostic(t *testing.T) {
	p := newReferenceScenario(t)
	for _, q := range []string{"CREATE TABLE sentinel(v CHAR(2))", "INSERT INTO sentinel VALUES('a')"} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var before bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
		t.Fatal(err)
	}
	script := "CREATE TABLE staged(v CHAR(2)); INSERT INTO staged VALUES(CAST(CAST($1 AS varchar(3)) AS char(2)))"
	err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
	referenceState(t, err, "42P02")
	referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(strings.Index(script, "$1") + 1)})
	var after bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != before.String() {
		t.Fatal(after.String(), err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"BPCHAR"}, [][]any{{"a "}}))
}
