//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 parse_coerce.c generic identities and rowtypes.c logical records.
// Generated OIDs are discovered independently on each backend; they are never
// copied from the oracle or confused with the internal selected RECORD formal.
func TestReferenceServerComparisonStructural(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, `CREATE TYPE comparison_wire_enum AS ENUM ('low','high');
CREATE TYPE comparison_wire_other_enum AS ENUM ('low','high');
CREATE DOMAIN comparison_wire_int AS integer CHECK (VALUE > 0);
CREATE DOMAIN comparison_wire_chain AS comparison_wire_int;
CREATE DOMAIN comparison_wire_bool AS boolean;
CREATE TYPE comparison_wire_record AS (x integer,y integer);
CREATE TYPE comparison_wire_other_record AS (x integer,y integer);
CREATE DOMAIN comparison_wire_record_domain AS comparison_wire_record`), "")
			enum := serverCodecTypeOID(t, w, "comparison_wire_enum")
			otherEnum := serverCodecTypeOID(t, w, "comparison_wire_other_enum")
			domain := serverCodecTypeOID(t, w, "comparison_wire_int")
			chain := serverCodecTypeOID(t, w, "comparison_wire_chain")
			boolean := serverCodecTypeOID(t, w, "comparison_wire_bool")
			record := serverCodecTypeOID(t, w, "comparison_wire_record")
			otherRecord := serverCodecTypeOID(t, w, "comparison_wire_other_record")
			recordDomain := serverCodecTypeOID(t, w, "comparison_wire_record_domain")
			recordArray := serverCodecTypeOID(t, w, "comparison_wire_record[]")
			// One field1 and one NULL field; composite equality treats NULL fields equal.
			rawRecord := serverPut32(nil, 2)
			rawRecord = serverPut32(rawRecord, 23)
			rawRecord = serverPut32(rawRecord, 4)
			rawRecord = serverPut32(rawRecord, 1)
			rawRecord = serverPut32(rawRecord, 23)
			rawRecord = serverPut32(rawRecord, ^uint32(0))
			rawArray := serverPut32(nil, 1)
			rawArray = serverPut32(rawArray, 0)
			rawArray = serverPut32(rawArray, record)
			rawArray = serverPut32(rawArray, 1)
			rawArray = serverPut32(rawArray, 1)
			rawArray = serverPut32(rawArray, uint32(len(rawRecord)))
			rawArray = append(rawArray, rawRecord...)
			for _, v := range []struct {
				name         string
				left, right  uint32
				text, binary []byte
			}{
				{"enum", enum, enum, []byte("low"), []byte("low")},
				{"domain_base", domain, 23, []byte("1"), []byte{0, 0, 0, 1}},
				{"chain_domain", chain, domain, []byte("1"), []byte{0, 0, 0, 1}},
				{"named_record", record, record, []byte("(1,)"), rawRecord},
				{"different_record_identity", record, otherRecord, []byte("(1,)"), rawRecord},
				{"domain_record", recordDomain, record, []byte("(1,)"), rawRecord},
				{"record_array", recordArray, recordArray, []byte(`{"(1,)"}`), rawArray},
			} {
				t.Run(v.name, func(t *testing.T) {
					for _, oids := range [][]uint32{{v.left, v.right}, {v.right, v.left}} {
						serverComparisonDescription(t, serverCycle(t, w, serverParse("shape", "SELECT $1 = $2, $1 < $2", oids...), serverDescribe('S', "shape")), oids, []uint32{16, 16})
						for _, format := range []int{0, 1} {
							input := v.text
							if format == 1 {
								input = v.binary
							}
							for _, args := range [][][]byte{{input, input}, {nil, input}, {input, nil}, {nil, nil}, {input, input}} {
								want := [][][]byte{{[]byte("t"), []byte("f")}}
								if args[0] == nil || args[1] == nil {
									want = [][][]byte{{nil, nil}}
								}
								serverWantRaw(t, serverCycle(t, w, serverBind("", "shape", []int{format}, args), serverExecute("", 0)), want)
							}
						}
						serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', "shape")), oids, []uint32{16, 16})
						serverWantCode(t, serverCycle(t, w, serverClose('S', "shape")), "")
					}
				})
			}
			// Same textual label is not the same ANYENUM identity in either orientation.
			for _, oids := range [][]uint32{{enum, otherEnum}, {otherEnum, enum}} {
				left, right := "comparison_wire_enum", "comparison_wire_other_enum"
				if oids[0] == otherEnum {
					left, right = right, left
				}
				serverComparisonError(t, serverCycle(t, w, serverParse("bad_enum", "SELECT $1 = $2", oids...)), "42883", "operator does not exist: "+left+" = "+right, "11", "No operator matches the given name and argument types. You might need to add explicit type casts.")
			}
			serverComparisonDescription(t, serverCycle(t, w, serverParse("truth", "SELECT $1 IS TRUE,$1 IS NOT TRUE,$1 IS UNKNOWN", boolean), serverDescribe('S', "truth")), []uint32{boolean}, []uint32{16, 16, 16})
			for _, format := range []int{0, 1} {
				for _, v := range []struct {
					text, binary []byte
					want         [][][]byte
				}{
					{[]byte("true"), []byte{1}, [][][]byte{{[]byte("t"), []byte("f"), []byte("f")}}},
					{[]byte("false"), []byte{0}, [][][]byte{{[]byte("f"), []byte("t"), []byte("f")}}},
					{nil, nil, [][][]byte{{[]byte("f"), []byte("t"), []byte("t")}}},
				} {
					input := v.text
					if format == 1 {
						input = v.binary
					}
					serverWantRaw(t, serverCycle(t, w, serverBind("", "truth", []int{format}, [][]byte{input}), serverExecute("", 0)), v.want)
				}
			}
			serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', "truth")), []uint32{boolean}, []uint32{16, 16, 16})
		})
	}
}
