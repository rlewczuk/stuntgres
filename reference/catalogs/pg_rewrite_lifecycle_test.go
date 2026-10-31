package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func rewriteOID(t *testing.T, p *fixture.Pair, b fixture.Backend, relation, name string) int64 {
	t.Helper()
	var id int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_rewrite WHERE ev_class='"+relation+"'::regclass AND rulename='"+name+"'").Scan(&id); err != nil || id <= 0 {
		t.Fatal(id, err)
	}
	return id
}
func rewriteEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, relation, name string) []string {
	t.Helper()
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT d.refclassid::text || ':' || d.refobjid::text || ':' || d.refobjsubid::text || ':' || d.deptype::text FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='"+relation+"'::regclass AND r.rulename='"+name+"' ORDER BY d.refclassid,d.refobjid,d.refobjsubid,d.deptype")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bag []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		bag = append(bag, s)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return bag
}
func rewriteEffect(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, n int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, n))
}

func TestReferencePGRewriteLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE other(v INT)", "CREATE TABLE dst(v INT)", "CREATE RULE r AS ON INSERT TO src DO INSERT INTO dst VALUES(NEW.v)", "CREATE RULE r AS ON INSERT TO other DO NOTHING")
			id := rewriteOID(t, p, b, "src", "r")
			if id == rewriteOID(t, p, b, "other", "r") {
				t.Fatal("table-scoped rules share OID")
			}
			before := rewriteTrees(t, p, b, "src", "r")
			const heldRuleQuery = "SELECT rulename,ev_type,ev_enabled,is_instead,ev_qual IS NOT NULL AS qual,ev_action IS NOT NULL AS action FROM pg_rewrite WHERE ev_class='src'::regclass"
			heldRule, err := b.DB.PrepareContext(p.Ctx, heldRuleQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer heldRule.Close()
			rewriteExec(t, p, b, "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v+1)")
			after := rewriteTrees(t, p, b, "src", "r")
			if before[1] == after[1] || id != rewriteOID(t, p, b, "src", "r") {
				t.Fatal("replacement definition/identity", before, after)
			}
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(10)", 1)
			rewriteCount(t, p, b, "SELECT count(*) FROM src", 0)
			rewriteRows(t, p, b, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
			rewriteExec(t, p, b, "ALTER RULE r ON src RENAME TO renamed")
			if after != rewriteTrees(t, p, b, "src", "renamed") || id != rewriteOID(t, p, b, "src", "renamed") {
				t.Fatal("rename changed tree/identity")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: heldRule, SQL: heldRuleQuery}, b.Recorder, fixture.Select(heldRuleQuery, []string{"rulename", "ev_type", "ev_enabled", "is_instead", "qual", "action"}, []string{"NAME", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"renamed", "3", "O", true, true, true}}))
			edges := rewriteEdges(t, p, b, "src", "renamed")
			modeConn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer modeConn.Close()
			defer func() {
				if _, err := modeConn.ExecContext(context.Background(), "SET session_replication_role=origin"); err != nil {
					t.Error(err)
				}
			}()
			modeRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, modeConn, b.Recorder, s) }
			for _, mode := range []struct{ action, code string }{{"DISABLE", "D"}, {"ENABLE REPLICA", "R"}, {"ENABLE ALWAYS", "A"}, {"ENABLE", "O"}} {
				rewriteExec(t, p, b, "ALTER TABLE src "+mode.action+" RULE renamed")
				if after != rewriteTrees(t, p, b, "src", "renamed") || id != rewriteOID(t, p, b, "src", "renamed") || !reflect.DeepEqual(edges, rewriteEdges(t, p, b, "src", "renamed")) {
					t.Fatal("mode changed definition/identity/dependencies")
				}
				rewriteRows(t, p, b, fixture.Select("SELECT ev_enabled FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='renamed'", []string{"ev_enabled"}, []string{"CHAR"}, [][]any{{mode.code}}))
				if mode.code == "D" {
					previous := after
					rewriteExec(t, p, b, "CREATE OR REPLACE RULE renamed AS ON INSERT TO src DO INSTEAD INSERT INTO dst VALUES(NEW.v+2)")
					after = rewriteTrees(t, p, b, "src", "renamed")
					if after[1] == previous[1] || id != rewriteOID(t, p, b, "src", "renamed") {
						t.Fatal("disabled replacement definition/identity")
					}
					rewriteRows(t, p, b, fixture.Select("SELECT ev_enabled FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='renamed'", []string{"ev_enabled"}, []string{"CHAR"}, [][]any{{"D"}}))
				}
				for _, role := range []string{"origin", "local", "replica"} {
					modeRun(fixture.Exec("SET session_replication_role="+role, 0))
					modeRun(fixture.Exec("TRUNCATE src,dst", 0))
					active := mode.code == "A" || mode.code == "O" && role != "replica" || mode.code == "R" && role == "replica"
					step := fixture.Exec("INSERT INTO src VALUES(20)", 1)
					if role == "local" {
						step = fixture.Prepared(step)
					}
					modeRun(step)
					source, sink := int64(1), int64(0)
					if active {
						source, sink = 0, 1
					}
					modeRun(fixture.Select("SELECT count(*) FROM src", []string{"count"}, []string{"INT8"}, [][]any{{source}}))
					modeRun(fixture.Select("SELECT count(*) FROM dst", []string{"count"}, []string{"INT8"}, [][]any{{sink}}))
				}
			}
			modeRun(fixture.Exec("SET session_replication_role=origin", 0))
			rewriteExec(t, p, b, "CREATE TABLE base(id INT)")
			rewriteEffect(t, p, b, "INSERT INTO base VALUES(1)", 1)
			rewriteExec(t, p, b, "CREATE VIEW v AS SELECT id FROM base")
			rewriteEffect(t, p, b, "CREATE MATERIALIZED VIEW mv AS SELECT id FROM base", 1)
			viewID := rewriteOID(t, p, b, "v", "_RETURN")
			viewTree := rewriteTrees(t, p, b, "v", "_RETURN")
			stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT id FROM v")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rewriteExec(t, p, b, `CREATE OR REPLACE RULE "_RETURN" AS ON SELECT TO v DO INSTEAD SELECT id+10 AS id FROM base`)
			if viewID != rewriteOID(t, p, b, "v", "_RETURN") || viewTree[1] == rewriteTrees(t, p, b, "v", "_RETURN")[1] {
				t.Fatal("view replacement")
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: "SELECT id FROM v"}, b.Recorder, fixture.Select("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
			rewriteEffect(t, p, b, "UPDATE base SET id=2", 1)
			rewriteRows(t, p, b, fixture.Select("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			rewriteExec(t, p, b, "REFRESH MATERIALIZED VIEW mv")
			rewriteRows(t, p, b, fixture.Select("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			old := rewriteTrees(t, p, b, "src", "renamed")
			oldEdges := rewriteEdges(t, p, b, "src", "renamed")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("CREATE OR REPLACE RULE renamed AS ON INSERT TO src DO INSERT INTO missing_sink VALUES(NEW.v)", fixture.Diagnostic{Code: "42P01", Message: `relation "missing_sink" does not exist`, Position: "67"}))
			if old != rewriteTrees(t, p, b, "src", "renamed") || !reflect.DeepEqual(oldEdges, rewriteEdges(t, p, b, "src", "renamed")) {
				t.Fatal("failed replacement published")
			}
		})
	}
}

func TestReferencePGRewriteDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE other(v INT)", "CREATE TABLE sink1(v INT)", "CREATE TABLE sink2(v INT)", "CREATE RULE r AS ON INSERT TO src DO INSERT INTO sink1 VALUES(NEW.v)", "CREATE RULE r AS ON INSERT TO other DO NOTHING")
			id := rewriteOID(t, p, b, "src", "r")
			before := rewriteTrees(t, p, b, "src", "r")
			checkEdge := func(target, kind string, n int64) {
				rewriteCount(t, p, b, "SELECT count(*) FROM (SELECT DISTINCT d.refobjid FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='src'::regclass AND r.rulename='r' AND d.refobjid='"+target+"'::regclass AND d.deptype='"+kind+"') edges", n)
			}
			checkEdge("src", "a", 1)
			checkEdge("sink1", "n", 1)
			rewriteExec(t, p, b, "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSERT INTO sink2 VALUES(NEW.v)")
			checkEdge("sink1", "n", 0)
			checkEdge("sink2", "n", 1)
			after := rewriteTrees(t, p, b, "src", "r")
			if before == after || id != rewriteOID(t, p, b, "src", "r") {
				t.Fatal("dependency replacement")
			}
			rewriteExec(t, p, b, "DROP TABLE sink1 RESTRICT")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("DROP TABLE sink2 RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table sink2 because other objects depend on it", Detail: "rule r on table src depends on table sink2", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			if after != rewriteTrees(t, p, b, "src", "r") {
				t.Fatal("RESTRICT changed definition")
			}
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(9)", 1)
			rewriteRows(t, p, b, fixture.Select("SELECT v FROM sink2", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
			// Savepoint metadata is private and rollback restores exact owner-derived trees.
			originalEdges := rewriteEdges(t, p, b, "src", "r")
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"BEGIN", "SAVEPOINT original", "CREATE OR REPLACE RULE r AS ON INSERT TO src DO NOTHING", "DROP RULE r ON src"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			if after != rewriteTrees(t, p, b, "src", "r") {
				t.Fatal("uncommitted metadata leaked")
			}
			if _, err := c.ExecContext(p.Ctx, "ROLLBACK TO original"); err != nil {
				t.Fatal(err)
			}
			rows, err := c.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'")
			if err != nil {
				t.Fatal(err)
			}
			if after != rewriteTreeRows(t, b, rows) {
				t.Fatal("savepoint tree restoration")
			}
			if _, err := c.ExecContext(p.Ctx, "COMMIT"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(originalEdges, rewriteEdges(t, p, b, "src", "r")) || id != rewriteOID(t, p, b, "src", "r") {
				t.Fatal("rollback changed dependencies/identity")
			}
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(8)", 1)
			rewriteCount(t, p, b, "SELECT count(*) FROM sink2", 2)
			rewriteExec(t, p, b, "DROP TABLE sink2 CASCADE")
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+fmtRewriteOID(id), 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend WHERE classid='pg_rewrite'::regclass AND objid="+fmtRewriteOID(id), 0)
			rewriteEffect(t, p, b, "INSERT INTO src VALUES(10)", 1)
			rewriteExec(t, p, b, "CREATE RULE local_rule AS ON DELETE TO src DO NOTHING")
			localID := rewriteOID(t, p, b, "src", "local_rule")
			rewriteExec(t, p, b, "DROP TABLE src")
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid="+fmtRewriteOID(localID), 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend WHERE classid='pg_rewrite'::regclass AND objid="+fmtRewriteOID(localID), 0)
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE ev_class='other'::regclass", 1)
			rewriteExec(t, p, b, "CREATE TABLE base(id INT,v INT)", "CREATE VIEW v AS SELECT id FROM base", "CREATE MATERIALIZED VIEW mv AS SELECT id FROM base")
			for _, name := range []string{"v", "mv"} {
				rewriteCount(t, p, b, "SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid='pg_rewrite'::regclass AND d.refobjid='"+name+"'::regclass AND d.deptype='i'", 1)
				rewriteCount(t, p, b, "SELECT count(*) FROM (SELECT DISTINCT d.refobjid FROM pg_depend d JOIN pg_rewrite r ON d.objid=r.oid WHERE d.classid='pg_rewrite'::regclass AND r.ev_class='"+name+"'::regclass AND d.refobjid='base'::regclass AND d.deptype='n') edges", 1)
			}
			viewID := rewriteOID(t, p, b, "v", "_RETURN")
			mvID := rewriteOID(t, p, b, "mv", "_RETURN")
			rewriteExec(t, p, b, "DROP VIEW v")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("DROP TABLE base RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table base because other objects depend on it", Detail: "materialized view mv depends on table base", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
			rewriteExec(t, p, b, "DROP TABLE base CASCADE")
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE oid IN ("+fmtRewriteOID(viewID)+","+fmtRewriteOID(mvID)+")", 0)
			// Condition-only reference and referenced-column cascade use the real dependency owner.
			rewriteExec(t, p, b, "CREATE TABLE gate(v INT)", "CREATE TABLE event(v INT)", "CREATE RULE conditional AS ON INSERT TO event WHERE EXISTS(SELECT v FROM gate WHERE gate.v=NEW.v) DO NOTHING")
			rewriteExec(t, p, b, "ALTER TABLE gate DROP COLUMN v CASCADE")
			rewriteCount(t, p, b, "SELECT count(*) FROM pg_rewrite WHERE ev_class='event'::regclass", 0)
		})
	}
}

// An integer format helper, not an allocator-sequence expectation.
func fmtRewriteOID(id int64) string { return strconv.FormatInt(id, 10) }

func TestReferencePGRewriteCapture(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			rewriteExec(t, p, b, "CREATE TABLE src(v INT)", "CREATE TABLE dst(v INT)", "CREATE RULE r AS ON INSERT TO src DO INSERT INTO dst VALUES(NEW.v)", "CREATE VIEW v AS SELECT v FROM dst")
			const captureQuery = "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='src'::regclass AND rulename='r'"
			stmt, err := b.DB.PrepareContext(p.Ctx, captureQuery)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = b.DB.QueryContext(ctx, captureQuery)
				}
				if rows != nil {
					rows.Close()
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("capture cancellation", err)
				}
			}
			var retained [2]*sql.Rows
			var want [2][2]string
			for i, relation := range []string{"src", "v"} {
				name := "r"
				if i == 1 {
					name = "_RETURN"
				}
				want[i] = rewriteTrees(t, p, b, relation, name)
				var err error
				retained[i], err = b.DB.QueryContext(p.Ctx, "SELECT ev_qual,ev_action FROM pg_rewrite WHERE ev_class='"+relation+"'::regclass AND rulename='"+name+"'")
				if err != nil {
					t.Fatal(err)
				}
				defer retained[i].Close()
			}
			if !b.PG {
				for _, rows := range retained {
					columns, err := rows.Columns()
					if err != nil || len(columns) != 2 {
						t.Fatal(columns, err)
					}
					columns[0] = "caller_mutation"
					fresh, err := rows.Columns()
					if err != nil || fresh[0] != "ev_qual" {
						t.Fatal("exported columns alias retained source", fresh, err)
					}
				}
			}
			rewriteExec(t, p, b, "CREATE OR REPLACE RULE r AS ON INSERT TO src DO INSERT INTO dst VALUES(NEW.v+2)", "ALTER RULE r ON src RENAME TO renamed", "ALTER TABLE src DISABLE RULE renamed", "CREATE OR REPLACE VIEW v AS SELECT v+1 AS v FROM dst")
			if want[0] == rewriteTrees(t, p, b, "src", "renamed") || want[1] == rewriteTrees(t, p, b, "v", "_RETURN") {
				t.Fatal("replacement tree unchanged")
			}
			rewriteExec(t, p, b, "DROP TABLE dst CASCADE", "DROP TABLE src")
			for i, rows := range retained {
				if got := rewriteTreeRows(t, b, rows); got != want[i] {
					t.Fatal("retained predecessor changed", got, want[i])
				}
			}
		})
	}
}
