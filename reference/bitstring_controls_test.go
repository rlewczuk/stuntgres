package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"stuntgres"
)

const bitstringControlCreate = `CREATE TABLE bs_control(id INTEGER,f BIT(3),v BIT VARYING(5),u VARBIT,d BIT,g "bit")`
const bitstringControlInsert = `INSERT INTO bs_control VALUES(1,B'101',B'',X'0f',B'0',B'0000'),(2,'001','B10','xA','1',B'10'),(3,NULL,NULL,NULL,NULL,NULL)`

func bitstringControlRows() [][]any {
	return [][]any{{"101", "", "00001111", "0", "0000"}, {"001", "10", "1010", "1", "10"}, {nil, nil, nil, nil, nil}}
}

func bitstringControlSelect(pg bool) referenceStep {
	s := referenceSelect("SELECT f,v,u,d,g FROM bs_control ORDER BY id", []string{"f", "v", "u", "d", "g"}, []string{"BIT", "VARBIT", "VARBIT", "BIT", "BIT"}, bitstringControlRows())
	s.checkMetadata = bitstringReferenceMetadata(pg, []int64{3, 5, -1, 1, -1}, []bool{true, true, true, true, true})
	return s
}

// Public controls have native-only API phases. Real mode consumes the actual
// emitted native SQL, then independently applies the same row/metadata contract.
func bitstringControlRoundTrip(t *testing.T, p *referencePair) {
	t.Helper()
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(bitstringControlCreate, 0))
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec(bitstringControlInsert, 3))
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var dump bytes.Buffer
	err = conn.Raw(func(raw any) error {
		held, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT f,v,u,d,g FROM bs_control ORDER BY id", nil)
		if err != nil {
			return err
		}
		defer held.Close()
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
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
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("PostgreSQL replay of actual native Dump", err, dump.String())
		}
	}
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			s := bitstringControlSelect(backend.pg)
			s.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
		}
	}
}

func bitstringRetainedReplacement(t *testing.T, p *referencePair, held driver.Rows, dump []byte) error {
	t.Helper()
	settings, err := p.native.Conn(p.ctx)
	if err != nil {
		return err
	}
	defer settings.Close()
	if _, err := settings.ExecContext(p.ctx, "SET standard_conforming_strings=off"); err != nil {
		return err
	}
	defer func() {
		if _, err := settings.ExecContext(p.ctx, "SET standard_conforming_strings=on"); err != nil {
			t.Error(err)
		}
	}()
	var stable bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &stable); err != nil {
		return err
	}
	if !bytes.Equal(dump, stable.Bytes()) {
		t.Fatal("Dump depends on session string setting")
	}
	if _, err := settings.ExecContext(p.ctx, "INSERT INTO bs_control VALUES(4,B'100',B'1',B'',B'1',B'001')"); err != nil {
		return err
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		return err
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump)); err != nil {
		return err
	}
	var setting string
	if err := settings.QueryRowContext(p.ctx, "SHOW standard_conforming_strings").Scan(&setting); err != nil || setting != "off" {
		t.Fatal(setting, err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		return err
	}
	if !bytes.Equal(dump, again.Bytes()) {
		t.Fatal("redump is not stable", again.String())
	}
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bs_control(replacement TEXT);INSERT INTO bs_control VALUES('new')")); err != nil {
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
			referenceAssertRawValue(t, false, families[i], value)
			if !reflect.DeepEqual(value, want[i]) {
				t.Fatal("retained value", i, value, want[i])
			}
		}
	}
	if err := held.Next(values); err != io.EOF {
		t.Fatal("retained EOF", err)
	}
	check()
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT replacement FROM bs_control", []string{"replacement"}, []string{"TEXT"}, [][]any{{"new"}}))
	return stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump))
}

func TestReferenceBitstringControls(t *testing.T) {
	bitstringControlRoundTrip(t, newReferenceScenario(t))
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
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(bitstringControlCreate, 0))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(bitstringControlInsert, 3))
	}
	for _, backend := range p.backends() {
		for _, tc := range []struct{ value, state, message string }{{"B'10'", "22026", "bit string length 2 does not match type bit(3)"}, {"B'2'", "22P02", `"2" is not a valid binary digit`}} {
			for _, prepared := range []bool{false, true} {
				q := "INSERT INTO bs_control(f) VALUES(B'001'),(" + tc.value + ")"
				position := ""
				if tc.state == "22P02" {
					position = referencePosition(utf8.RuneCountInString("INSERT INTO bs_control(f) VALUES(B'001'),(") + 1)
				}
				bitstringReferenceError(t, p, backend, backend.db, q, nil, prepared, false, tc.state, tc.message, position)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, bitstringControlSelect(backend.pg))
			}
		}
		if backend.pg {
			continue
		} // No invented PostgreSQL Restore API.
		preserved := func() { referenceRunStep(t, p.ctx, backend.db, backend.recorder, bitstringControlSelect(false)) }
		prefix := "/*é*/ CREATE TABLE bs_new(v BIT(3));INSERT INTO bs_new VALUES(B'001');INSERT INTO bs_new VALUES("
		for _, tc := range []struct{ value, state, message string }{{"B'2'", "22P02", `"2" is not a valid binary digit`}, {"B'10'", "22026", "bit string length 2 does not match type bit(3)"}} {
			want := controlsReferenceDiagnostic(tc.state, tc.message)
			if tc.state == "22P02" {
				want.Position = referencePosition(utf8.RuneCountInString(prefix) + 1)
			}
			err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(prefix+tc.value+");"))
			referenceAssertDiagnostic(t, err, &want)
			preserved()
		}
		failure := errors.New("bitstring restore reader failure")
		err := stuntgres.Restore(p.ctx, p.native, iotest.ErrReader(failure))
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("reader fabricated SQLSTATE", err)
		}
		preserved()
		canceled, cancel := context.WithCancel(p.ctx)
		err = stuntgres.Restore(canceled, p.native, bitstringCancelReader{strings.NewReader(prefix + "B'001');"), cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("staged cancellation", err)
		}
		preserved()
		precanceled, stop := context.WithCancel(p.ctx)
		stop()
		result, err := backend.db.ExecContext(precanceled, "INSERT INTO bs_control VALUES(9,B'001',B'',B'',B'0',B'1')")
		if !errors.Is(err, context.Canceled) || result != nil {
			t.Fatal(result, err)
		}
		preserved()
	}
}
