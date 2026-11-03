package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func statioUserNames(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, predicate string, names ...string) {
	t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	s := fixture.Select("SELECT indexrelname FROM pg_statio_user_indexes WHERE "+predicate+" ORDER BY indexrelname", []string{"indexrelname"}, []string{"NAME"}, want)
	vacuumRun(t, p, b, c, s)
	vacuumRun(t, p, b, c, fixture.Prepared(s))
}

// Both SQL names must capture the same provider cut, in either read direction.
// Independent names/identities and counter expectations are asserted by callers.
func statioUserShared(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, predicate string) [][]any {
	t.Helper()
	var result [][]any
	for _, first := range []string{"pg_statio_all_indexes", "pg_statio_user_indexes"} {
		statioExec(t, p, b, c, "SET stats_fetch_consistency='snapshot'", "BEGIN")
		second := "pg_statio_user_indexes"
		if first == second {
			second = "pg_statio_all_indexes"
		}
		a := statioRows(t, p, b, c, "SELECT * FROM "+first+" WHERE "+predicate+" ORDER BY indexrelname")
		z := statioRows(t, p, b, c, "SELECT * FROM "+second+" WHERE "+predicate+" ORDER BY indexrelname")
		if !reflect.DeepEqual(a, z) {
			t.Fatal("named ALL/USER shared cut", first, a, z)
		}
		result = a
		statioExec(t, p, b, c, "ROLLBACK", "SET stats_fetch_consistency='none'")
	}
	return result
}

func TestReferenceStatioUserIndexesMembership(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c,
			"CREATE TABLE statio_user_member(k integer PRIMARY KEY,v integer)",
			"CREATE INDEX statio_user_member_hash ON statio_user_member USING hash(k)",
			"CREATE INDEX statio_user_member_unused ON statio_user_member(v)",
			"CREATE TABLE information_schema.statio_user_system(k integer)",
			"CREATE INDEX statio_user_system_i ON information_schema.statio_user_system(k)",
			"CREATE TABLE statio_user_partition(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE statio_user_leaf PARTITION OF statio_user_partition FOR VALUES FROM(0) TO(10)",
			"CREATE INDEX statio_user_leaf_i ON statio_user_leaf(k)",
			"CREATE MATERIALIZED VIEW statio_user_mat AS SELECT k FROM statio_user_leaf",
			"CREATE INDEX statio_user_mat_i ON statio_user_mat(k)",
			"CREATE VIEW statio_user_plain AS SELECT k FROM statio_user_leaf",
			"CREATE SEQUENCE statio_user_sequence")
		t.Cleanup(func() { copyProgressExec(t, p.Ctx, c, "DROP TABLE information_schema.statio_user_system") })
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO statio_user_member VALUES(1,10),(2,20)", 2))
		statioUserNames(t, p, b, c, "relname='statio_user_member'", "statio_user_member_hash", "statio_user_member_pkey", "statio_user_member_unused")
		statioUserNames(t, p, b, c, "relname IN ('statio_user_leaf','statio_user_mat')", "statio_user_leaf_i", "statio_user_mat_i")
		statioUserNames(t, p, b, c, "relname IN ('statio_user_partition','statio_user_plain','statio_user_sequence','statio_user_system')")
		for _, view := range []string{"pg_statio_all_indexes", "pg_statio_user_indexes"} {
			s := fixture.Select("SELECT s.indexrelname,s.relid=c.oid AS parent_link,s.indexrelid=i.oid AS index_link,s.relid=x.indrelid AS indexed_parent,s.indexrelid=x.indexrelid AS indexed_id FROM "+view+" s JOIN pg_class c ON c.oid=s.relid JOIN pg_class i ON i.oid=s.indexrelid JOIN pg_index x ON x.indexrelid=s.indexrelid WHERE s.relname='statio_user_member' ORDER BY s.indexrelname", []string{"indexrelname", "parent_link", "index_link", "indexed_parent", "indexed_id"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"statio_user_member_hash", true, true, true, true}, {"statio_user_member_pkey", true, true, true, true}, {"statio_user_member_unused", true, true, true, true}})
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		if got := statioUserShared(t, p, b, c, "relname='statio_user_member'"); len(got) != 3 {
			t.Fatal("shared real rows", got)
		}
		statioExec(t, p, b, c, "SET stats_fetch_consistency='snapshot'", "BEGIN")
		predicate := "relname IN ('statio_user_member','statio_user_system')"
		system := "(schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast')"
		all := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE "+predicate+" ORDER BY indexrelname")
		user := statioRows(t, p, b, c, "SELECT * FROM pg_statio_user_indexes WHERE "+predicate+" ORDER BY indexrelname")
		sys := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE "+predicate+" AND "+system+" ORDER BY indexrelname")
		if len(all) != 4 || len(user) != 3 || len(sys) != 1 {
			t.Fatal("exact controlled partition", all, user, sys)
		}
		if !reflect.DeepEqual(user, all[:3]) || !reflect.DeepEqual(sys[0], all[3]) || sys[0][2] != "information_schema" || sys[0][3] != "statio_user_system" || sys[0][4] != "statio_user_system_i" {
			t.Fatal("full seven-column category union", all, user, sys)
		}
		if got := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE relname='statio_user_member' AND "+system); len(got) != 0 {
			t.Fatal("user in system category", got)
		}
		if b.PG {
			actual := statioRows(t, p, b, c, "SELECT * FROM pg_statio_sys_indexes WHERE "+predicate+" ORDER BY indexrelname")
			if !reflect.DeepEqual(actual, sys) {
				t.Fatal("oracle named SYS category", actual, sys)
			}
		} // Native named SYS final cross-view proof is scheduler-staged, not an alias.
		statioExec(t, p, b, c, "ROLLBACK")
	})
}
