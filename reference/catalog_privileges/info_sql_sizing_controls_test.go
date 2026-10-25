package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoSQLSizingNativeControls(t *testing.T) {
	szScenario(t, false, func(c *szCase) {
		c.check(c.conn, szQuery, c.want)
		szCatalogShape(c)
		// Reset/Dump/Restore are host APIs, not fictional PostgreSQL commands.
		if c.b.PG {
			return
		}
		c.exec("CREATE TABLE public.sz_user(v integer)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.sz_user VALUES(7)", 1))
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			c.t.Fatal(err)
		}
		if dump.Len() == 0 || strings.Contains(dump.String(), "sql_sizing") {
			c.t.Fatalf("built-in sizing must not be serialized as user DDL/data: %q", dump.String())
		}
		// A native eager result, not an execution handle, remains detached from
		// the catalog replacement. All prepared statements have closed already.
		retained, err := c.conn.QueryContext(c.p.Ctx, szQuery+" ORDER BY sizing_id")
		if err != nil {
			c.t.Fatal(err)
		}
		defer retained.Close()
		columns, err := retained.ColumnTypes()
		if err != nil {
			c.t.Fatal(err)
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			c.t.Fatal(err)
		}
		c.check(c.conn, szQuery, c.want)
		szCatalogShape(c)
		szMetadata(c.t, columns, fixture.SZColumns, szTypes, false)
		szConsume(c, retained, fixture.SZColumns, szTypes, c.want)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			c.t.Fatal(err)
		}
		c.run(c.conn, fixture.Select("SELECT v FROM public.sz_user", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		c.check(c.conn, szQuery, c.want)
		szCatalogShape(c)
		szMetadata(c.t, columns, fixture.SZColumns, szTypes, false)
		q := szQuery + " WHERE sizing_id=$1"
		held := c.hold(q)
		c.run(held, c.projection(q, [][]any{{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil}}, int64(101)))
	})
}

func TestReferenceInfoSQLSizingFeatureSmoke(t *testing.T) {
	szScenario(t, true, func(c *szCase) {
		szPrivateSeed(c)
		selectLimit := int64(0)
		if c.b.PG {
			selectLimit = 1664
		}
		want := [][]any{
			{int64(0), "MAXIMUM DRIVER CONNECTIONS", nil, nil},
			{int64(100), "MAXIMUM COLUMNS IN SELECT", selectLimit, nil},
			{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil},
			{int64(10005), "MAXIMUM IDENTIFIER LENGTH", int64(63), szComment},
		}
		q := szQuery + " WHERE sizing_id IN (0,100,101,10005) ORDER BY sizing_id"
		s := c.projection(q, want)
		s.Bag = false
		c.run(c.conn, s)
		q = szQuery + " WHERE sizing_id=$1"
		held := c.hold(q)
		for _, id := range []any{int64(101), nil, int64(10005)} {
			c.run(held, c.projection(q, szItem(want, id), id))
		}
		c.run(c.conn, fixture.Prepared(c.projection("SELECT * FROM "+szTable+" WHERE sizing_id=0", szItem(want, int64(0)))))
		c.check(c.conn, szQuery+" WHERE FALSE", nil)
		allNull := "SELECT supported_value,comments FROM " + szTable + " WHERE sizing_id=0"
		rows, err := c.conn.QueryContext(c.p.Ctx, allNull)
		if err != nil {
			c.t.Fatal(err)
		}
		szConsume(c, rows, []string{"supported_value", "comments"}, []string{"INT4", "VARCHAR"}, [][]any{{nil, nil}})
		c.exec("SET SESSION AUTHORIZATION sz_reader")
		c.privateDenied()
		c.run(held, c.projection(q, szItem(want, int64(101)), int64(101)))
		c.run(held, c.projection(q, szItem(want, int64(10005)), int64(10005)))
		c.exec("RESET SESSION AUTHORIZATION")
	})
}
