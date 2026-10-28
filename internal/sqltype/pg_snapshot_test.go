package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPGSnapshotInputOutput(t *testing.T) {
	d := testTypes()[PgSnapshot]
	ctx := context.Background()
	for _, tc := range []struct{ input, want string }{
		{"10:10:", "10:10:"}, {"12:16:14,14", "12:16:14"}, {"12:16:14,", "12:16:14"},
		{"010:020:014", "10:20:14"}, {"+12:+16:+14", "12:16:14"}, {" 12: 16: 14", "12:16:14"},
		{"1:18446744073709551616:", "1:18446744073709551615:"}, {"1:-1:", "1:18446744073709551615:"},
		{"-18446744073709551615:2:", "1:2:"}, {"1:-18446744073709551616:", "1:18446744073709551615:"},
		{"4294967295:4294967297:4294967296", "4294967295:4294967297:4294967296"},
		{"1:9223372036854775809:3", "1:9223372036854775809:3"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		got, err := d.Output(ctx, v)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %q %v", tc.input, got, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+tc.want+"'::pg_snapshot" {
			t.Fatal(literal, err)
		}
	}
	for _, input := range []string{"", "garbage", "31:12:", "0:1:", "1:0:", "12:13:0", "12:13:13", "12:16:14,13", "12", "12:16", "12:16::", "12:16:14x", "12 :16:", "12:16:14 ", "12:16:14,,", "1:4294967296:3", "4294967296:4294967297:", "1:9223372036854775808:3"} {
		_, err := d.Input(ctx, input, -1)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type pg_snapshot: \"" + input + "\""}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(input, err)
		}
	}
	if _, err := PlanCoercion(testTypes()[Int4].Usage(), d.Usage(), Explicit); err == nil {
		t.Fatal("integer cast admitted")
	}
	if testTypes()[5039].Concrete() {
		t.Fatal("snapshot array admitted")
	}
	if d.CheckBinaryInput() == nil || d.CheckBinaryOutput() == nil {
		t.Fatal("snapshot advertised binary codec")
	}
}
func TestPGSnapshotVisibility(t *testing.T) {
	var members []uint64
	for id := uint64(100); id < 170; id += 2 {
		members = append(members, id)
	}
	s, err := NewSnapshot(100, 170, members)
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(0); id <= 171; id++ {
		want := id < 100 || id < 170 && id%2 == 1
		if s.Visible(id) != want {
			t.Fatal(id, want)
		}
	}
	if s.Visible(^uint64(0)) {
		t.Fatal("unsigned maximum visible")
	}
	equal, err := NewSnapshot(10, 10, nil)
	if err != nil || !equal.Visible(9) || equal.Visible(10) {
		t.Fatal(equal, err)
	}
}
func TestPGSnapshotOwnership(t *testing.T) {
	types := testTypes()
	members := []uint64{10, 14}
	s, err := NewSnapshot(10, 20, members)
	if err != nil {
		t.Fatal(err)
	}
	members[0] = 11
	if s.Member(0) != 10 || s.Visible(10) || !s.Visible(11) {
		t.Fatal("mutable input retained")
	}
	value, err := SnapshotValue(types[PgSnapshot], s)
	if err != nil {
		t.Fatal(err)
	}
	fresh := testTypes()[PgSnapshot]
	reminted, err := value.Retag(fresh)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := reminted.Snapshot()
	if err != nil || payload != s {
		t.Fatal("payload copied on remint", err)
	}
	if _, err := SnapshotValue(types[Text], s); err == nil {
		t.Fatal("foreign descriptor")
	}
	if _, err := SnapshotValue(types[PgSnapshot], new(Snapshot)); err == nil {
		t.Fatal("zero snapshot accepted")
	}
	if _, err := Null(types[PgSnapshot]).Snapshot(); err == nil {
		t.Fatal("NULL payload exposed")
	}
	for _, tc := range []struct {
		lo, hi  uint64
		members []uint64
	}{{0, 20, nil}, {20, 10, nil}, {10, 20, []uint64{9}}, {10, 20, []uint64{20}}, {10, 20, []uint64{14, 10}}, {10, 20, []uint64{10, 10}}} {
		if _, err := NewSnapshot(tc.lo, tc.hi, tc.members); err == nil {
			t.Fatal(tc)
		}
	}
}
