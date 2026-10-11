package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT,v TEXT)", 0), fixture.Exec("INSERT INTO s VALUES(1,'x'),(1,'x'),(2,NULL)", 3), fixture.Exec("CREATE TABLE t(v TEXT,i BIGINT)", 0), fixture.Exec("INSERT INTO t SELECT v,i FROM s", 3)} {
		for _, backend := range p.Backends() {
			fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
	}
	expected := fixture.Select("SELECT * FROM t ORDER BY i", []string{"v", "i"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}})
	p.Step(t, expected)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "SELECT") {
		t.Fatal("query provenance in canonical dump")
	}
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name() + "/clone")
	if err != nil {
		t.Fatal(err)
	}
	clone := sql.OpenDB(connector)
	defer clone.Close()
	if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	// Clone is unwrapped: use the shipped recorder interceptor, not a second reader.
	wrapped, cloneRecord := fixture.InsertSelectRecordedPool(t, connector)
	defer wrapped.Close()
	fixture.RunStep(t, p.Ctx, wrapped, cloneRecord, expected)
	for _, script := range []string{"CREATE TABLE t(i INT); INSERT INTO t SELECT 1", "CREATE TABLE t(i INT); INSERT INTO t SELECT $1", "CREATE TABLE t(i INT); INSERT INTO t VALUES(1) ORDER BY 1"} {
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
		p.Step(t, expected)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.Step(t, expected)
}

func TestReferenceInsertSelectRetainedControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE s(i INT,v TEXT)", 0), fixture.Exec("INSERT INTO s VALUES(1,'x'),(1,'x'),(2,NULL)", 3), fixture.Exec("CREATE TABLE t(v TEXT,i BIGINT)", 0), fixture.Exec("INSERT INTO t SELECT v,i FROM s", 3)} {
		p.Step(t, s)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT * FROM t ORDER BY i", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		expected := fixture.Select("SELECT * FROM t ORDER BY i", []string{"v", "i"}, []string{"TEXT", "INT8"}, [][]any{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}})
		for _, script := range []string{"CREATE TABLE t(i INT); INSERT INTO t SELECT 1", "CREATE TABLE t(i INT); INSERT INTO t SELECT $1", "CREATE TABLE t(i INT); INSERT INTO t VALUES(1) ORDER BY 1"} {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
			p.Step(t, expected)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if !reflect.DeepEqual(held.Columns(), []string{"v", "i"}) || held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "INT8" {
			t.Fatal(held.Columns())
		}
		dst := make([]driver.Value, 2)
		for _, want := range [][]driver.Value{{"x", int64(1)}, {"x", int64(1)}, {nil, int64(2)}} {
			if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
				t.Fatal(dst, err, want)
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		return held.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
}
