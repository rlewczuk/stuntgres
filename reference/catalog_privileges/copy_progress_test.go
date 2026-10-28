package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCopyProgressShape(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		for _, q := range []string{"SELECT * FROM pg_stat_progress_copy", "SELECT * FROM pg_catalog.pg_stat_progress_copy"} {
			s := fixture.Select(q, copyProgressNames, copyProgressTypes, nil)
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		var attrs [][]any
		for i, name := range copyProgressNames {
			oid := []int64{23, 26, 19, 26, 25, 25, 20, 20, 20, 20, 20}[i]
			attrs = append(attrs, []any{name, int64(i + 1), oid, false, false})
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT attname,attnum,atttypid::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_progress_copy'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "BOOL", "BOOL"}, attrs))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT relkind,pg_get_userbyid(relowner) AS owner,relhasindex FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_copy'::regclass", []string{"relkind", "owner", "relhasindex"}, []string{"CHAR", "NAME", "BOOL"}, [][]any{{"v", fixture.PGTableViewsOwner(b), false}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_progress_copy'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_stat_progress_copy", "c", true, true, true, true}}),
		} {
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		for _, tc := range []struct{ pretty, expected string }{
			{"false", copyProgressDefaultDefinition},
			{"true", copyProgressPrettyDefinition},
		} {
			step := fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_stat_progress_copy'::regclass,"+tc.pretty+") AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.expected}})
			vacuumRun(t, p, b, c, step)
			vacuumRun(t, p, b, c, fixture.Prepared(step))
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT rulename,ev_type,is_instead FROM pg_rewrite WHERE ev_class='pg_catalog.pg_stat_progress_copy'::regclass", []string{"rulename", "ev_type", "is_instead"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", true}}))
		// REL_18_6 owns _RETURN internally; its backing pg_database/progress
		// routine references are bootstrap-pinned, not additional dependency rows.
		dependencies := fixture.Select("SELECT d.classid='pg_catalog.pg_rewrite'::regclass AS rule_class,d.objsubid,d.refclassid='pg_catalog.pg_class'::regclass AS relation_class,d.refobjid='pg_catalog.pg_stat_progress_copy'::regclass AS view_owner,d.refobjsubid,d.deptype FROM pg_depend d JOIN pg_rewrite r ON d.classid='pg_catalog.pg_rewrite'::regclass AND d.objid=r.oid WHERE r.ev_class='pg_catalog.pg_stat_progress_copy'::regclass AND r.rulename='_RETURN' ORDER BY d.classid,d.objsubid,d.refclassid,d.refobjid,d.refobjsubid,d.deptype", []string{"rule_class", "objsubid", "relation_class", "view_owner", "refobjsubid", "deptype"}, []string{"BOOL", "INT4", "BOOL", "BOOL", "INT4", "CHAR"}, [][]any{{true, int64(0), true, true, int64(0), "i"}})
		vacuumRun(t, p, b, c, dependencies)
		vacuumRun(t, p, b, c, fixture.Prepared(dependencies))
	})
}

// Independently recorded from the pinned PostgreSQL 18.6 oracle, with released
// projection and parameter mappings checked against system_views.sql.
const copyProgressDefaultDefinition = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
        CASE s.param5
            WHEN 1 THEN 'COPY FROM'::text
            WHEN 2 THEN 'COPY TO'::text
            ELSE NULL::text
        END AS command,
        CASE s.param6
            WHEN 1 THEN 'FILE'::text
            WHEN 2 THEN 'PROGRAM'::text
            WHEN 3 THEN 'PIPE'::text
            WHEN 4 THEN 'CALLBACK'::text
            ELSE NULL::text
        END AS type,
    s.param1 AS bytes_processed,
    s.param2 AS bytes_total,
    s.param3 AS tuples_processed,
    s.param4 AS tuples_excluded,
    s.param7 AS tuples_skipped
   FROM (pg_stat_get_progress_info('COPY'::text) s(pid, datid, relid, param1, param2, param3, param4, param5, param6, param7, param8, param9, param10, param11, param12, param13, param14, param15, param16, param17, param18, param19, param20)
     LEFT JOIN pg_database d ON ((s.datid = d.oid)));`

const copyProgressPrettyDefinition = ` SELECT s.pid,
    s.datid,
    d.datname,
    s.relid,
        CASE s.param5
            WHEN 1 THEN 'COPY FROM'::text
            WHEN 2 THEN 'COPY TO'::text
            ELSE NULL::text
        END AS command,
        CASE s.param6
            WHEN 1 THEN 'FILE'::text
            WHEN 2 THEN 'PROGRAM'::text
            WHEN 3 THEN 'PIPE'::text
            WHEN 4 THEN 'CALLBACK'::text
            ELSE NULL::text
        END AS type,
    s.param1 AS bytes_processed,
    s.param2 AS bytes_total,
    s.param3 AS tuples_processed,
    s.param4 AS tuples_excluded,
    s.param7 AS tuples_skipped
   FROM pg_stat_get_progress_info('COPY'::text) s(pid, datid, relid, param1, param2, param3, param4, param5, param6, param7, param8, param9, param10, param11, param12, param13, param14, param15, param16, param17, param18, param19, param20)
     LEFT JOIN pg_database d ON s.datid = d.oid;`

func TestReferenceCopyProgressFrom(t *testing.T) {
	for _, key := range []string{" PRIMARY KEY", ""} {
		t.Run("key="+key, func(t *testing.T) {
			copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				observer := vacuumConn(t, p, b)
				copyProgressExec(t, p.Ctx, observer, "CREATE TABLE copy_progress_from(id INT"+key+")")
				copyProgressExec(t, p.Ctx, observer, "INSERT INTO copy_progress_from VALUES(99)")
				worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
				id := copyProgressIdentify(t, p, worker, "copy_progress_from")
				run := copyProgressStart(t, p, b, worker, "COPY copy_progress_from FROM STDIN")
				initial := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
				copyProgressVisible(t, initial, id, "COPY FROM")
				var supplied int64
				previous := initial
				for segment := range 2 {
					payload := copyProgressIntegers(1000+segment*1000, 1000)
					supplied += int64(len(payload))
					run.input.send(t, p, payload)
					s := copyProgressAwait(t, p, observer, id.pid, func(s copyProgressSnapshot, ok bool) bool {
						return ok && s.processed.Valid && s.processed.Int64 == int64((segment+1)*1000)
					})
					copyProgressVisible(t, s, id, "COPY FROM")
					if s.bytes.Int64 < previous.bytes.Int64 || s.bytes.Int64 > supplied || s.processed.Int64 <= previous.processed.Int64 || s.excluded.Int64 != 0 || s.skipped.Int64 != 0 {
						t.Fatalf("nonmonotonic or misclassified previous=%+v current=%+v supplied=%d", previous, s, supplied)
					}
					previous = s
				}
				run.finish(t, 2000)
				copyProgressIdle(t, p, observer, id.pid)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT count(*) AS n FROM copy_progress_from", []string{"n"}, []string{"INT8"}, [][]any{{int64(2001)}}))
			})
		})
	}
}

func TestReferenceCopyProgressReadOnly(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		copyProgressExec(t, p.Ctx, c, "CREATE TABLE copy_progress_sentinel(id INT)")
		copyProgressExec(t, p.Ctx, c, "INSERT INTO copy_progress_sentinel VALUES(99)")
		worker := copyProgressConn(t, p, fixture.CopyScenarioPool(t, p, b.PG, "", ""))
		id := copyProgressIdentify(t, p, worker, "copy_progress_sentinel")
		run := copyProgressStart(t, p, b, worker, "COPY copy_progress_sentinel FROM STDIN")
		copyProgressAwait(t, p, c, id.pid, func(s copyProgressSnapshot, ok bool) bool { return ok })
		for _, tc := range []struct{ q, verb, action, gerund string }{
			{"INSERT INTO pg_stat_progress_copy(pid) VALUES(1)", "insert into", "INSERT", "inserting into"},
			{"INSERT INTO pg_stat_progress_copy(pid) SELECT pid FROM pg_stat_progress_copy", "insert into", "INSERT", "inserting into"},
			{"UPDATE pg_stat_progress_copy SET command='x' WHERE false", "update", "UPDATE", "updating"},
			{"DELETE FROM pg_stat_progress_copy WHERE false", "delete from", "DELETE", "deleting from"},
			{"TRUNCATE pg_stat_progress_copy", "", "", ""},
		} {
			want := fixture.Diagnostic{Code: "55000", Message: "cannot " + tc.verb + " view \"pg_stat_progress_copy\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule."}
			if tc.action == "" {
				want = fixture.Diagnostic{Code: "42809", Message: "\"pg_stat_progress_copy\" is not a table"}
			}
			for _, qualified := range []bool{false, true} {
				q := tc.q
				if qualified {
					q = replaceCopyProgressName(q)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.action != "" {
						s.ErrorStage = "prepare"
					}
					vacuumRun(t, p, b, c, s)
					snapshot, ok := copyProgressRead(t, p, c, id.pid)
					if !ok {
						t.Fatal("observer mutation ended real COPY")
					}
					copyProgressVisible(t, snapshot, id, "COPY FROM")
					vacuumRun(t, p, b, c, fixture.Select("SELECT id FROM copy_progress_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
				}
			}
		}
		run.finish(t, 0)
		copyProgressIdle(t, p, c, id.pid)
	})
}
