package reference_test

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func mcvWord(n uint64, width int) []byte {
	v := make([]byte, 8)
	binary.LittleEndian.PutUint64(v, n)
	return v[:width]
}
func TestReferenceStatisticsMCVTypes(t *testing.T) {
	cases := []struct {
		name, typ, literal string
		expected           []byte
	}{
		{"bool", "boolean", "true", []byte{1}},
		{"char", `"char"`, "'Z'", []byte{'Z'}},
		{"int2", "int2", "-7", mcvWord(65529, 2)},
		{"int4", "int4", "-7", mcvWord(4294967289, 4)},
		{"regclass", "regclass", "'123456'", mcvWord(123456, 4)},
		{"regtype", "regtype", "'23'", mcvWord(23, 4)},
		{"regproc", "regproc", "'123456'", mcvWord(123456, 4)},
		{"regprocedure", "regprocedure", "'123456'", mcvWord(123456, 4)},
		{"regrole", "regrole", "'123456'", mcvWord(123456, 4)},
		{"regoper", "regoper", "'123456'", mcvWord(123456, 4)},
		{"regoperator", "regoperator", "'123456'", mcvWord(123456, 4)},
		{"regcollation", "regcollation", "'123456'", mcvWord(123456, 4)},
		{"regnamespace", "regnamespace", "'123456'", mcvWord(123456, 4)},
		{"regconfig", "regconfig", "'123456'", mcvWord(123456, 4)},
		{"regdictionary", "regdictionary", "'123456'", mcvWord(123456, 4)},
		{"int8", "int8", "1234567890123", mcvWord(1234567890123, 8)},
		{"oid", "oid", "4294967294", mcvWord(4294967294, 4)},
		{"xid8", "xid8", "'18446744073709551614'", mcvWord(math.MaxUint64-1, 8)},
		{"lsn", "pg_lsn", "'FEDCBA98/76543210'", mcvWord(0xfedcba9876543210, 8)},
		{"float4", "float4", "1.5", mcvWord(uint64(math.Float32bits(1.5)), 4)},
		{"float8", "float8", "-2.25", mcvWord(math.Float64bits(-2.25), 8)},
		{"date", "date", "'2000-01-02'", mcvWord(1, 4)},
		{"dateinf", "date", "'infinity'", mcvWord(math.MaxInt32, 4)},
		{"time", "time", "'00:00:01'", mcvWord(1000000, 8)},
		{"timestamp", "timestamp", "'2000-01-01 00:00:01'", mcvWord(1000000, 8)},
		{"timestamptz", "timestamptz", "'2000-01-01 00:00:01+00'", mcvWord(1000000, 8)},
		{"timestampinf", "timestamp", "'-infinity'", mcvWord(1<<63, 8)},
		{"timetz", "timetz", "'00:00:01+02'", append(mcvWord(1000000, 8), mcvWord(4294960096, 4)...)},
		{"interval", "interval", "'2 months 3 days 4 seconds'", append(append(mcvWord(4000000, 8), mcvWord(3, 4)...), mcvWord(2, 4)...)},
		{"uuid", "uuid", "'00112233-4455-6677-8899-aabbccddeeff'", []byte{0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255}},
		{"mac", "macaddr", "'01:23:45:67:89:ab'", []byte{1, 35, 69, 103, 137, 171}},
		{"tid", "tid", "'(65538,7)'", []byte{1, 0, 2, 0, 7, 0}},
		{"text", "text", "'NULL'", []byte("NULL")},
		{"empty", "text", "''", []byte{}},
		{"varchar", "varchar(9)", "'hello'", []byte("hello")},
		{"bpchar", "char(3)", "'x'", []byte("x  ")},
		{"bytea", "bytea", `'\x0001ff'`, []byte{0, 1, 255}},
		{"inet4", "inet", "'192.0.2.9/24'", []byte{2, 24, 192, 0, 2, 9}},
		{"cidr4", "cidr", "'192.0.2.0/24'", []byte{2, 24, 192, 0, 2, 0}},
		{"inet6", "inet", "'2001:db8::1/64'", []byte{3, 64, 32, 1, 13, 184, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
				s.run(fixture.Exec("CREATE TABLE mcv_type(a "+c.typ+", b INT)", 0))
				s.run(fixture.Exec("CREATE STATISTICS mcv_type_s(mcv) ON a,b FROM mcv_type", 0))
				s.run(fixture.Exec("INSERT INTO mcv_type VALUES("+c.literal+",1),("+c.literal+",1)", 2))
				s.run(fixture.Exec("ANALYZE mcv_type", 0))
				_, v := readMCV(t, s, "mcv_type_s", false)
				if len(v.items) != 1 || v.items[0].frequency != 1 || v.items[0].baseline != 1 || v.items[0].nulls[0] {
					t.Fatal(v.items)
				}
				if hex.EncodeToString(v.items[0].values[0]) != hex.EncodeToString(c.expected) {
					t.Fatalf("%s native payload %x want %x", c.name, v.items[0].values[0], c.expected)
				}
				var oid int64
				if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT atttypid::bigint FROM pg_attribute WHERE attrelid='mcv_type'::regclass AND attname='a'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				if v.dimensions[0].oid != uint32(oid) {
					t.Fatal("logical type identity", v.dimensions[0].oid, oid)
				}
			})
		})
	}
	t.Run("name", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_type(a NAME,b INT)", 0))
			s.run(fixture.Exec("INSERT INTO mcv_type VALUES('abc',1)", 1))
			s.run(fixture.Exec("CREATE STATISTICS mcv_type_s(mcv) ON a,b FROM mcv_type", 0))
			s.run(fixture.Exec("ANALYZE mcv_type", 0))
			_, v := readMCV(t, s, "mcv_type_s", false)
			p := v.items[0].values[0]
			if len(p) != 64 || string(p[:3]) != "abc" {
				t.Fatal(p)
			}
			for _, b := range p[3:] {
				if b != 0 {
					t.Fatal("name padding")
				}
			}
		})
	})
	t.Run("null-text", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			s.run(fixture.Exec("CREATE TABLE mcv_type(a TEXT,b INT)", 0))
			s.run(fixture.Exec("INSERT INTO mcv_type VALUES('NULL',1),('',1),(NULL,1)", 3))
			s.run(fixture.Exec("CREATE STATISTICS mcv_type_s(mcv) ON a,b FROM mcv_type", 0))
			s.run(fixture.Exec("ANALYZE mcv_type", 0))
			_, v := readMCV(t, s, "mcv_type_s", false)
			seen := map[string]bool{}
			for _, item := range v.items {
				key := string(item.values[0])
				if item.nulls[0] {
					key = "<SQL NULL>"
				}
				seen[key] = true
				if math.Abs(item.frequency-1.0/3) > 1e-14 || math.Abs(item.baseline-1.0/3) > 1e-14 {
					t.Fatal(item)
				}
			}
			if len(seen) != 3 || !seen["NULL"] || !seen[""] || !seen["<SQL NULL>"] {
				t.Fatal(seen)
			}
		})
	})
}
