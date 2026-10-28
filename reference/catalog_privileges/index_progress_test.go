package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateIndexProgressShape(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		for _, name := range []string{"pg_stat_progress_create_index", "pg_catalog.pg_stat_progress_create_index"} {
			s := fixture.Select("SELECT * FROM "+name, indexProgressNames, indexProgressTypes, nil)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		var attrs [][]any
		for i, name := range indexProgressNames {
			oid := int64(20)
			if i < 7 {
				oid = []int64{23, 26, 19, 26, 26, 25, 25}[i]
			}
			attrs = append(attrs, []any{name, int64(i + 1), oid, false, false})
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT attname,attnum,atttypid::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_progress_create_index'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "BOOL", "BOOL"}, attrs))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT relkind,pg_get_userbyid(relowner) AS owner,relhasindex FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_create_index'::regclass", []string{"relkind", "owner", "relhasindex"}, []string{"CHAR", "NAME", "BOOL"}, [][]any{{"v", fixture.PGTableViewsOwner(b), false}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_progress_create_index'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_stat_progress_create_index", "c", true, true, true, true}}),
		} {
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}

func TestReferenceCreateIndexProgressReadOnly(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c, holder, worker := vacuumConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		copyProgressExec(t, p.Ctx, c, "CREATE TABLE index_progress_readonly(n INT)")
		copyProgressExec(t, p.Ctx, c, "INSERT INTO index_progress_readonly VALUES(99)")
		copyProgressExec(t, p.Ctx, holder, "BEGIN")
		copyProgressExec(t, p.Ctx, holder, "LOCK TABLE index_progress_readonly IN ROW EXCLUSIVE MODE")
		id := copyProgressIdentify(t, p, worker, "index_progress_readonly")
		run := fixture.ConcurrentIndexStart(t, p, worker, "CREATE INDEX CONCURRENTLY index_progress_readonly_ix ON index_progress_readonly(n)", false)
		indexProgressAwait(t, p, c, id.pid, "waiting for writers before build", 0)
		for _, tc := range []struct{ q, verb, action, gerund string }{
			{"INSERT INTO pg_stat_progress_create_index(pid) VALUES(1)", "insert into", "INSERT", "inserting into"},
			{"INSERT INTO pg_stat_progress_create_index(pid) SELECT pid FROM pg_stat_progress_create_index", "insert into", "INSERT", "inserting into"},
			{"UPDATE pg_stat_progress_create_index SET command='x' WHERE false", "update", "UPDATE", "updating"},
			{"DELETE FROM pg_stat_progress_create_index WHERE false", "delete from", "DELETE", "deleting from"},
			{"TRUNCATE pg_stat_progress_create_index", "", "", ""},
		} {
			want := fixture.Diagnostic{Code: "55000", Message: "cannot " + tc.verb + " view \"pg_stat_progress_create_index\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."}
			if tc.action == "" {
				want = fixture.Diagnostic{Code: "42809", Message: "\"pg_stat_progress_create_index\" is not a table"}
			}
			for _, qualified := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					q := tc.q
					if qualified {
						q = strings.ReplaceAll(q, "pg_stat_progress_create_index", "pg_catalog.pg_stat_progress_create_index")
					}
					s := fixture.ImplicitError(q, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.action != "" {
						s.ErrorStage = "prepare"
					}
					vacuumRun(t, p, b, c, s)
					snapshot := indexProgressAwait(t, p, c, id.pid, "waiting for writers before build", 0)
					indexProgressVisible(t, snapshot, id, "CREATE INDEX CONCURRENTLY")
					vacuumRun(t, p, b, c, fixture.Select("SELECT n FROM index_progress_readonly", []string{"n"}, []string{"INT4"}, [][]any{{int64(99)}}))
				}
			}
		}
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		if err := run.Join(t, p); err != nil {
			t.Fatal(err)
		}
		indexProgressIdle(t, p, c, id.pid)
	})
}
