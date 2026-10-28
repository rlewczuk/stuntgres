package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

const bitstringControlCreate = `CREATE TABLE bs_control(id INTEGER,f BIT(3),v BIT VARYING(5),u VARBIT,d BIT,g "bit")`
const bitstringControlInsert = `INSERT INTO bs_control VALUES(1,B'101',B'',X'0f',B'0',B'0000'),(2,'001','B10','xA','1',B'10'),(3,NULL,NULL,NULL,NULL,NULL)`

func bitstringControlRows() [][]any {
	return [][]any{{"101", "", "00001111", "0", "0000"}, {"001", "10", "1010", "1", "10"}, {nil, nil, nil, nil, nil}}
}

func bitstringControlSelect(pg bool) fixture.Step {
	s := fixture.Select("SELECT f,v,u,d,g FROM bs_control ORDER BY id", []string{"f", "v", "u", "d", "g"}, []string{"BIT", "VARBIT", "VARBIT", "BIT", "BIT"}, bitstringControlRows())
	s.CheckMetadata = bitstringReferenceMetadata(pg, []int64{3, 5, -1, 1, -1}, []bool{true, true, true, true, true})
	return s
}

// Public controls have native-only API phases. Real mode consumes the actual
// emitted native SQL, then independently applies the same row/metadata contract.
func bitstringControlRoundTrip(t *testing.T, p *fixture.Pair) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(bitstringControlCreate, 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec(bitstringControlInsert, 3))
	conn, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var dump bytes.Buffer
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT f,v,u,d,g FROM bs_control ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			return err
		}
		for _, piece := range []string{`"f" BIT(3)`, `"v" BIT VARYING(5)`, `"u" BIT VARYING`, `"d" BIT(1)`, `"g" "bit"`, "B''", "B'00001111'", "B'0000'", "NULL"} {
			if !strings.Contains(dump.String(), piece) {
				t.Fatal("missing Dump piece", piece, dump.String())
			}
		}
		return bitstringRetainedReplacement(t, p, held, dump.Bytes())
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL replay of actual native Dump", err, dump.String())
		}
	}
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			s := bitstringControlSelect(backend.PG)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
		}
	}
}

func bitstringRetainedReplacement(t *testing.T, p *fixture.Pair, held driver.Rows, dump []byte) error {
	t.Helper()
	settings, err := p.Native.Conn(p.Ctx)
	if err != nil {
		return err
	}
	defer settings.Close()
	if _, err := settings.ExecContext(p.Ctx, "SET standard_conforming_strings=off"); err != nil {
		return err
	}
	defer func() {
		if _, err := settings.ExecContext(p.Ctx, "SET standard_conforming_strings=on"); err != nil {
			t.Error(err)
		}
	}()
	var stable bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &stable); err != nil {
		return err
	}
	if !bytes.Equal(dump, stable.Bytes()) {
		t.Fatal("Dump depends on session string setting")
	}
	if _, err := settings.ExecContext(p.Ctx, "INSERT INTO bs_control VALUES(4,B'100',B'1',B'',B'1',B'001')"); err != nil {
		return err
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		return err
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		return err
	}
	var setting string
	if err := settings.QueryRowContext(p.Ctx, "SHOW standard_conforming_strings").Scan(&setting); err != nil || setting != "off" {
		t.Fatal(setting, err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		return err
	}
	if !bytes.Equal(dump, again.Bytes()) {
		t.Fatal("redump is not stable", again.String())
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bs_control(replacement TEXT);INSERT INTO bs_control VALUES('new')")); err != nil {
		return err
	}
	if !reflect.DeepEqual(held.Columns(), []string{"f", "v", "u", "d", "g"}) {
		t.Fatal(held.Columns())
	}
	widths := []int64{3, 5, -1, 1, -1}
	families := []string{"BIT", "VARBIT", "VARBIT", "BIT", "BIT"}
	check := func() {
		for i, family := range families {
			if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != family || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != reflect.TypeFor[string]() {
				t.Fatal("retained type", i)
			}
			if n, ok := held.(driver.RowsColumnTypeLength).ColumnTypeLength(i); !ok || n != widths[i] {
				t.Fatal("retained width", i, n, ok)
			}
		}
	}
	check()
	values := make([]driver.Value, 5)
	for _, want := range bitstringControlRows() {
		if err := held.Next(values); err != nil {
			return err
		}
		for i, value := range values {
			fixture.AssertRawValue(t, false, families[i], value)
			if !reflect.DeepEqual(value, want[i]) {
				t.Fatal("retained value", i, value, want[i])
			}
		}
	}
	if err := held.Next(values); err != io.EOF {
		t.Fatal("retained EOF", err)
	}
	check()
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT replacement FROM bs_control", []string{"replacement"}, []string{"TEXT"}, [][]any{{"new"}}))
	return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump))
}

func TestReferenceBitstringControls(t *testing.T) {
	bitstringControlRoundTrip(t, fixture.NewScenario(t))
}

type bitstringCancelReader struct {
	source io.Reader
	cancel context.CancelFunc
}

func (r bitstringCancelReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	r.cancel()
	return n, err
}

func TestReferenceBitstringRestoreFailures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(bitstringControlCreate, 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(bitstringControlInsert, 3))
	}
	for _, backend := range p.Backends() {
		for _, tc := range []struct{ value, state, message string }{{"B'10'", "22026", "bit string length 2 does not match type bit(3)"}, {"B'2'", "22P02", `"2" is not a valid binary digit`}} {
			for _, prepared := range []bool{false, true} {
				q := "INSERT INTO bs_control(f) VALUES(B'001'),(" + tc.value + ")"
				position := ""
				if tc.state == "22P02" {
					position = fixture.Position(utf8.RuneCountInString("INSERT INTO bs_control(f) VALUES(B'001'),(") + 1)
				}
				bitstringReferenceError(t, p, backend, backend.DB, q, nil, prepared, false, tc.state, tc.message, position)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, bitstringControlSelect(backend.PG))
			}
		}
		if backend.PG {
			continue
		} // No invented PostgreSQL Restore API.
		preserved := func() { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, bitstringControlSelect(false)) }
		prefix := "/*é*/ CREATE TABLE bs_new(v BIT(3));INSERT INTO bs_new VALUES(B'001');INSERT INTO bs_new VALUES("
		for _, tc := range []struct{ value, state, message string }{{"B'2'", "22P02", `"2" is not a valid binary digit`}, {"B'10'", "22026", "bit string length 2 does not match type bit(3)"}} {
			want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
			if tc.state == "22P02" {
				want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
			}
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+tc.value+");"))
			fixture.AssertDiagnostic(t, err, &want)
			preserved()
		}
		failure := errors.New("bitstring restore reader failure")
		err := stuntgres.Restore(p.Ctx, p.Native, iotest.ErrReader(failure))
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("reader fabricated SQLSTATE", err)
		}
		preserved()
		canceled, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(canceled, p.Native, bitstringCancelReader{strings.NewReader(prefix + "B'001');"), cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("staged cancellation", err)
		}
		preserved()
		precanceled, stop := context.WithCancel(p.Ctx)
		stop()
		result, err := backend.DB.ExecContext(precanceled, "INSERT INTO bs_control VALUES(9,B'001',B'',B'',B'0',B'1')")
		if !errors.Is(err, context.Canceled) || result != nil {
			t.Fatal(result, err)
		}
		preserved()
	}
}
