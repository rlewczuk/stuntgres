package reference_test

import "testing"

func TestReferenceInfoSQLSizingRows(t *testing.T) {
	t.Run("complete ordered and bag", func(t *testing.T) {
		szScenario(t, false, func(c *szCase) {
			c.exec("SET search_path=public")
			q := szQuery + " ORDER BY sizing_id"
			held := c.hold(q)
			s := c.projection(q, c.want)
			s.Bag = false
			c.run(c.conn, s)
			s.Prepared = true
			c.run(c.conn, s)
			s.Prepared = false
			c.run(held, s)
			c.check(c.conn, szQuery, c.want)
			c.exec("CREATE TABLE public.sz_unrelated(id integer)")
			c.run(held, s)
			c.exec("DROP TABLE public.sz_unrelated")
			c.run(held, s)
		})
	})
	t.Run("held nullable parameter", func(t *testing.T) {
		szScenario(t, false, func(c *szCase) {
			q := szQuery + " WHERE sizing_id=$1"
			held := c.hold(q)
			for _, id := range []any{int64(0), int64(100), int64(101), int64(10005), nil, int64(-1), int64(0)} {
				want := szItem(c.want, id)
				c.check(held, q, want, id)
				c.run(c.conn, c.projection(q, want, id))
			}
		})
	})
	for _, predicate := range []string{"FALSE", "sizing_id IS NULL", "sizing_id=NULL", "sizing_id=-1", "comments=''", "supported_value IS NULL", "supported_value=0", "comments IS NULL", "comments IS NOT NULL"} {
		t.Run(predicate, func(t *testing.T) {
			szScenario(t, false, func(c *szCase) {
				var want [][]any
				switch predicate {
				case "supported_value IS NULL":
					want = szFilter(c.want, func(r []any) bool { return r[2] == nil })
				case "supported_value=0":
					want = szFilter(c.want, func(r []any) bool { return r[2] == int64(0) })
				case "comments IS NULL":
					want = szFilter(c.want, func(r []any) bool { return r[3] == nil })
				case "comments IS NOT NULL":
					want = szFilter(c.want, func(r []any) bool { return r[3] != nil })
				}
				q := szQuery + " WHERE " + predicate
				c.check(c.conn, q, want)
				c.run(c.hold(q), c.projection(q, want))
			})
		})
	}
}
