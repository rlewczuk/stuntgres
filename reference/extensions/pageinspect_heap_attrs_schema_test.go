package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsSchema(t *testing.T) {
	t.Run("alignment", func(t *testing.T) {
		attrsCase(t, "a smallint,b bigint,c boolean,d integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			attrsSplitRoutes(t, p, b, c, attrsBytes("11220000000000000102030405060708ff000000aabbccdd"), 0, 4, nil, attrsArray("1122", "0102030405060708", "ff", "aabbccdd"))
		})
	})
	for _, dropped := range []string{"a", "b", "c", "a,c"} {
		t.Run("dropped-"+dropped, func(t *testing.T) {
			attrsCase(t, "a integer,b bigint,c integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, name := range strings.Split(dropped, ",") {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE attrs_schema DROP COLUMN "+name, 0))
				}
				attrsSplitRoutes(t, p, b, c, attrsBytes("01020304000000001122334455667788aabbccdd"), 0, 3, nil, attrsArray("01020304", "1122334455667788", "aabbccdd"))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE attrs_schema ADD COLUMN added integer DEFAULT 99", 0))
				attrsSplitRoutes(t, p, b, c, attrsBytes("01020304000000001122334455667788aabbccdd"), 0, 3, nil, attrsArray("01020304", "1122334455667788", "aabbccdd", "NULL"))
			})
		})
	}
	t.Run("domains-and-removed-type", func(t *testing.T) {
		attrsCase(t, "", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE DOMAIN attrs_int AS integer", "CREATE DOMAIN attrs_text AS text", "ALTER TABLE attrs_schema ADD COLUMN a attrs_int", "ALTER TABLE attrs_schema ADD COLUMN b attrs_text"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			attrsSplitRoutes(t, p, b, c, attrsBytes("01020304076162"), 0, 2, nil, attrsArray("01020304", "076162"))
			for _, q := range []string{"ALTER TABLE attrs_schema DROP COLUMN a", "DROP DOMAIN attrs_int"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			attrsSplitRoutes(t, p, b, c, attrsBytes("01020304076162"), 0, 2, nil, attrsArray("01020304", "076162"))
		})
	})
	t.Run("bitmap-boundaries", func(t *testing.T) {
		columns := make([]string, 9)
		for i := range columns {
			columns[i] = fmt.Sprintf("a%d integer", i)
		}
		attrsCase(t, strings.Join(columns, ","), func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, natts := range []int{0, 1, 8, 9} {
				want := make([]string, 9)
				for i := range want {
					want[i] = "NULL"
				}
				attrsSplitRoutes(t, p, b, c, []byte{}, 1, natts, strings.Repeat("0", (natts+7)/8*8), attrsArray(want...))
			}
			attrsSplitRoutes(t, p, b, c, attrsBytes("01020304"), 1, 1, "11111111", attrsArray("01020304", "NULL", "NULL", "NULL", "NULL", "NULL", "NULL", "NULL", "NULL"))
			heapBoth(t, p, b, c, heapError(attrsSplit5, "XX001", "unexpected length of t_bits string: 8, expected 16", []byte{}, 1, 9, "00000000"))
		})
	})
	t.Run("sequence-and-nonheap", func(t *testing.T) {
		attrsCase(t, "a integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS attrs_fdw CASCADE"); err != nil {
					t.Error(err)
				}
			}()
			for _, q := range []string{"CREATE SEQUENCE attrs_seq", "CREATE VIEW attrs_view AS SELECT a FROM attrs_schema", "CREATE INDEX attrs_index ON attrs_schema(a)", "CREATE TABLE attrs_partition(a integer) PARTITION BY RANGE(a)", "CREATE FOREIGN DATA WRAPPER attrs_fdw NO HANDLER", "CREATE SERVER attrs_server FOREIGN DATA WRAPPER attrs_fdw", "CREATE FOREIGN TABLE attrs_foreign(a integer) SERVER attrs_server"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			q := "SELECT heap_part.tuple_data_split('attrs_seq'::regclass::oid,$1,0,3,NULL) AS attrs"
			heapBoth(t, p, b, c, attrsValue(q, attrsArray("0102030405060708", "1112131415161718", "01"), attrsBytes("0102030405060708111213141516171801")))
			for _, name := range []string{"attrs_view", "attrs_index", "attrs_partition", "attrs_foreign"} {
				q := "SELECT heap_part.tuple_data_split('" + name + "'::regclass::oid,$1,0,99,NULL)"
				heapBoth(t, p, b, c, heapError(q, "0A000", "only heap AM is supported", []byte{}))
			}
		})
	})
}
