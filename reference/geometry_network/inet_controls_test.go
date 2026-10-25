package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

var inetPersistentSetup = []fixture.Step{
	fixture.Exec("CREATE TABLE empty(h INET,c CIDR)", 0),
	fixture.Exec("CREATE TABLE saved(id INTEGER,h INET NOT NULL,c CIDR DEFAULT ('192.168.0.129/25'::inet)::cidr)", 0),
	fixture.Exec("INSERT INTO saved VALUES(1,'192.168.0.1/24','10'),(2,'::1',NULL)", 2),
	fixture.Exec("CREATE VIEW network_view AS SELECT id,h::cidr AS c FROM saved", 0),
}

func inetPersistentAssertions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,h,c FROM saved ORDER BY id", []string{"id", "h", "c"}, []string{"INT4", "INET", "CIDR"}, [][]any{{int64(1), "192.168.0.1/24", "10.0.0.0/8"}, {int64(2), "::1", nil}}),
		fixture.Select("SELECT h,c FROM empty", []string{"h", "c"}, []string{"INET", "CIDR"}, nil),
		fixture.Select("SELECT id,c FROM network_view ORDER BY id", []string{"id", "c"}, []string{"INT4", "CIDR"}, [][]any{{int64(1), "192.168.0.0/24"}, {int64(2), "::1/128"}}),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	s := fixture.Select("SELECT h,c FROM saved ORDER BY id", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{"192.168.0.1/24", "10.0.0.0/8"}, {"::1", nil}})
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		inetMetadata(t, r, b.PG, []string{"h", "c"}, []string{"INET", "CIDR"}, []bool{false, true})
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

type inetFailingReader struct{ err error }

func (r inetFailingReader) Read([]byte) (int, error) { return 0, r.err }

type inetFailingWriter struct{ err error }

func (w inetFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReferenceINETControlsAndRetainedSnapshot(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range inetPersistentSetup {
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT h,c FROM saved ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		if _, err := p.Memory.ExecContext(p.Ctx, "INSERT INTO saved VALUES(3,'1.2.3.4','10')"); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			return err
		}
		if redump.String() != dump.String() {
			t.Fatal("redump changed", redump.String(), dump.String())
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(redump.Bytes())); err != nil {
			return err
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				return err
			}
		}
		metadata := held.(driver.RowsColumnTypeDatabaseTypeName)
		for i, typ := range []string{"INET", "CIDR"} {
			if metadata.ColumnTypeDatabaseTypeName(i) != typ || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[string]() {
				t.Fatal("retained metadata")
			}
		}
		dst := make([]driver.Value, 2)
		for _, want := range [][]driver.Value{{"192.168.0.1/24", "10.0.0.0/8"}, {"::1", nil}} {
			if err := held.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
				t.Fatal(dst, want, err)
			}
		}
		if err := held.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		if metadata.ColumnTypeDatabaseTypeName(0) != "INET" {
			t.Fatal("EOF metadata")
		}
		return held.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := stuntgres.NewDriver().OpenConnector("inet-fresh-destination")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(fresh)
	defer db.Close()
	if err := stuntgres.Restore(p.Ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.InsertSelectRecordedPool(t, fresh)
	inetPersistentAssertions(t, p, fixture.Backend{Name: "fresh", DB: recorded, Recorder: recorder})
	for _, tc := range []struct {
		typ, input string
		host       bool
	}{{"inet", "bad", false}, {"cidr", "192.168.0.1/24", true}} {
		script := "/*é*/ CREATE TABLE replacement(v " + tc.typ + "); INSERT INTO replacement VALUES('10.0.0.0'),('" + tc.input + "')"
		d := inetDiagnostic(tc.typ, tc.input, tc.host)
		d.Position = fixture.Position(utf8.RuneCountInString(script[:strings.Index(script, "'"+tc.input+"'")]) + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		inetPersistentAssertions(t, p, fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord})
	}
	script := "CREATE TABLE replacement(v INET); INSERT INTO replacement VALUES($1)"
	d := fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
	d.Position = fixture.Position(strings.Index(script, "$1") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	failure := errors.New("inet control I/O failure")
	if err := stuntgres.Restore(p.Ctx, p.Native, inetFailingReader{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := stuntgres.Dump(p.Ctx, p.Native, inetFailingWriter{failure}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	inetPersistentAssertions(t, p, fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord})
	for _, b := range p.Backends() {
		inetPersistentAssertions(t, p, b)
	}
	p.Step(t, fixture.Exec("INSERT INTO saved(id,h) VALUES(3,'1.2.3.4')", 1))
	p.Step(t, fixture.Select("SELECT c FROM saved WHERE id=3", []string{"c"}, []string{"CIDR"}, [][]any{{"192.168.0.128/25"}}))
}
