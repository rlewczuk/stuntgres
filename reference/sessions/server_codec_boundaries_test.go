//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func serverCodecBoundaries(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) {
	t.Helper()
	dsn := f.DSNFor(b)
	for _, c := range []struct {
		typ, text string
		oid       uint32
		binary    []byte
	}{
		{"tid", "(1,2)", 27, []byte{0, 0, 0, 1, 0, 2}},
		{"aclitem", "=r/stuntgres", 1033, nil},
	} {
		t.Run(c.typ+"_capability", func(t *testing.T) {
			w := serverAuthenticated(t, dsn)
			frames := serverCycle(t, w, serverParse("", "SELECT $1", c.oid), serverBind("", "", nil, [][]byte{[]byte(c.text)}), serverExecute("", 0))
			serverWantRaw(t, frames, [][][]byte{{[]byte(c.text)}})
			frames = serverCycle(t, w, serverParse("", "SELECT $1", c.oid), serverBind("", "", nil, [][]byte{[]byte(c.text)}, 1), serverExecute("", 0))
			if b.PG {
				if c.binary != nil {
					serverWantRaw(t, frames, [][][]byte{{c.binary}})
				} else {
					serverNotificationError(t, frames, "42883", "no binary output function available for type "+c.typ, "")
				}
			} else if c.typ == "tid" {
				serverNotificationError(t, frames, "0A000", "binary output is not supported for type tid", "")
			} else {
				serverNotificationError(t, frames, "42883", "no binary output function available for type "+c.typ, "")
			}
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
	t.Run("forbidden_input", func(t *testing.T) {
		for _, c := range []struct {
			typ, text, code, message string
			oid                      uint32
		}{{"pg_node_tree", "x", "0A000", "cannot accept a value of type pg_node_tree", 194}, {"record", "(1)", "0A000", "input of anonymous composite types is not implemented", 2249}} {
			w := serverAuthenticated(t, dsn)
			frames := serverCycle(t, w, serverParse("", "SELECT $1", c.oid), serverBind("", "", nil, [][]byte{[]byte(c.text)}), serverExecute("", 0))
			serverWantCode(t, frames, c.code)
			for _, fr := range frames {
				if fr.tag == 'E' && serverFields(fr.body)['M'] != c.message {
					t.Fatal(serverFields(fr.body))
				}
			}
		}
	})
	t.Run("named_text_search_boundary", func(t *testing.T) {
		for _, c := range []struct {
			oid           uint32
			code, message string
		}{
			{3734, "0A000", "text search configuration name lookup is not supported"},
			{3769, "42704", "text search dictionary \"simple\" does not exist"},
		} {
			w := serverAuthenticated(t, dsn)
			frames := serverCycle(t, w, serverParse("", "SELECT $1", c.oid), serverBind("", "", nil, [][]byte{[]byte("simple")}), serverExecute("", 0))
			if b.PG {
				serverWantTags(t, frames, "12DCZ")
				serverWantRaw(t, frames, [][][]byte{{[]byte("simple")}})
			} else {
				serverWantTags(t, frames, "1EZ")
				serverWantCode(t, frames, c.code)
				for _, fr := range frames {
					if fr.tag == 'E' && serverFields(fr.body)['M'] != c.message {
						t.Fatal(serverFields(fr.body))
					}
				}
			}
		}
	})
	t.Run("installed_query_int", func(t *testing.T) {
		w := serverAuthenticated(t, dsn)
		serverWantCode(t, w.query(t, "CREATE EXTENSION intarray"), "")
		// Native installation belongs to this isolated server fixture. DROP is
		// intentionally unavailable there; the persistent oracle needs cleanup.
		if b.PG {
			defer func() { serverWantCode(t, w.query(t, "DROP EXTENSION intarray"), "") }()
		}
		oid := serverCodecTypeOID(t, w, "query_int")
		frames := serverCycle(t, w, serverParse("", "SELECT $1", oid), serverBind("", "", nil, [][]byte{[]byte("1")}), serverExecute("", 0))
		serverWantRaw(t, frames, [][][]byte{{[]byte("1")}})
		frames = serverCycle(t, w, serverParse("", "SELECT $1", oid), serverBind("", "", nil, [][]byte{[]byte("1")}, 1), serverExecute("", 0))
		serverNotificationError(t, frames, "42883", "no binary output function available for type query_int", "")
	})
}
