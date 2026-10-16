//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerExtendedLifecycle(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			t.Run("normal_describe", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				frames := serverCycle(t, w, serverParse("s", "SELECT $1::integer AS answer", 0), serverDescribe('S', "s"), serverBind("p", "s", nil, [][]byte{[]byte("42")}, 1), serverDescribe('P', "p"), serverExecute("p", 0))
				serverWantTags(t, frames, "1tT2TDCZ")
				serverWantRaw(t, frames, [][][]byte{{{0, 0, 0, 42}}})
				serverWantCompletions(t, frames, "SELECT 1")
				for _, v := range frames {
					if v.tag == 't' && string(v.body) != "\x00\x01\x00\x00\x00\x17" {
						t.Fatalf("parameter description %x", v.body)
					}
				}
			})
			t.Run("empty_and_nodata", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				serverWantTags(t, serverCycle(t, w, serverParse("empty", ""), serverDescribe('S', "empty"), serverBind("e", "empty", nil, nil), serverDescribe('P', "e"), serverExecute("e", 1)), "1tn2nIZ")
				serverWantTags(t, serverCycle(t, w, serverParse("ddl", "CREATE TABLE server_describe_only(v int)"), serverDescribe('S', "ddl")), "1tnZ")
				serverScalar(t, w, "SELECT count(*) FROM pg_class WHERE relname='server_describe_only'", "0")
			})
			t.Run("shared_names", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "PREPARE from_sql(integer) AS SELECT $1 + 1"), "")
				frames := serverCycle(t, w, serverBind("p", "from_sql", nil, [][]byte{[]byte("41")}), serverExecute("p", 0))
				serverWantTags(t, frames, "2DCZ")
				serverWantValues(t, frames, [][]string{{"42"}})
				serverWantTags(t, serverCycle(t, w, serverParse("from_wire", "SELECT $1::integer + 2", 23)), "1Z")
				serverScalar(t, w, "EXECUTE from_wire(40)", "42")
				serverWantCode(t, w.query(t, "DEALLOCATE from_wire"), "")
				serverNotificationError(t, serverCycle(t, w, serverBind("p", "from_wire", nil, [][]byte{[]byte("40")})), "26000", "prepared statement \"from_wire\" does not exist", "")
			})
			t.Run("replacement_and_close", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "BEGIN"), "")
				serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT 42"), serverBind("p", "s", nil, nil), serverClose('S', "s")), "123Z")
				serverWantValues(t, serverCycle(t, w, serverExecute("p", 0)), [][]string{{"42"}})
				serverWantTags(t, serverCycle(t, w, serverClose('P', "p"), serverClose('S', "missing")), "33Z")
				serverWantTags(t, serverCycle(t, w, serverParse("", "SELECT 1"), serverParse("", "SELECT 42"), serverBind("", "", nil, nil), serverExecute("", 0)), "112DCZ")
				serverWantCode(t, w.query(t, "ROLLBACK"), "")
				serverScalar(t, w, "SELECT 1", "1")
				serverNotificationError(t, serverCycle(t, w, serverBind("", "", nil, nil)), "26000", "unnamed prepared statement does not exist", "")
			})
			for _, c := range []struct {
				name, sql, code, message string
				oids                     []uint32
				packets                  func() [][]byte
			}{
				{name: "duplicate_statement", code: "42P05", message: "prepared statement \"s\" already exists", packets: func() [][]byte { return [][]byte{serverParse("s", "SELECT 1"), serverParse("s", "SELECT 2")} }},
				{name: "duplicate_portal", code: "42P03", message: "cursor \"p\" already exists", packets: func() [][]byte {
					return [][]byte{serverParse("s", "SELECT 1"), serverBind("p", "s", nil, nil), serverBind("p", "s", nil, nil)}
				}},
				{name: "missing_portal", code: "34000", message: "portal \"absent\" does not exist", packets: func() [][]byte { return [][]byte{serverExecute("absent", 0)} }},
				{name: "multi_command", sql: "SELECT 1; SELECT 2", code: "42601", message: "cannot insert multiple commands into a prepared statement"},
				{name: "sparse_unknown", sql: "SELECT $2::integer", code: "42P18", message: "could not determine data type of parameter $1"},
				{name: "wrong_count", code: "08P01", message: "bind message supplies 0 parameters, but prepared statement \"s\" requires 1", packets: func() [][]byte {
					return [][]byte{serverParse("s", "SELECT $1::integer"), serverBind("p", "s", nil, nil)}
				}},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					packets := [][]byte{serverParse("s", c.sql, c.oids...)}
					if c.packets != nil {
						packets = c.packets()
					}
					frames := serverCycle(t, w, packets...)
					serverNotificationError(t, frames, c.code, c.message, "")
					serverWantReady(t, frames, 'I')
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("unused_sparse_formals", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				frames := serverCycle(t, w, serverParse("s", "SELECT $2::integer", 23, 23), serverBind("p", "s", nil, [][]byte{[]byte("99"), []byte("42")}), serverExecute("p", 0))
				serverWantTags(t, frames, "12DCZ")
				serverWantValues(t, frames, [][]string{{"42"}})
			})
			t.Run("empty_oid_hints", func(t *testing.T) {
				for _, oid := range []uint32{0, 23, 4294967295} {
					w := serverAuthenticated(t, dsn)
					frames := serverCycle(t, w, serverParse("empty", "", oid), serverDescribe('S', "empty"))
					serverWantTags(t, frames, "1tnZ")
					want := serverPut32(server16(nil, 1), oid)
					for _, fr := range frames {
						if fr.tag == 't' && string(fr.body) != string(want) {
							t.Fatalf("empty hint parameter description %x want %x", fr.body, want)
						}
					}
				}
			})
			t.Run("unused_result_format", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				frames := serverCycle(t, w, serverParse("s", "SELECT NULL::integer"), serverBind("p", "s", nil, nil, 2), serverExecute("p", 0))
				serverNotificationError(t, frames, "22023", "unsupported format code: 2", "")
			})
		})
	}
}
