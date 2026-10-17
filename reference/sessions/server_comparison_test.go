//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const serverComparisonCount = "SELECT COUNT(*) FROM pg_namespace WHERE nspname=$1"

// PostgreSQL18 protocol-flow and REL_18_6 name.c/pg_operator.dat independently
// specify these identities and full VARCHAR (not name-clipped) comparisons.
func TestReferenceServerComparisonOriginalVarchar(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, name := range []string{"held_comparison", ""} {
				w := serverAuthenticated(t, f.DSNFor(b))
				frames := serverCycle(t, w, serverParse(name, serverComparisonCount, 1043), serverDescribe('S', name))
				serverComparisonDescription(t, frames, []uint32{1043}, []uint32{20})
				for _, format := range []int{0, 1} {
					for _, c := range []struct {
						value []byte
						count string
					}{
						{[]byte("public"), "1"}, {[]byte("comparison_absent_wire_186"), "0"}, {nil, "0"}, {[]byte("public"), "1"},
					} {
						frames = serverCycle(t, w, serverBind("", name, []int{format}, [][]byte{c.value}), serverDescribe('P', ""), serverExecute("", 0))
						serverWantRaw(t, frames, [][][]byte{{[]byte(c.count)}})
						cols := serverColumns(t, frames)
						if len(cols) != 1 || cols[0].name != "count" || cols[0].oid != 20 || cols[0].format != 0 {
							t.Fatalf("count metadata: %+v", cols)
						}
					}
				}
				serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', name)), []uint32{1043}, []uint32{20})
			}
			for _, c := range []struct {
				name     string
				hints    []uint32
				inferred uint32
			}{
				{"omitted", nil, 19}, {"zero", []uint32{0}, 19}, {"text", []uint32{25}, 25}, {"name", []uint32{19}, 19},
			} {
				w := serverAuthenticated(t, f.DSNFor(b))
				serverComparisonDescription(t, serverCycle(t, w, serverParse(c.name, serverComparisonCount, c.hints...), serverDescribe('S', c.name)), []uint32{c.inferred}, []uint32{20})
				serverWantRaw(t, serverCycle(t, w, serverBind("", c.name, nil, [][]byte{[]byte("public")}), serverExecute("", 0)), [][][]byte{{[]byte("1")}})
			}
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, serverCycle(t, w, serverParse("schema_count", serverComparisonCount, 1043)), "")
			const schema = "comparison_schema_wire_186"
			t.Cleanup(func() { _, _ = b.DB.ExecContext(f.Ctx, "DROP SCHEMA IF EXISTS "+schema) })
			for _, c := range []struct{ ddl, count string }{
				{"", "0"}, {"CREATE SCHEMA " + schema, "1"}, {"DROP SCHEMA " + schema, "0"},
			} {
				if c.ddl != "" {
					serverWantCode(t, w.query(t, c.ddl), "")
				}
				serverWantRaw(t, serverCycle(t, w, serverBind("", "schema_count", nil, [][]byte{[]byte(schema)}), serverExecute("", 0)), [][][]byte{{[]byte(c.count)}})
				serverComparisonDescription(t, serverCycle(t, w, serverDescribe('S', "schema_count")), []uint32{1043}, []uint32{20})
			}
			for _, prefix := range []string{strings.Repeat("a", 63), strings.Repeat("é", 31)} {
				// 62 UTF8 bytes plus é cannot fit in NAME's 63-byte payload.
				full := prefix + "x"
				if len(prefix) == 62 {
					full = prefix + "é"
				}
				q := "SELECT $1 = $2, $1 < $2, $2 > $1"
				serverWantCode(t, serverCycle(t, w, serverParse("", q, 19, 1043)), "")
				serverWantRaw(t, serverCycle(t, w, serverBind("", "", nil, [][]byte{[]byte(full), []byte(full)}), serverExecute("", 0)), [][][]byte{{[]byte("f"), []byte("t"), []byte("t")}})
			}
			bad := serverCycle(t, w, serverParse("bad_comparison", serverComparisonCount, 16), serverBind("", "bad_comparison", nil, [][]byte{[]byte("true")}), serverExecute("", 0))
			serverComparisonError(t, bad, "42883", "operator does not exist: name = boolean", "48", "No operator matches the given name and argument types. You might need to add explicit type casts.")
			serverWantTags(t, bad, "EZ")
			serverNotificationError(t, serverCycle(t, w, serverDescribe('S', "bad_comparison")), "26000", "prepared statement \"bad_comparison\" does not exist", "")
			serverWantRaw(t, serverCycle(t, w, serverBind("", "schema_count", nil, [][]byte{[]byte("public")}), serverExecute("", 0)), [][][]byte{{[]byte("1")}})
		})
	}
}

func serverComparisonDescription(t *testing.T, frames []serverFrame, parameters, results []uint32) {
	t.Helper()
	serverWantCode(t, frames, "")
	seen := false
	for _, f := range frames {
		if f.tag != 't' {
			continue
		}
		if len(f.body) != 2+4*len(parameters) || int(binary.BigEndian.Uint16(f.body)) != len(parameters) {
			t.Fatalf("parameter description %x", f.body)
		}
		got := make([]uint32, len(parameters))
		for i := range got {
			got[i] = binary.BigEndian.Uint32(f.body[2+4*i:])
		}
		if !reflect.DeepEqual(got, parameters) {
			t.Fatalf("source OIDs %v want %v", got, parameters)
		}
		seen = true
	}
	if !seen {
		t.Fatal("missing ParameterDescription")
	}
	cols := serverColumns(t, frames)
	got := make([]uint32, len(cols))
	for i, c := range cols {
		got[i] = c.oid
	}
	if !reflect.DeepEqual(got, results) {
		t.Fatalf("result OIDs %v want %v", got, results)
	}
}

func serverComparisonError(t *testing.T, frames []serverFrame, code, message, position, hint string) {
	t.Helper()
	serverWantCode(t, frames, code)
	count := 0
	for _, f := range frames {
		if f.tag != 'E' {
			continue
		}
		count++
		fields := serverFields(f.body)
		for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': code, 'M': message, 'P': position, 'H': hint, 'D': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
			if fields[key] != want {
				t.Fatalf("field %c = %q want %q: %v", key, fields[key], want, fields)
			}
		}
	}
	if count != 1 {
		t.Fatalf("error count %d", count)
	}
}
