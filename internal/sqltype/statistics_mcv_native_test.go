package sqltype_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func mcvNativeTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
func mcvNativeInput(t *testing.T, d *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	v, err := d.Input(t.Context(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}
func mcvNativeImage(t *testing.T, v sqltype.Value) []byte {
	t.Helper()
	types := mcvNativeTypes()
	var dictionaries [8][]sqltype.Value
	dictionaries[0] = []sqltype.Value{v}
	result, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{v.Type().Usage()}, dictionaries, []sqltype.StatisticsMCVItem{{Frequency: 1, BaseFrequency: 1}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := result.Type().AppendBinaryWithEnvironment(t.Context(), nil, result, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	// Independent one-dimension framing: 38-byte header, 19-byte item.
	if len(raw) < 57 || binary.LittleEndian.Uint32(raw) != 0xe1a651c2 || binary.LittleEndian.Uint32(raw[4:]) != 1 || binary.LittleEndian.Uint32(raw[8:]) != 1 || binary.LittleEndian.Uint16(raw[12:]) != 1 || binary.LittleEndian.Uint32(raw[14:]) != uint32(v.Type().ID()) {
		t.Fatalf("bad header %x", raw)
	}
	n := int(binary.LittleEndian.Uint32(raw[22:]))
	if n != len(raw)-57 {
		t.Fatalf("bad extent %x", raw)
	}
	f := v.Type().AttributeFacts()
	if int16(binary.LittleEndian.Uint32(raw[30:])) != f.Length || raw[34] != boolByte(f.ByValue) {
		t.Fatalf("bad physical facts %x", raw)
	}
	wantAligned := 0
	if !f.ByValue {
		allocation := n
		if f.Length == -2 {
			allocation -= 4
		}
		wantAligned = (allocation + 7) &^ 7
	}
	if int(binary.LittleEndian.Uint32(raw[26:])) != wantAligned {
		t.Fatalf("bad aligned extent %x", raw)
	}
	at := 38 + n
	if raw[at] != 0 || math.Float64frombits(binary.LittleEndian.Uint64(raw[at+1:])) != 1 || math.Float64frombits(binary.LittleEndian.Uint64(raw[at+9:])) != 1 || binary.LittleEndian.Uint16(raw[at+17:]) != 0 {
		t.Fatalf("bad item %x", raw)
	}
	payload := raw[38:at]
	if f.Length < 0 {
		if len(payload) < 4 || int(binary.LittleEndian.Uint32(payload)) != len(payload)-4 {
			t.Fatal("bad varlena length")
		}
		payload = payload[4:]
	}
	return payload
}
func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}
func mcvNativeWant(t *testing.T, v sqltype.Value, want string) {
	t.Helper()
	expected, err := hex.DecodeString(want)
	if err != nil {
		t.Fatal(err)
	}
	if got := mcvNativeImage(t, v); !bytes.Equal(got, expected) {
		t.Fatalf("native %s: got %x want %x", v.Type().Name(), got, expected)
	}
}

func TestStatisticsMCVNativeEncoding(t *testing.T) {
	types := mcvNativeTypes()
	for _, tc := range []struct {
		id        sqltype.ID
		text, hex string
	}{
		{sqltype.Bool, "true", "01"}, {sqltype.Bool, "false", "00"},
		{sqltype.Char, "", "00"}, {sqltype.Char, "A", "41"},
		{sqltype.Int2, "-2", "feff"}, {sqltype.Int4, "-7", "f9ffffff"}, {sqltype.Int8, "-2", "feffffffffffffff"},
		{sqltype.Oid, "4294967295", "ffffffff"}, {sqltype.Xid, "4294967295", "ffffffff"}, {sqltype.Cid, "17", "11000000"}, {sqltype.Xid8, "18446744073709551615", "ffffffffffffffff"},
		{sqltype.PgLSN, "FEDCBA98/76543210", "1032547698badcfe"},
		{sqltype.Float4, "1.5", "0000c03f"}, {sqltype.Float8, "1.5", "000000000000f83f"},
		{sqltype.Float4, "NaN", "0000c07f"}, {sqltype.Float8, "Infinity", "000000000000f07f"},
		{sqltype.Date, "2000-01-02", "01000000"}, {sqltype.Date, "infinity", "ffffff7f"}, {sqltype.Date, "-infinity", "00000080"},
		{sqltype.Time, "00:00:00.000001", "0100000000000000"},
		{sqltype.Timestamp, "2000-01-01 00:00:00.000001", "0100000000000000"},
		{sqltype.Timestamp, "infinity", "ffffffffffffff7f"}, {sqltype.Timestamp, "-infinity", "0000000000000080"},
		{sqltype.Timestamptz, "2000-01-01 00:00:00+00", "0000000000000000"},
		{sqltype.Timetz, "00:00:00.000001+02", "0100000000000000e0e3ffff"},
		{sqltype.Interval, "1 month 2 days 00:00:00.000003", "03000000000000000200000001000000"},
		{sqltype.Tid, "(65538,3)", "010002000300"}, {sqltype.Macaddr, "01:23:45:67:89:ab", "0123456789ab"},
		{sqltype.UUID, "00112233-4455-6677-8899-aabbccddeeff", "00112233445566778899aabbccddeeff"},
		{sqltype.Text, "NULL", "4e554c4c"}, {sqltype.Text, "", ""}, {sqltype.VarChar, "abc", "616263"},
		{sqltype.BPChar, "a  ", "612020"}, {sqltype.Bytea, `\x00ff`, "00ff"},
		{sqltype.Inet, "192.0.2.7/24", "0218c0000207"}, {sqltype.Cidr, "192.0.2.0/24", "0218c0000200"},
		{sqltype.Inet, "2001:db8::1/64", "034020010db8000000000000000000000001"},
		{sqltype.Numeric, "0", "0080"}, {sqltype.Numeric, "1.2300", "00820100fc08"},
		{sqltype.Numeric, "-0.0001", "7fa20100"},
		{sqltype.Numeric, "NaN", "00c0"}, {sqltype.Numeric, "Infinity", "00d0"}, {sqltype.Numeric, "-Infinity", "00f0"},
	} {
		t.Run(tc.text+"/"+types[tc.id].Name(), func(t *testing.T) { mcvNativeWant(t, mcvNativeInput(t, types[tc.id], tc.text), tc.hex) })
	}
	name := mcvNativeInput(t, types[sqltype.Name], "abc")
	if got := mcvNativeImage(t, name); len(got) != 64 || string(got[:3]) != "abc" || !bytes.Equal(got[3:], make([]byte, 61)) {
		t.Fatalf("name %x", got)
	}
	for _, id := range []sqltype.ID{sqltype.Regclass, sqltype.Regtype, sqltype.Regproc, sqltype.Regprocedure, sqltype.Regrole, sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary} {
		v, err := types[id].BinaryInputWithEnvironment(t.Context(), []byte{0, 0, 0, 42}, -1, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		mcvNativeWant(t, v, "2a000000")
	}
	// Long numeric scale and weight forms are physical, not numeric_send headers.
	mcvNativeWant(t, mcvNativeInput(t, types[sqltype.Numeric], "1."+strings.Repeat("0", 64)), "400000000100")
	mcvNativeWant(t, mcvNativeInput(t, types[sqltype.Numeric], "1e256"), "000040000100")
}

type mcvNativeCancel struct {
	context.Context
	remaining int
}

func (c *mcvNativeCancel) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestStatisticsMCVNativeAuthority(t *testing.T) {
	types := mcvNativeTypes()
	fresh := mcvNativeTypes()
	u := types[sqltype.Int4].Usage()
	var dictionaries [8][]sqltype.Value
	dictionaries[0] = []sqltype.Value{mcvNativeInput(t, u.Type, "1")}
	items := []sqltype.StatisticsMCVItem{{Frequency: 0.5, BaseFrequency: 0.25}}
	v, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{u, u}, [8][]sqltype.Value{dictionaries[0], dictionaries[0]}, items)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := v.Type().AppendBinaryWithEnvironment(t.Context(), nil, v, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	// Independent two-INT4 vector offsets, no datum-reader roundtrip.
	if len(raw) != 92 || raw[70] != 0 || raw[71] != 0 || binary.LittleEndian.Uint32(raw[62:]) != 1 || binary.LittleEndian.Uint32(raw[66:]) != 1 || math.Float64frombits(binary.LittleEndian.Uint64(raw[72:])) != 0.5 || math.Float64frombits(binary.LittleEndian.Uint64(raw[80:])) != 0.25 {
		t.Fatalf("vector %x", raw)
	}
	before := bytes.Clone(raw)
	items[0].Frequency = 1
	dictionaries[0][0] = mcvNativeInput(t, u.Type, "2")
	raw[0] = 0
	after, err := v.Type().AppendBinaryWithEnvironment(t.Context(), nil, v, sqltype.OutputEnvironment{})
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("caller mutation leaked", err)
	}
	retag, err := v.Retag(fresh[sqltype.PgMCVList])
	if err != nil {
		t.Fatal(err)
	}
	same, err := v.SamePayload(t.Context(), retag)
	if err != nil || !same {
		t.Fatal("retag lost backing", err)
	}
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.Text], []sqltype.Usage{u}, dictionaries, items); err == nil {
		t.Fatal("wrong producer type")
	}
	copiedProducer := *types[sqltype.PgMCVList]
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), &copiedProducer, []sqltype.Usage{u}, dictionaries, items); err == nil {
		t.Fatal("copied descriptor acquired producer authority")
	}
	dictionaries[0][0] = mcvNativeInput(t, fresh[sqltype.Int4], "1")
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{u}, dictionaries, items); err == nil {
		t.Fatal("foreign datum generation")
	}
	dictionaries[0][0] = sqltype.Null(u.Type)
	if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{u}, dictionaries, items); err == nil {
		t.Fatal("NULL dictionary")
	}
	dictionaries[0][0] = mcvNativeInput(t, u.Type, "1")
	for _, bad := range []sqltype.StatisticsMCVItem{{Frequency: math.NaN()}, {Frequency: 1, BaseFrequency: math.Inf(1)}, {Frequency: 1, Indexes: [8]uint16{1}}, {Frequency: 1, Nulls: [8]bool{true}, Indexes: [8]uint16{1}}} {
		if _, err := sqltype.StatisticsMCVBuild(t.Context(), types[sqltype.PgMCVList], []sqltype.Usage{u}, dictionaries, []sqltype.StatisticsMCVItem{bad}); err == nil {
			t.Fatal("invalid item accepted", bad)
		}
	}
	for _, limit := range []int{1, 4, 8, 12} {
		ctx := &mcvNativeCancel{Context: context.Background(), remaining: limit}
		if got, err := sqltype.StatisticsMCVBuild(ctx, types[sqltype.PgMCVList], []sqltype.Usage{u}, dictionaries, items); !errors.Is(err, context.Canceled) || got.Type() != nil {
			t.Fatal("cancellation returned prefix", limit, err)
		}
	}
	tree, err := sqltype.StatisticsRetainedTreeValue(types[sqltype.PgNodeTree].Usage(), "{retained}")
	if err != nil {
		t.Fatal(err)
	}
	mcvNativeWant(t, tree, "7b72657461696e65647d")
	for _, text := range []string{"\x00", "\xff"} {
		if _, err := sqltype.StatisticsRetainedTreeValue(types[sqltype.PgNodeTree].Usage(), text); err == nil {
			t.Fatal("invalid retained text")
		}
	}
	if _, err := sqltype.StatisticsRetainedTreeValue(u, "tree"); err == nil {
		t.Fatal("wrong retained usage")
	}
	copiedTree := *types[sqltype.PgNodeTree]
	if _, err := sqltype.StatisticsRetainedTreeValue(copiedTree.Usage(), "tree"); err == nil {
		t.Fatal("copied descriptor acquired retained-tree authority")
	}
	if _, err := types[sqltype.PgNodeTree].Input(t.Context(), "{retained}", -1); err == nil {
		t.Fatal("tree SQL input widened")
	}
}
