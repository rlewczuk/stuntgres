package enginetests

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func txidSnapshotTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func TestTxidSnapshotInputOutput(t *testing.T) {
	types := txidSnapshotTypes()
	d := types[sqltype.TxidSnapshot]
	ctx := context.Background()
	if d.ID() != 2970 || !d.Concrete() || d.Name() != "txid_snapshot" || d.DatabaseName() != "TXID_SNAPSHOT" {
		t.Fatal("nominal descriptor", d)
	}
	for _, tc := range []struct{ input, want string }{
		{"12:13:", "12:13:"}, {"12:12:", "12:12:"}, {"12:16:14,14", "12:16:14"},
		{"12:16:14,", "12:16:14"}, {"010:020:014", "10:20:14"},
		{"+12:+16:+14", "12:16:14"}, {" 12: 16: 14", "12:16:14"},
		{"1:18446744073709551616:", "1:18446744073709551615:"},
		{"1:-1:", "1:18446744073709551615:"}, {"-18446744073709551615:2:", "1:2:"},
		{"1:-18446744073709551616:", "1:18446744073709551615:"},
		{"4294967295:4294967297:4294967296", "4294967295:4294967297:4294967296"},
		{"9223372036854775807:9223372036854775809:9223372036854775808", "9223372036854775807:9223372036854775809:9223372036854775808"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc.want {
			t.Fatal(out, err)
		}
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "'"+tc.want+"'::txid_snapshot" {
			t.Fatal(literal, err)
		}
	}
	for _, input := range []string{"", "bad", "31:12:", "0:1:", "1:0:", "12:13:13", "12:16:14,13", "12:16:14x", "12:16:14 ", "12:16:14,,", "1:4294967296:3", "4294967296:4294967297:", "1:9223372036854775808:3"} {
		_, err := d.Input(ctx, input, -1)
		var diagnostic *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type pg_snapshot: \"" + input + "\""}
		if !errors.As(err, &diagnostic) || *diagnostic != want {
			t.Fatal(input, err)
		}
	}
	if literal, err := d.SQLLiteral(ctx, sqltype.Null(d)); err != nil || literal != "NULL::txid_snapshot" {
		t.Fatal(literal, err)
	}
	if d.CheckBinaryInput() == nil || d.CheckBinaryOutput() == nil {
		t.Fatal("accidental binary admission")
	}
	if types[2949].Concrete() {
		t.Fatal("accidental array admission")
	}
	if _, err := sqltype.Array(90001, "legacy_array", d); err == nil {
		t.Fatal("constructed snapshot array")
	}
	for _, other := range []*sqltype.Descriptor{types[sqltype.Int8], types[sqltype.PgSnapshot]} {
		for _, mode := range []sqltype.CoercionContext{sqltype.Implicit, sqltype.Explicit} {
			if _, err := sqltype.PlanCoercion(other.Usage(), d.Usage(), mode); err == nil {
				t.Fatal("foreign input coercion", other)
			}
		}
	}
}

func TestTxidSnapshotOwnership(t *testing.T) {
	types := txidSnapshotTypes()
	members := []uint64{4294967296}
	s, err := sqltype.NewSnapshot(4294967295, 4294967297, members)
	if err != nil {
		t.Fatal(err)
	}
	members[0] = 4294967295
	if s.Member(0) != 4294967296 || s.Visible(4294967296) || !s.Visible(4294967295) {
		t.Fatal("borrowed mutable members")
	}
	v, err := sqltype.SnapshotValue(types[sqltype.TxidSnapshot], s)
	if err != nil {
		t.Fatal(err)
	}
	fresh := txidSnapshotTypes()[sqltype.TxidSnapshot]
	retagged, err := v.Retag(fresh)
	if err != nil || retagged.Type() != fresh {
		t.Fatal("descriptor remint", retagged, err)
	}
	if payload, err := retagged.Snapshot(); err != nil || payload != s {
		t.Fatal("remint copied payload", err)
	}
	if same, err := v.SamePayload(context.Background(), retagged); err != nil || !same {
		t.Fatal("remint changed immutable bits", same, err)
	}
	for _, value := range []sqltype.Value{v, sqltype.Null(types[sqltype.TxidSnapshot])} {
		if _, err := value.Retag(types[sqltype.PgSnapshot]); err == nil {
			t.Fatal("cross-nominal retag")
		}
	}
	modern, err := sqltype.SnapshotValue(types[sqltype.PgSnapshot], s)
	if err != nil {
		t.Fatal(err)
	}
	if sqltype.SameDatum(v, modern) {
		t.Fatal("cross-nominal datum equality")
	}
	if _, err := sqltype.Null(types[sqltype.TxidSnapshot]).Snapshot(); err == nil {
		t.Fatal("NULL payload exposed")
	}
	for _, bounds := range [][2]uint64{{0, 1}, {1, 0}, {1, 4294967296}, {4294967296, 4294967297}, {1, 9223372036854775808}} {
		if _, err := sqltype.NewSnapshot(bounds[0], bounds[1], nil); err == nil {
			t.Fatal("invalid low32 bound", bounds)
		}
	}
}
