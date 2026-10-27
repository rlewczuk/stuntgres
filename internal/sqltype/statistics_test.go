package sqltype

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestStatisticsTypeDescriptors(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, id := range []ID{PgNDistinct, PgDependencies, PgMCVList} {
		d := types[id]
		if d == nil || !d.Concrete() || d == fresh[id] {
			t.Fatal("missing distinct concrete generation", id)
		}
		f, ok := d.PGTypeMetadata()
		if !ok || f.Kind != "b" || f.Category != "Z" || f.Length != -1 || f.ByValue || f.Alignment != "i" || f.Storage != "x" || f.Collation != 100 || d.AssociatedArray() != 0 {
			t.Fatal("statistics type facts", id, f)
		}
		for _, text := range []string{"", "x"} {
			_, err := d.Input(t.Context(), text, -1)
			var e *sqlstate.Error
			if !errors.As(err, &e) || e.Code != "0A000" || e.Message != "cannot accept a value of type "+d.Name() {
				t.Fatal("text input", id, text, err)
			}
		}
		for _, data := range [][]byte{nil, {0}, {1, 2, 3}} {
			_, err := d.BinaryInputWithEnvironment(t.Context(), data, -1, InputEnvironment{})
			var e *sqlstate.Error
			if !errors.As(err, &e) || e.Code != "0A000" || e.Message != "cannot accept a value of type "+d.Name() {
				t.Fatal("binary receive", id, err)
			}
		}
		if v, err := Null(d).Retag(fresh[id]); err != nil || !v.IsNull() || v.Type() != fresh[id] {
			t.Fatal("typed NULL retag", err)
		}
		if equal, order := ComparisonCapabilities(d); equal || order {
			t.Fatal("input rejection type gained comparison", id)
		}
	}
}

func TestStatisticsTypeOutput(t *testing.T) {
	types, fresh := testTypes(), testTypes()
	for _, tc := range []struct {
		id         ID
		value      float64
		want, wire string
	}{
		{PgNDistinct, 3, `{"1, 2": 3}`, "a4bf52a3010000000100000000000000000008400200000001000200"},
		{PgDependencies, 1, `{"1 => 2": 1.000000}`, "2c9a54b40100000001000000000000000000f03f020001000200"},
		{PgDependencies, 1, `{"1 => -1": 1.000000}`, "2c9a54b40100000001000000000000000000f03f02000100ffff"},
	} {
		attrs := []int16{1, 2}
		if tc.want == `{"1 => -1": 1.000000}` {
			attrs[1] = -1
		}
		v, err := StatisticsValue(types[tc.id], []StatisticsItem{{attrs, tc.value}})
		if err != nil {
			t.Fatal(err)
		}
		equivalent, err := StatisticsValue(types[tc.id], []StatisticsItem{{append([]int16(nil), attrs...), tc.value}})
		if err != nil || equivalent.statistics == v.statistics || !SameDatum(v, equivalent) {
			t.Fatal("independent equivalent statistics datum identity", err)
		}
		distinct, err := StatisticsValue(types[tc.id], []StatisticsItem{{[]int16{1, 3}, tc.value}})
		if err != nil || SameDatum(v, distinct) || SameDatum(v, Value{typ: types[tc.id]}) {
			t.Fatal("distinct or invalid statistics datum identity", err)
		}
		attrs[0] = 7
		got, err := v.Type().Output(t.Context(), v)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
		retag, err := v.Retag(fresh[tc.id])
		if err != nil || retag.statistics != v.statistics {
			t.Fatal("immutable backing copied", err)
		}
		same, err := v.SamePayload(t.Context(), retag)
		if err != nil || !same {
			t.Fatal("payload remap", err)
		}
		if _, err = v.Retag(types[Text]); err == nil {
			t.Fatal("foreign type retag accepted")
		}
		b, err := v.Type().AppendBinaryWithEnvironment(t.Context(), nil, v, OutputEnvironment{})
		expected, decodeErr := hex.DecodeString(tc.wire)
		if decodeErr != nil || err != nil || !bytes.Equal(b, expected) {
			t.Fatal("released send vector", b, expected, err, decodeErr)
		}
		bytea, err := v.StatisticsSend(t.Context(), types[Bytea])
		if err != nil {
			t.Fatal(err)
		}
		raw, err := bytea.Binary()
		if err != nil || raw != string(expected) {
			t.Fatal("I/O function send payload", raw, err)
		}
		b[0] = 0
		if v.statistics.binary[0] == 0 {
			t.Fatal("send leaked immutable bytes")
		}
	}
	v, err := StatisticsValue(types[PgDependencies], []StatisticsItem{{[]int16{1, 3, 2}, 0.25}, {[]int16{2, 1}, 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Type().Output(t.Context(), v); err != nil || got != `{"1, 3 => 2": 0.250000, "2 => 1": 0.500000}` {
		t.Fatal(got, err)
	}
	for _, item := range []StatisticsItem{{[]int16{1, 1}, 1}, {[]int16{0, 2}, 1}, {[]int16{-9, 2}, 1}, {[]int16{1}, 1}, {[]int16{1, 2}, math.NaN()}, {[]int16{1, 2}, math.Inf(1)}, {[]int16{1, 2}, 1.1}} {
		if _, err := StatisticsValue(types[PgDependencies], []StatisticsItem{item}); err == nil {
			t.Fatal("invalid private item accepted", item)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.Type().Output(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal("output cancellation", err)
	}
	if _, err := v.Type().AppendBinaryWithEnvironment(ctx, nil, v, OutputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("send cancellation", err)
	}
}

func statisticsMCVVector() []byte {
	// Independent released linux/amd64 vector: two INT4 dimensions, one value
	// each and one frequency-1 item. Offsets deliberately don't use the codec.
	b := make([]byte, 92)
	binary.LittleEndian.PutUint32(b, 0xE1A651C2)
	binary.LittleEndian.PutUint32(b[4:], 1)
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint16(b[12:], 2)
	for _, at := range []int{14, 18} {
		binary.LittleEndian.PutUint32(b[at:], 23)
	}
	for _, at := range []int{22, 42} {
		for i, n := range []uint32{1, 4, 0, 4} {
			binary.LittleEndian.PutUint32(b[at+4*i:], n)
		}
		b[at+16] = 1
	}
	binary.LittleEndian.PutUint32(b[62:], 1)
	binary.LittleEndian.PutUint32(b[66:], 1)
	binary.LittleEndian.PutUint64(b[72:], math.Float64bits(1))
	binary.LittleEndian.PutUint64(b[80:], math.Float64bits(1))
	return b
}
func TestStatisticsTypeMCV(t *testing.T) {
	d := testTypes()[PgMCVList]
	b := statisticsMCVVector()
	frozen := bytes.Clone(b)
	v, err := StatisticsMCVValue(d, b)
	if err != nil {
		t.Fatal(err)
	}
	b[0] = 0
	want := `\x` + hex.EncodeToString(frozen)
	if got, err := d.Output(t.Context(), v); err != nil || got != want {
		t.Fatal(got, want, err)
	}
	escape, err := byteaOutput(string(frozen), ByteaEscape)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := d.OutputWithEnvironment(t.Context(), v, OutputEnvironment{ByteaOutput: ByteaEscape}); err != nil || got != escape {
		t.Fatal("bytea escape", got, err)
	}
	if got, err := d.AppendBinaryWithEnvironment(t.Context(), nil, v, OutputEnvironment{}); err != nil || !bytes.Equal(got, frozen) {
		t.Fatal("MCV send", got, err)
	}
	for _, at := range []int{30, 50} {
		bad := bytes.Clone(frozen)
		binary.LittleEndian.PutUint32(bad[at:], 8)
		if _, err := StatisticsMCVValue(d, bad); err == nil {
			t.Fatal("by-value dimensions accepted by-reference allocation", at)
		}
	}
	for n := range len(frozen) {
		if _, err := StatisticsMCVValue(d, frozen[:n]); err == nil {
			t.Fatal("truncated MCV accepted", n)
		}
	}
	for _, at := range []int{0, 4, 8, 12, 22, 26, 30, 34, 38, 70, 88} {
		bad := bytes.Clone(frozen)
		bad[at] = 255
		if _, err := StatisticsMCVValue(d, bad); err == nil {
			t.Fatal("malformed MCV accepted", at)
		}
	}
}
