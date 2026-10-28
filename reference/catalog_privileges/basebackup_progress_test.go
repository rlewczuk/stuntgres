package reference_test

import (
	"database/sql"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func basebackupEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"SELECT * FROM pg_stat_progress_basebackup", "SELECT * FROM pg_catalog.pg_stat_progress_basebackup", "SELECT count(*) AS n FROM pg_stat_progress_basebackup"} {
		s := fixture.Select(q, []string{"pid", "phase", "backup_total", "backup_streamed", "tablespaces_total", "tablespaces_streamed"}, []string{"INT4", "TEXT", "INT8", "INT8", "INT8", "INT8"}, nil)
		if strings.Contains(q, "count(*)") {
			s = fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		}
		vacuumRun(t, p, b, c, s)
		vacuumRun(t, p, b, c, fixture.Prepared(s))
	}
}

func TestReferenceBasebackupProgress(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"basebackup_observer"})
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		basebackupEmpty(t, p, b, c)
		vacuumRun(t, p, b, c, fixture.Select("SELECT relkind FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_basebackup'::regclass", []string{"relkind"}, []string{"CHAR"}, [][]any{{"v"}}))
		var attributes [][]any
		for i, name := range []string{"pid", "phase", "backup_total", "backup_streamed", "tablespaces_total", "tablespaces_streamed"} {
			typ := int64(20)
			if i == 0 {
				typ = 23
			}
			if i == 1 {
				typ = 25
			}
			attributes = append(attributes, []any{name, int64(i + 1), typ, false, false})
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT attname,attnum,atttypid::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_progress_basebackup'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "BOOL", "BOOL"}, attributes))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT pg_get_userbyid(relowner) AS owner,relhasindex FROM pg_class WHERE oid='pg_catalog.pg_stat_progress_basebackup'::regclass", []string{"owner", "relhasindex"}, []string{"NAME", "BOOL"}, [][]any{{fixture.PGTableViewsOwner(b), false}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_progress_basebackup'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_stat_progress_basebackup", "c", true, true, true, true}}),
		} {
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
		for _, q := range []string{"CREATE ROLE basebackup_observer", "SET ROLE basebackup_observer"} {
			vacuumRun(t, p, b, c, fixture.Exec(q, 0))
		}
		basebackupEmpty(t, p, b, c)
		for _, q := range []string{"RESET ROLE", "GRANT pg_read_all_stats TO basebackup_observer", "SET ROLE basebackup_observer"} {
			vacuumRun(t, p, b, c, fixture.Exec(q, 0))
		}
		basebackupEmpty(t, p, b, c)
		vacuumRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
	})
}

func basebackupSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	basebackupEmpty(t, p, b, c)
	vacuumRun(t, p, b, c, fixture.Select("SELECT id FROM basebackup_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func basebackupSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	vacuumRun(t, p, b, c, fixture.Exec("CREATE TABLE basebackup_sentinel(id INT)", 0))
	vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO basebackup_sentinel VALUES(7)", 1))
}

func TestReferenceBasebackupReadOnly(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		basebackupSeed(t, p, b, c)
		for _, tc := range []struct{ sql, verb, action, gerund string }{
			{"INSERT INTO pg_stat_progress_basebackup(pid) VALUES(1)", "insert into", "INSERT", "inserting into"},
			{"INSERT INTO pg_stat_progress_basebackup(pid) SELECT pid FROM pg_stat_progress_basebackup", "insert into", "INSERT", "inserting into"},
			{"UPDATE pg_stat_progress_basebackup SET phase='x' WHERE false", "update", "UPDATE", "updating"},
			{"DELETE FROM pg_stat_progress_basebackup WHERE false", "delete from", "DELETE", "deleting from"},
			{"TRUNCATE pg_stat_progress_basebackup", "", "", ""},
		} {
			want := fixture.Diagnostic{
				Code:    "55000",
				Message: "cannot " + tc.verb + " view \"pg_stat_progress_basebackup\"",
				Detail:  "Views that do not select from a single table or view are not automatically updatable.",
				Hint:    "To enable " + tc.gerund + " the view, provide an INSTEAD OF " + tc.action + " trigger or an unconditional ON " + tc.action + " DO INSTEAD rule.",
			}
			if tc.action == "" {
				want = fixture.Diagnostic{Code: "42809", Message: `"pg_stat_progress_basebackup" is not a table`}
			}
			for _, q := range []string{tc.sql, strings.ReplaceAll(tc.sql, "pg_stat_progress_basebackup", "pg_catalog.pg_stat_progress_basebackup")} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && tc.action != "" {
						s.ErrorStage = "prepare"
					}
					vacuumRun(t, p, b, c, s)
					basebackupSentinel(t, p, b, c)
				}
			}
		}
	})
}

func TestReferenceBasebackupBoundary(t *testing.T) {
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		basebackupSeed(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("BASE_BACKUP", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "BASE_BACKUP"`, Position: "1"})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			vacuumRun(t, p, b, c, s)
			basebackupSentinel(t, p, b, c)
		}
		if b.PG {
			return
		}
		// Dump borrows its own physical connection; c remains a separate observer.
		w := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
		done := make(chan error, 1)
		var release sync.Once
		joined := false
		defer func() {
			release.Do(func() { close(w.Release) })
			if !joined {
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
		}()
		go func() { done <- stuntgres.Dump(p.Ctx, p.Native, w) }()
		select {
		case <-w.Started:
		case err := <-done:
			joined = true
			t.Fatalf("Dump ended before writer gate: %v", err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		basebackupSentinel(t, p, b, c)
		release.Do(func() { close(w.Release) })
		err := <-done
		joined = true
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(w.Output.String(), `CREATE TABLE "public"."basebackup_sentinel"`) || !strings.Contains(w.Output.String(), `INSERT INTO "public"."basebackup_sentinel" VALUES (7);`) {
			t.Fatal("Dump omitted real sentinel data", w.Output.String())
		}
		basebackupSentinel(t, p, b, c)
	})
}
