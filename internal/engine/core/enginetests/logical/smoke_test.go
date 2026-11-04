package logical

import "testing"

func TestLogicalStreamSmoke(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int PRIMARY KEY, v text)")
	f.slot(s, "smoke")
	f.must(s, "INSERT INTO t VALUES (1, 'a')")
	f.must(s, "DELETE FROM t WHERE id = 1")
	got := f.changes(s, "smoke")
	f.expect(got, concat(tx("INSERT public.t (id[integer]:'1' v[text]:'a')"), tx("DELETE public.t old-key:(id[integer]:'1' v[text]:null)"))...)
	f.expect(f.changes(s, "smoke"))
}
