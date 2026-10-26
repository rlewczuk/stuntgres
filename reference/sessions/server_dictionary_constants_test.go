//go:build !noservertests

package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerDictionaryPreparedConstants(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			const constantSQL = "SELECT ts_lexize('public.dictionary_wire_constant'::regdictionary,'YeS'::text) AS lexemes"
			const runtimeSQL = "SELECT ts_lexize('public.dictionary_wire_constant'::regdictionary,$1::text) AS lexemes"
			columns := func(frames []serverFrame) {
				t.Helper()
				want := [][]serverColumn{{{name: "lexemes", oid: 1009, length: -1, typmod: -1}}}
				if got := serverDescriptions(t, frames); !reflect.DeepEqual(got, want) {
					t.Fatalf("TEXT[] descriptions=%v want=%v", got, want)
				}
			}
			command := func(sql, tag string) {
				t.Helper()
				frames := w.query(t, sql)
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "CZ")
				serverWantReady(t, frames, 'I')
				if string(frames[0].body) != tag+"\x00" {
					t.Fatalf("command=%q want=%q", frames[0].body, tag)
				}
			}
			parse := func(name, sql string, parameters []byte) {
				t.Helper()
				frames := serverCycle(t, w, serverParse(name, sql), serverDescribe('S', name))
				serverWantCode(t, frames, "")
				serverWantTags(t, frames, "1tTZ")
				serverWantReady(t, frames, 'I')
				if !reflect.DeepEqual(frames[1].body, parameters) {
					t.Fatalf("parameter description=%x want=%x", frames[1].body, parameters)
				}
				columns(frames)
			}
			read := func(name string, args [][]byte, want []byte) {
				t.Helper()
				// Every call makes a new portal from the same named definition.
				// Bind and Execute share a cycle because idle Sync retires portals.
				frames := serverCycle(t, w, serverBind("", name, nil, args), serverDescribe('P', ""), serverExecute("", 0))
				serverWantTags(t, frames, "2TDCZ")
				serverWantReady(t, frames, 'I')
				columns(frames)
				serverWantRaw(t, frames, [][][]byte{{want}})
				if string(frames[3].body) != "SELECT 1\x00" {
					t.Fatalf("command=%q want SELECT 1", frames[3].body)
				}
			}
			command("CREATE TEXT SEARCH DICTIONARY public.dictionary_wire_constant (template=pg_catalog.simple, stopwords=english)", "CREATE TEXT SEARCH DICTIONARY")
			defer command("DROP TEXT SEARCH DICTIONARY public.dictionary_wire_constant", "DROP TEXT SEARCH DICTIONARY")
			// Wire RowDescription identifies _TEXT by its catalog OID, not an
			// adapter's spelling of DatabaseTypeName.
			serverScalar(t, w, "SELECT typname FROM pg_type WHERE oid=1009", "_text")
			parse("dictionary_held", constantSQL, []byte{0, 0})
			parse("dictionary_runtime", runtimeSQL, []byte{0, 1, 0, 0, 0, 25})
			read("dictionary_held", nil, []byte("{yes}"))
			read("dictionary_runtime", [][]byte{[]byte("YeS")}, []byte("{yes}"))

			command("ALTER TEXT SEARCH DICTIONARY public.dictionary_wire_constant (accept=false)", "ALTER TEXT SEARCH DICTIONARY")
			// The dictionary operand revision now invalidates ordinary analysis;
			// a repeated Bind must still retain the accepted constant lexemes.
			read("dictionary_held", nil, []byte("{yes}"))
			parse("dictionary_fresh", constantSQL, []byte{0, 0})
			read("dictionary_fresh", nil, nil)
			read("dictionary_runtime", [][]byte{[]byte("YeS")}, nil)
			read("dictionary_runtime", [][]byte{[]byte("The")}, []byte("{}"))
			read("dictionary_runtime", [][]byte{[]byte("")}, []byte("{}"))
			read("dictionary_runtime", [][]byte{nil}, nil)

			// A valid earlier option in a rejected ALTER must not publish either.
			frames := w.query(t, "ALTER TEXT SEARCH DICTIONARY public.dictionary_wire_constant (accept=true, bogus=1)")
			serverNotificationError(t, frames, "22023", `unrecognized simple dictionary parameter: "bogus"`, "")
			serverWantTags(t, frames, "EZ")
			serverWantReady(t, frames, 'I')
			read("dictionary_held", nil, []byte("{yes}"))
			read("dictionary_fresh", nil, nil)
			read("dictionary_runtime", [][]byte{[]byte("YeS")}, nil)
			read("dictionary_runtime", [][]byte{[]byte("The")}, []byte("{}"))
			parse("dictionary_after_rejection", constantSQL, []byte{0, 0})
			read("dictionary_after_rejection", nil, nil)
		})
	}
}
