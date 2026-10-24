//go:build !noservertests

package reference_test

import (
	"bytes"
	"testing"

	"stuntgres/reference/fixture"
)

func threePartWireSetup(t *testing.T, w *serverWire) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE public.nonces(id uuid,note text)",
		"INSERT INTO public.nonces VALUES('00000000-0000-0000-0000-000000000001','match'),('00000000-0000-0000-0000-000000000002','other'),(NULL,'null')",
		"CREATE TABLE public.items(id int,v int)", "INSERT INTO public.items VALUES(1,10),(2,20),(3,30)",
	} {
		serverWantCode(t, w.query(t, q), "")
	}
}

func threePartWireColumns(t *testing.T, frames []serverFrame) {
	t.Helper()
	for _, cols := range serverDescriptions(t, frames) {
		if len(cols) != 2 {
			t.Fatalf("columns=%+v", cols)
		}
		for i, name := range []string{"id", "v"} {
			c := cols[i]
			if c.name != name || c.oid != 23 || c.length != 4 || c.typmod != -1 || c.format != 0 || c.table == 0 || c.attribute != int16(i+1) {
				t.Fatalf("column=%+v", c)
			}
		}
		if cols[0].table != cols[1].table {
			t.Fatal("lost common origin")
		}
	}
}

func TestReferenceThreePartWireSimple(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			threePartWireSetup(t, w)
			for _, q := range []string{
				"PREPARE three_del(uuid) AS " + threePartHeldDelete,
				"PREPARE three_sel(int) AS SELECT public.items.id,public.items.v FROM public.items WHERE public.items.id >= $1 ORDER BY public.items.id DESC",
				"PREPARE three_up(int,int) AS UPDATE public.items SET v=public.items.v+$1 WHERE public.items.id=$2 RETURNING public.items.id,public.items.v",
			} {
				serverWantCompletions(t, w.query(t, q), "PREPARE")
			}
			check := func(q, tag string, rows [][]string) {
				frames := w.query(t, q)
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, tag)
				serverWantValues(t, frames, rows)
				serverWantReady(t, frames, 'I')
				if len(serverDescriptions(t, frames)) > 0 {
					threePartWireColumns(t, frames)
				}
			}
			check("SELECT public.items.id,public.items.v FROM public.items WHERE public.items.id>=2 ORDER BY public.items.id DESC", "SELECT 2", [][]string{{"3", "30"}, {"2", "20"}})
			check("EXECUTE three_sel(99)", "SELECT 0", nil)
			check("EXECUTE three_sel(2)", "SELECT 2", [][]string{{"3", "30"}, {"2", "20"}})
			check("EXECUTE three_up(5,2)", "UPDATE 1", [][]string{{"2", "25"}})
			check("EXECUTE three_up(5,99)", "UPDATE 0", nil)
			check("UPDATE public.items SET v=public.items.v-5 WHERE public.items.id=2 RETURNING public.items.id,public.items.v", "UPDATE 1", [][]string{{"2", "20"}})
			frames := w.query(t, "BEGIN")
			serverWantCompletions(t, frames, "BEGIN")
			serverWantReady(t, frames, 'T')
			frames = w.query(t, "EXECUTE three_del('"+threePartHeldUUID+"')")
			serverWantCompletions(t, frames, "DELETE 1")
			serverWantReady(t, frames, 'T')
			frames = w.query(t, "ROLLBACK")
			serverWantCompletions(t, frames, "ROLLBACK")
			serverWantReady(t, frames, 'I')
			serverScalar(t, w, "SELECT count(*) FROM public.nonces", "3")
			check("EXECUTE three_del('"+threePartHeldUUID+"')", "DELETE 1", nil)
			check("EXECUTE three_del('"+threePartHeldUUID+"')", "DELETE 0", nil)
			check("EXECUTE three_del(NULL)", "DELETE 0", nil)
			check("delete from PUBLIC.NONCES where PUBLIC.NONCES.ID=cast('00000000-0000-0000-0000-000000000002' as uuid)", "DELETE 1", nil)
			serverScalar(t, w, "SELECT note FROM public.nonces", "null")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			frames = w.query(t, "SELECT 1/0")
			serverWantCode(t, frames, "22012")
			serverWantReady(t, frames, 'E')
			serverWantCode(t, w.query(t, "ROLLBACK"), "")
			serverScalar(t, w, "SELECT count(*) FROM public.items", "3")
		})
	}
}

func TestReferenceThreePartWireExtended(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			threePartWireSetup(t, w)
			for _, tc := range []struct {
				name, q, tags string
				oids          []uint32
			}{
				{"del", threePartHeldDelete, "1tnZ", []uint32{2950}},
				{"sel", "SELECT public.items.id,public.items.v FROM public.items WHERE public.items.id >= $1 ORDER BY public.items.id DESC", "1tTZ", []uint32{23}},
				{"up", "UPDATE public.items SET v=public.items.v+$1 WHERE public.items.id=$2 RETURNING public.items.id,public.items.v", "1tTZ", []uint32{23, 23}},
			} {
				frames := serverCycle(t, w, serverParse(tc.name, tc.q), serverDescribe('S', tc.name))
				serverWantTags(t, frames, tc.tags)
				want := server16(nil, len(tc.oids))
				for _, oid := range tc.oids {
					want = serverPut32(want, oid)
				}
				for _, fr := range frames {
					if fr.tag == 't' && !bytes.Equal(fr.body, want) {
						t.Fatalf("parameter types=%x want=%x", fr.body, want)
					}
				}
				threePartWireColumns(t, frames)
			}
			// Describe is effect-free; this simple query cannot destroy the named handles.
			serverScalar(t, w, "SELECT count(*) FROM public.nonces", "3")
			bind := func(name, tag string, args [][]byte, rows [][]string, returns bool) {
				frames := serverCycle(t, w, serverBind("", name, nil, args), serverDescribe('P', ""), serverExecute("", 0))
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, tag)
				serverWantValues(t, frames, rows)
				serverWantReady(t, frames, 'I')
				if returns {
					threePartWireColumns(t, frames)
					if len(serverDescriptions(t, frames)) != 1 {
						t.Fatal("missing portal description")
					}
				} else {
					serverWantTags(t, frames, "2nCZ")
				}
			}
			bind("sel", "SELECT 2", [][]byte{[]byte("2")}, [][]string{{"3", "30"}, {"2", "20"}}, true)
			bind("sel", "SELECT 0", [][]byte{[]byte("99")}, nil, true)
			bind("sel", "SELECT 0", [][]byte{nil}, nil, true)
			bind("up", "UPDATE 1", [][]byte{[]byte("5"), []byte("2")}, [][]string{{"2", "25"}}, true)
			bind("up", "UPDATE 0", [][]byte{[]byte("5"), []byte("99")}, nil, true)
			bind("up", "UPDATE 0", [][]byte{[]byte("5"), nil}, nil, true)
			frames := serverCycle(t, w, serverParse("path", "SET search_path=pg_catalog,public"), serverBind("", "path", nil, nil), serverExecute("", 0))
			serverWantCompletions(t, frames, "SET")
			bind("del", "DELETE 1", [][]byte{[]byte(threePartHeldUUID)}, nil, false)
			bind("del", "DELETE 0", [][]byte{[]byte(threePartHeldUUID)}, nil, false)
			bind("del", "DELETE 0", [][]byte{nil}, nil, false)
			serverScalar(t, w, "SELECT count(*) FROM public.nonces", "2")
			serverWantValues(t, w.query(t, "SELECT note FROM public.nonces ORDER BY note"), [][]string{{"null"}, {"other"}})
			bind("sel", "SELECT 2", [][]byte{[]byte("2")}, [][]string{{"3", "30"}, {"2", "25"}}, true)
		})
	}
}

func TestReferenceThreePartServerDriver(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "qa")
	fixture.SchemaReferenceCleanup(t, "qb")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) { threePartHeldScenario(t, f.Ctx, b) })
	}
}
