//go:build !noservertests

package reference_test

import (
	"encoding/hex"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

type serverCodecCase struct {
	typ, text, wire string
	oid             uint32
	length          int16
}

// Wire bytes are independent network-format goldens, not output captured from either backend.
var serverCodecCases = []serverCodecCase{
	{"boolean", "t", "01", 16, 1}, {"smallint", "42", "002a", 21, 2}, {"integer", "42", "0000002a", 23, 4}, {"bigint", "42", "000000000000002a", 20, 8},
	{"real", "1.5", "3fc00000", 700, 4}, {"double precision", "1.5", "3ff8000000000000", 701, 8}, {"numeric", "12.50", "0002000000000002000c1388", 1700, -1},
	{"money", "$1.00", "0000000000000064", 790, 8}, {"text", "é", "c3a9", 25, -1}, {"character varying", "abc", "616263", 1043, -1}, {"character", "a", "61", 1042, -1}, {"name", "abc", "616263", 19, 64}, {"\"char\"", "a", "61", 18, 1}, {"bytea", "\\x00ff", "00ff", 17, -1},
	{"oid", "42", "0000002a", 26, 4}, {"xid", "42", "0000002a", 28, 4}, {"cid", "42", "0000002a", 29, 4}, {"xid8", "42", "000000000000002a", 5069, 8},
	{"regclass", "pg_class", "000004eb", 2205, 4}, {"regtype", "integer", "00000017", 2206, 4}, {"regproc", "-", "00000000", 24, 4},
	{"regprocedure", "abs(integer)", "00000575", 2202, 4}, {"regoper", "0", "00000000", 2203, 4}, {"regoperator", "0", "00000000", 2204, 4}, {"regnamespace", "pg_catalog", "0000000b", 4089, 4}, {"regrole", "-", "00000000", 4096, 4}, {"regcollation", "\"C\"", "000003b6", 4191, 4}, {"regconfig", "-", "00000000", 3734, 4}, {"regdictionary", "-", "00000000", 3769, 4},
	{"uuid", "00112233-4455-6677-8899-aabbccddeeff", "00112233445566778899aabbccddeeff", 2950, 16},
	{"date", "2000-01-01", "00000000", 1082, 4}, {"time without time zone", "00:00:00", "0000000000000000", 1083, 8}, {"time with time zone", "00:00:00+00", "000000000000000000000000", 1266, 12},
	{"timestamp without time zone", "2000-01-01 00:00:00", "0000000000000000", 1114, 8}, {"timestamp with time zone", "2000-01-01 00:00:00+00", "0000000000000000", 1184, 8}, {"interval", "00:00:01", "00000000000f42400000000000000000", 1186, 16},
	{"bit(3)", "101", "00000003a0", 1560, -1}, {"bit varying", "101", "00000003a0", 1562, -1}, {"json", "{\"a\":1}", "7b2261223a317d", 114, -1}, {"jsonb", "{\"a\": 1}", "017b2261223a20317d", 3802, -1}, {"jsonpath", "$", "0124", 4072, -1},
	{"inet", "127.0.0.1", "022000047f000001", 869, -1}, {"cidr", "127.0.0.0/8", "020801047f000000", 650, -1},
	{"integer[]", "{1,NULL,2}", "00000001000000010000001700000003000000010000000400000001ffffffff0000000400000002", 1007, -1},
	{"text[]", "{a,\"\"}", "0000000100000000000000190000000200000001000000016100000000", 1009, -1},
	{"int4range", "[1,3)", "0200000004000000010000000400000003", 3904, -1}, {"int8range", "[1,3)", "02000000080000000000000001000000080000000000000003", 3926, -1},
	{"numrange", "empty", "01", 3906, -1}, {"tsrange", "empty", "01", 3908, -1}, {"tstzrange", "empty", "01", 3910, -1}, {"daterange", "empty", "01", 3912, -1},
	{"int4multirange", "{}", "00000000", 4451, -1}, {"int8multirange", "{}", "00000000", 4536, -1}, {"nummultirange", "{}", "00000000", 4532, -1},
}

// Associated concrete array identities are explicit, not discovered from native output.
var serverArrayOIDs = map[uint32]uint32{16: 1000, 17: 1001, 19: 1003, 20: 1016, 21: 1005, 23: 1007, 25: 1009, 26: 1028, 114: 199, 3802: 3807, 4072: 4073, 1042: 1014, 1043: 1015, 700: 1021, 701: 1022, 790: 791, 1082: 1182, 1083: 1183, 1114: 1115, 1184: 1185, 1186: 1187, 1266: 1270, 1700: 1231, 2950: 2951, 2205: 2210, 2206: 2211, 24: 1008, 4096: 4097, 2203: 2208, 2204: 2209, 2202: 2207, 4191: 4192, 4089: 4090, 3734: 3735, 3769: 3770}

func TestReferenceServerExtendedCodecs(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			serverCodecBoundaries(t, f, b)
			for _, c := range serverCodecCases {
				t.Run(c.typ, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					raw, err := hex.DecodeString(c.wire)
					if err != nil {
						t.Fatal(err)
					}
					for _, format := range []int{0, 1} {
						input := []byte(c.text)
						if format == 1 {
							input = raw
						}
						frames := serverCycle(t, w, serverParse("", "SELECT $1 AS v", c.oid), serverBind("", "", []int{format}, [][]byte{input}, format), serverDescribe('P', ""), serverExecute("", 0))
						want := []byte(c.text)
						if format == 1 {
							want = raw
						}
						serverWantTags(t, frames, "12TDCZ")
						serverWantRaw(t, frames, [][][]byte{{want}})
						columns := serverColumns(t, frames)
						if len(columns) != 1 || columns[0].oid != c.oid || columns[0].length != c.length || columns[0].format != int16(format) {
							t.Fatalf("metadata %+v", columns)
						}
					}
					frames := serverCycle(t, w, serverParse("", "SELECT $1", c.oid), serverBind("", "", nil, [][]byte{nil}, 1), serverExecute("", 0))
					serverWantRaw(t, frames, [][][]byte{{nil}})
					storageSQL := "CREATE TABLE server_codec_storage(v " + c.typ + ")"
					storage := w.query(t, storageSQL)
					serverWantCode(t, storage, "")
					serverWantCompletions(t, serverCycle(t, w, serverParse("", "INSERT INTO server_codec_storage VALUES ($1)", c.oid), serverBind("", "", []int{1}, [][]byte{raw}), serverExecute("", 0)), "INSERT 0 1")
					serverWantRaw(t, serverCycle(t, w, serverParse("", "SELECT v FROM server_codec_storage"), serverBind("", "", nil, nil, 1), serverExecute("", 0)), [][][]byte{{raw}})
					serverWantCode(t, w.query(t, "DROP TABLE server_codec_storage"), "")
				})
			}
			for _, c := range serverCodecCases {
				arrayOID, ok := serverArrayOIDs[c.oid]
				if !ok {
					continue
				}
				t.Run(c.typ+"_array", func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					raw, err := hex.DecodeString(c.wire)
					if err != nil {
						t.Fatal(err)
					}
					array := serverPut32(nil, 1)
					array = serverPut32(array, 0)
					array = serverPut32(array, c.oid)
					array = serverPut32(array, 1)
					array = serverPut32(array, 1)
					array = serverPut32(array, uint32(len(raw)))
					array = append(array, raw...)
					frames := serverCycle(t, w, serverParse("", "SELECT $1", arrayOID), serverBind("", "", []int{1}, [][]byte{array}, 1), serverExecute("", 0))
					lookupOnly := c.oid == 790 || c.oid == 2203 || c.oid == 2204
					if !b.PG && lookupOnly {
						serverWantTags(t, frames, "EZ")
						serverNotificationError(t, frames, "0A000", "type "+c.typ+"[] is not supported", "")
					} else {
						serverWantRaw(t, frames, [][][]byte{{array}})
					}
					frames = serverCycle(t, w, serverParse("", "SELECT $1", arrayOID), serverBind("", "", nil, [][]byte{[]byte("{}")}, 0), serverExecute("", 0))
					if !b.PG && lookupOnly {
						serverWantTags(t, frames, "EZ")
						serverNotificationError(t, frames, "0A000", "type "+c.typ+"[] is not supported", "")
					} else {
						serverWantRaw(t, frames, [][][]byte{{[]byte("{}")}})
					}
				})
			}
			t.Run("catalog_owned", func(t *testing.T) {
				w := serverAuthenticated(t, f.DSNFor(b))
				serverWantCode(t, w.query(t, "CREATE TYPE server_codec_enum AS ENUM ('red','blue'); CREATE DOMAIN server_codec_domain AS integer CHECK (VALUE > 0); CREATE TYPE server_codec_record AS (n integer,v text)"), "")
				for _, c := range []struct {
					typ, text string
					raw       []byte
				}{
					{"server_codec_enum", "red", []byte("red")}, {"server_codec_domain", "42", []byte{0, 0, 0, 42}},
					{"server_codec_record", "(42,hi)", append(append(append(append(serverPut32(nil, 2), serverPut32(nil, 23)...), serverPut32(nil, 4)...), []byte{0, 0, 0, 42}...), append(append(serverPut32(nil, 25), serverPut32(nil, 2)...), []byte("hi")...)...)},
				} {
					oid := serverCodecTypeOID(t, w, c.typ)
					for _, format := range []int{0, 1} {
						input := []byte(c.text)
						if format == 1 {
							input = c.raw
						}
						frames := serverCycle(t, w, serverParse("", "SELECT $1", oid), serverBind("", "", []int{format}, [][]byte{input}, format), serverExecute("", 0))
						want := []byte(c.text)
						if format == 1 {
							want = c.raw
						}
						serverWantRaw(t, frames, [][][]byte{{want}})
					}
				}
			})
		})
	}
}

func serverCodecTypeOID(t *testing.T, w *serverWire, name string) uint32 {
	t.Helper()
	// Names are fixed fixtures; the public regtype authority supplies generated
	// catalog identity only. Expected codec bytes never come from server output.
	frames := w.query(t, "SELECT '"+name+"'::regtype::oid")
	serverWantCode(t, frames, "")
	rows := serverValues(t, frames)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("type identity rows %v", rows)
	}
	oid, err := strconv.ParseUint(rows[0][0], 10, 32)
	if err != nil || oid == 0 {
		t.Fatalf("type %s identity %v: %v", name, rows, err)
	}
	return uint32(oid)
}
func TestReferenceServerExtendedMetadata(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE TABLE server_metadata(i integer,v varchar(7),n numeric(6,2)); INSERT INTO server_metadata VALUES (NULL,NULL,NULL)"), "")
			for _, filter := range []string{"", " WHERE false"} {
				frames := serverCycle(t, w, serverParse("", "SELECT i,v,n FROM server_metadata"+filter), serverDescribe('S', ""), serverBind("", "", nil, nil, 1, 0, 1), serverDescribe('P', ""), serverExecute("", 0))
				serverWantCode(t, frames, "")
				var descriptions [][]serverColumn
				for _, fr := range frames {
					if fr.tag == 'T' {
						descriptions = append(descriptions, serverColumns(t, []serverFrame{fr}))
					}
				}
				if len(descriptions) != 2 {
					t.Fatal(descriptions)
				}
				expected := []serverColumn{{"i", descriptions[0][0].table, 1, 23, 4, -1, 0}, {"v", descriptions[0][0].table, 2, 1043, -1, 11, 0}, {"n", descriptions[0][0].table, 3, 1700, -1, (6 << 16) + 2 + 4, 0}}
				if expected[0].table == 0 || !reflect.DeepEqual(descriptions[0], expected) {
					t.Fatalf("statement metadata %+v want %+v", descriptions[0], expected)
				}
				expected[0].format = 1
				expected[2].format = 1
				if !reflect.DeepEqual(descriptions[1], expected) {
					t.Fatalf("portal metadata %+v want %+v", descriptions[1], expected)
				}
				if filter == "" {
					serverWantRaw(t, frames, [][][]byte{{nil, nil, nil}})
				} else if len(serverRawRows(t, frames)) != 0 {
					t.Fatal("unexpected rows")
				}
			}
			frames := serverCycle(t, w, serverParse("", "SELECT $1::text,$2::bytea"), serverBind("", "", nil, [][]byte{[]byte{}, []byte{}}, 1), serverExecute("", 0))
			serverWantRaw(t, frames, [][][]byte{{{}, {}}})
			for _, c := range []struct {
				formats       []int
				args          [][]byte
				code, message string
			}{
				{[]int{0, 0}, [][]byte{[]byte("1")}, "08P01", "bind message has 2 parameter formats but 1 parameters"},
				{[]int{2}, [][]byte{[]byte("1")}, "22023", "unsupported format code: 2"},
			} {
				frames := serverCycle(t, w, serverParse("", "SELECT $1::integer"), serverBind("", "", c.formats, c.args), serverExecute("", 0))
				serverWantCode(t, frames, c.code)
				for _, fr := range frames {
					if fr.tag == 'E' && serverFields(fr.body)['M'] != c.message {
						t.Fatalf("diagnostic %v", serverFields(fr.body))
					}
				}
			}
		})
	}
}
