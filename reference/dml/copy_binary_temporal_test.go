package reference_test

import "testing"

func TestReferenceCopyBinaryCodecsTemporal(t *testing.T) {
	h := copyBinaryHex
	cases := []copyBinaryProviderCase{
		{name: "date_epoch", typ: "date", payload: copyBinary32(0), valid: true, wire: copyBinary32(0)},
		{name: "date_before_epoch", typ: "date", payload: copyBinary32(-1), valid: true, wire: copyBinary32(-1)},
		{name: "date_infinity", typ: "date", payload: h("7fffffff"), valid: true, wire: h("7fffffff")},
		{name: "date_negative_infinity", typ: "date", payload: h("80000000"), valid: true, wire: h("80000000")},
		{name: "date_out_of_range", typ: "date", payload: h("7ffffffe")},
		{name: "time_midnight", typ: "time", payload: copyBinary64(0), valid: true, wire: copyBinary64(0)},
		{name: "time_24", typ: "time", payload: copyBinary64(86400000000), valid: true, wire: copyBinary64(86400000000)},
		{name: "time_negative", typ: "time", payload: copyBinary64(-1)},
		{name: "time_over_24", typ: "time", payload: copyBinary64(86400000001)},
		{name: "time_precision", typ: "time(3)", payload: copyBinary64(1234567)},
		// TIMETZ seconds west: 01:00 at +02 is micros=3600000000, zone=-7200.
		{name: "timetz", typ: "timetz", payload: append(copyBinary64(3600000000), copyBinary32(-7200)...), valid: true, wire: append(copyBinary64(3600000000), copyBinary32(-7200)...)},
		{name: "timetz_bad_zone", typ: "timetz", payload: append(copyBinary64(0), copyBinary32(86400)...)},
		{name: "timestamp_epoch", typ: "timestamp", payload: copyBinary64(0), valid: true, wire: copyBinary64(0)},
		{name: "timestamptz_epoch", typ: "timestamptz", payload: copyBinary64(0), valid: true, wire: copyBinary64(0)},
		{name: "timestamp_before_epoch", typ: "timestamp", payload: copyBinary64(-1), valid: true, wire: copyBinary64(-1)},
		{name: "timestamp_precision", typ: "timestamp(3)", payload: copyBinary64(1234567)},
		{name: "timestamptz_precision", typ: "timestamptz(3)", payload: copyBinary64(1234567)},
		{name: "timestamp_infinity", typ: "timestamp", payload: h("7fffffffffffffff"), valid: true, wire: h("7fffffffffffffff")},
		{name: "timestamptz_negative_infinity", typ: "timestamptz", payload: h("8000000000000000"), valid: true, wire: h("8000000000000000")},
		{name: "timestamp_out_of_range", typ: "timestamp", payload: h("7ffffffffffffffe")},
		{name: "interval", typ: "interval", payload: h("00000000000000010000000200000003"), valid: true, wire: h("00000000000000010000000200000003")},
		{name: "interval_precision", typ: "interval(3)", payload: append(append(copyBinary64(1234567), copyBinary32(2)...), copyBinary32(3)...)},
	}
	for _, spec := range []struct {
		typ   string
		width int
	}{{"date", 4}, {"time", 8}, {"timetz", 12}, {"timestamp", 8}, {"timestamptz", 8}, {"interval", 16}} {
		cases = append(cases, copyBinaryProviderCase{name: spec.typ + "_short", typ: spec.typ, payload: make([]byte, spec.width-1)}, copyBinaryProviderCase{name: spec.typ + "_trailing", typ: spec.typ, payload: make([]byte, spec.width+1)})
	}
	copyBinaryProviders(t, cases, copyBinaryTemporalExpected)
}
