package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pglMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 3 {
		t.Fatal("page metadata width", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 2 {
			scan = reflect.TypeFor[[]byte]()
		}
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 1 {
				scan = reflect.TypeFor[int32]()
			}
			if i == 2 {
				scan = reflect.TypeFor[[]byte]()
			}
		}
		if typ.Name() != []string{"loid", "pageno", "data"}[i] || typ.DatabaseTypeName() != []string{"OID", "INT4", "BYTEA"}[i] || typ.ScanType() != scan {
			t.Fatalf("page metadata %d: %s %s %v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
		}
		if nullable, known := typ.Nullable(); nullable || known != !pg {
			t.Fatal("nullability", i, nullable, known)
		}
		wantLength, wantLengthKnown := int64(0), false
		// Pinned PostgreSQL18.6 discovery: pq reports unbounded BYTEA length.
		if pg && i == 2 {
			wantLength, wantLengthKnown = 9223372036854775807, true
		}
		if length, known := typ.Length(); length != wantLength || known != wantLengthKnown {
			t.Fatal("length", i, length, known, wantLength, wantLengthKnown)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("decimal", i, precision, scale, known)
		}
	}
}

func TestReferencePGLargeobjectShape(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, relation := range []string{"pg_largeobject", "pg_catalog.pg_largeobject"} {
			s := fixture.Select("SELECT * FROM "+relation+" WHERE FALSE", []string{"loid", "pageno", "data"}, []string{"OID", "INT4", "BYTEA"}, [][]any{})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				pglMetadata(t, types, b.PG)
			}
			run(s)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT * FROM pg_largeobject WHERE FALSE")
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal("nonempty empty cursor", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		pglMetadata(t, types, b.PG)
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		run(fixture.Select("SELECT c.oid,c.relnamespace,c.relkind,c.relpersistence,c.relnatts,c.relhasindex,r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid=2613", []string{"oid", "relnamespace", "relkind", "relpersistence", "relnatts", "relhasindex", "rolname"}, []string{"OID", "OID", "CHAR", "CHAR", "INT2", "BOOL", "NAME"}, [][]any{{int64(2613), int64(11), "r", "p", int64(3), true, owner}}))
		run(fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attnotnull,attcollation,attndims FROM pg_attribute WHERE attrelid=2613 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "INT4", "BOOL", "OID", "INT2"}, [][]any{{int64(1), "loid", int64(26), int64(-1), true, int64(0), int64(0)}, {int64(2), "pageno", int64(23), int64(-1), true, int64(0), int64(0)}, {int64(3), "data", int64(17), int64(-1), true, int64(0), int64(0)}}))
		run(fixture.Select("SELECT t.oid,t.typname,t.typrelid,t.typarray,a.typelem FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.typrelid=2613", []string{"oid", "typname", "typrelid", "typarray", "typelem"}, []string{"OID", "NAME", "OID", "OID", "OID"}, [][]any{{int64(10025), "pg_largeobject", int64(2613), int64(10024), int64(10025)}}))
		run(fixture.Select("SELECT fkcols,pktable::oid,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() WHERE fktable::oid=2613", []string{"fkcols", "pktable", "pkcols", "is_array", "is_opt"}, []string{"_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, [][]any{{"{loid}", int64(2995), "{oid}", false, false}}))
	})
}

func TestReferencePGLargeobjectPages(t *testing.T) {
	pglScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(pglCreate(920101, strings.Repeat("00ff", 1025)))
		page := bytes.Repeat([]byte{0, 255}, 1024)
		run(pglPages(920101, 0, page, 1, []byte{0, 255}))
		run(pglValue("lo_create(920102::oid)", "OID", int64(920102)))
		run(pglPages(920102))
		run(pglPut(920102, 4097, "dead00ff"))
		run(pglPages(920102, 2, []byte{0, 222, 173, 0, 255}))
		run(pglCreate(920103, "6162"))
		run(pglPut(920103, 4097, "dead00ff"))
		run(pglPages(920103, 0, []byte("ab"), 2, []byte{0, 222, 173, 0, 255}))
		run(pglValue("lo_create(920104::oid)", "OID", int64(920104)))
		run(pglPut(920104, 2047, "0080ff"))
		run(pglPages(920104, 0, make([]byte, 2048), 1, []byte{128, 255}))
		run(pglValue("lo_create(920105::oid)", "OID", int64(920105)))
		run(pglPut(920105, 4294967296, "666f6f"))
		run(pglPages(920105, 2097152, []byte("foo")))
		run(pglPut(920101, 0, "ff00"))
		changed := append([]byte(nil), page...)
		changed[0], changed[1] = 255, 0
		run(pglPages(920101, 0, changed, 1, []byte{0, 255}))
		run(pglValue("lo_unlink(920101::oid)", "INT4", int64(1)))
		run(pglPages(920101))
		run(pglPages(920102, 2, []byte{0, 222, 173, 0, 255}))
	})
}
