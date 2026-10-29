package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var connectbySignatures = []string{"text,text,text,text,integer", "text,text,text,text,integer,text", "text,text,text,text,text,integer", "text,text,text,text,text,integer,text"}

func TestReferenceConnectbyInstalledIdentity(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { connectbyRun(t, p, b, c, s) }
		connectbyTree(t, p, b, c)
		catalog := "SELECT p.proargtypes::text AS args,p.prorettype::integer AS result,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.prosrc,l.lanname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='cb_part' AND p.proname='connectby' ORDER BY args"
		run(fixture.Select(catalog, []string{"args", "result", "proisstrict", "provolatile", "proretset", "proparallel", "prosrc", "lanname"}, []string{"TEXT", "INT4", "BOOL", "CHAR", "BOOL", "CHAR", "TEXT", "NAME"}, [][]any{
			{"25 25 25 25 23", int64(2249), true, "s", true, "u", "connectby_text", "c"},
			{"25 25 25 25 23 25", int64(2249), true, "s", true, "u", "connectby_text", "c"},
			{"25 25 25 25 25 23", int64(2249), true, "s", true, "u", "connectby_text_serial", "c"},
			{"25 25 25 25 25 23 25", int64(2249), true, "s", true, "u", "connectby_text_serial", "c"},
		}))
		var count, distinct int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*),count(DISTINCT p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='cb_part' AND p.proname='connectby'").Scan(&count, &distinct); err != nil || count != 4 || distinct != 4 {
			t.Fatal("four distinct installed identities", count, distinct, err)
		}
		run(fixture.Select("SELECT extversion FROM pg_extension WHERE extname='tablefunc'", []string{"extversion"}, []string{"TEXT"}, [][]any{{"1.0"}}))
		run(fixture.Select("SELECT p.pronargdefaults,p.provariadic::integer AS variadic,p.proargnames FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='cb_part' AND p.proname='connectby' ORDER BY p.proargtypes::text", []string{"pronargdefaults", "variadic", "proargnames"}, []string{"INT2", "INT4", "_TEXT"}, [][]any{{int64(0), int64(0), nil}, {int64(0), int64(0), nil}, {int64(0), int64(0), nil}, {int64(0), int64(0), nil}}))
		// The composed native provider retains normal_rand and all four connectby
		// overloads alongside the five single-source crosstab procedures.
		if !b.PG {
			run(fixture.Select("SELECT p.proname,count(*) AS members FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='cb_part' GROUP BY p.proname ORDER BY p.proname", []string{"proname", "members"}, []string{"NAME", "INT8"}, [][]any{{"connectby", int64(4)}, {"crosstab", int64(2)}, {"crosstab2", int64(1)}, {"crosstab3", int64(1)}, {"crosstab4", int64(1)}, {"normal_rand", int64(1)}}))
		}
		run(fixture.Exec("SET search_path TO cb_part,public", 0))
		q := strings.Replace(connectbyQuery(0, connectbyArgs[0]), "cb_part.connectby", "connectby", 1)
		run(connectbySelect(0, q, connectbyLayout(0, connectbyTreeRows), "row2", 0))
		// Each selected member is renamed and relocated separately. In particular,
		// the two six-argument members cannot be dispatched by arity or SQL name.
		run(fixture.Exec("CREATE SCHEMA cb_moved", 0))
		for v, sig := range connectbySignatures {
			old := connectbyQuery(v, connectbyArgs[v])
			held := connectbyHold(t, p, c, old)
			view := fmt.Sprintf("cb_saved_%d", v)
			literalArgs := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(connectbyArgs[v], "$1::text", "'row2'"), "$2::integer", "0"), "$3::text", "'~'")
			run(fixture.Exec("CREATE VIEW "+view+" AS "+connectbyQuery(v, literalArgs), 0))
			run(fixture.Exec(fmt.Sprintf("CREATE VIEW cb_nested_%d AS SELECT * FROM %s", v, view), 0))
			name := fmt.Sprintf("cb_walk_%d", v)
			run(fixture.Exec("ALTER FUNCTION cb_part.connectby("+sig+") RENAME TO "+name, 0))
			run(fixture.Exec("ALTER FUNCTION cb_part."+name+"("+sig+") SET SCHEMA cb_moved", 0))
			args := []any{"row2", 0}
			if v == 1 || v == 3 {
				args = append(args, "~")
			}
			newQuery := strings.Replace(old, "cb_part.connectby", "cb_moved."+name, 1)
			run(connectbySelect(v, newQuery, connectbyLayout(v, connectbyTreeRows), args...))
			// ALTER invalidates the held parse: demand reparses the original SQL
			// name, which no longer resolves. Stored views retain OID identity.
			types := "unknown, unknown, unknown, text, integer"
			if v == 1 {
				types += ", text"
			}
			if v == 2 {
				types = "unknown, unknown, unknown, unknown, text, integer"
			}
			if v == 3 {
				types = "unknown, unknown, unknown, unknown, text, integer, text"
			}
			invalidated := connectbyError(old, "42883", "function cb_part.connectby("+types+") does not exist", "")
			invalidated.Diagnostic.Position = "15"
			invalidated.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			invalidated.Args = args
			connectbyRun(t, p, b, held, invalidated)
			newHeld := connectbyHold(t, p, c, newQuery)
			connectbyRun(t, p, b, newHeld, connectbySelect(v, newQuery, connectbyLayout(v, connectbyTreeRows), args...))
			for _, source := range []string{view, fmt.Sprintf("cb_nested_%d", v)} {
				query := "SELECT * FROM " + source
				if v >= 2 {
					query += " ORDER BY serial"
				}
				run(connectbySelect(v, query, connectbyLayout(v, connectbyTreeRows)))
			}
		}
		// A genuine SQL routine with the old name must not inherit provider dispatch.
		run(fixture.Exec("CREATE FUNCTION cb_part.connectby(text,text,text,text,integer) RETURNS integer LANGUAGE SQL AS 'SELECT 73'", 0))
		run(fixture.Select("SELECT cb_part.connectby('a','b','c','d',0) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(73)}}))
		run(fixture.Exec("DROP EXTENSION tablefunc CASCADE", 0))
		missing := connectbyError("SELECT * FROM cb_moved.cb_walk_0('cb_tree','k','p','row2',0) AS t(node text,parent text,level integer)", "42883", "function cb_moved.cb_walk_0(unknown, unknown, unknown, unknown, integer) does not exist", "")
		missing.Diagnostic.Position = "15"
		missing.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		missing.ErrorStage = ""
		run(missing)
		missing.Prepared = true
		missing.ErrorStage = "prepare"
		run(missing)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION tablefunc SCHEMA cb_moved", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='tablefunc'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION tablefunc SCHEMA cb_moved", 0))
		run(fixture.Exec("COMMIT", 0))
		q = connectbyQuery(0, "'cb_tree','k','p','row2',1")
		q = strings.Replace(q, "cb_part.connectby", "cb_moved.connectby", 1)
		run(connectbySelect(0, q, [][]any{{"row2", nil, int64(0)}, {"row5", "row2", int64(1)}, {"row4", "row2", int64(1)}}))
	})
}

func TestReferenceConnectbyAuthorityAndPrepared(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { connectbyRun(t, p, b, c, s) }
		connectbyTree(t, p, b, c)
		run(fixture.Exec("CREATE USER cb_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public,cb_part TO cb_reader", 0))
		run(fixture.Exec("GRANT SELECT ON cb_tree TO cb_reader", 0))
		q := connectbyQuery(3, connectbyArgs[3])
		held := connectbyHold(t, p, c, q)
		good := connectbySelect(3, q, connectbyTreeRows, "row2", 0, "~")
		actor := func(body func()) { fixture.RoleGrantActor(run, "cb_reader", body) }
		actor(func() { connectbyRun(t, p, b, held, good) })
		run(fixture.Exec("CREATE VIEW cb_authority WITH (security_invoker=true) AS "+connectbyQuery(3, "'cb_tree','k','p','pos','row2',0,'~'"), 0))
		run(fixture.Exec("CREATE VIEW cb_authority_nested WITH (security_invoker=true) AS SELECT * FROM cb_authority", 0))
		run(fixture.Exec("GRANT SELECT ON cb_authority,cb_authority_nested TO cb_reader", 0))
		views := []string{"SELECT * FROM cb_authority ORDER BY serial", "SELECT * FROM cb_authority_nested ORDER BY serial"}
		viewHeld := []fixture.HeldStatement{connectbyHold(t, p, c, views[0]), connectbyHold(t, p, c, views[1])}
		actor(func() {
			for i, v := range views {
				connectbyRun(t, p, b, viewHeld[i], connectbySelect(3, v, connectbyTreeRows))
			}
		})
		sig := "cb_part.connectby(text,text,text,text,text,integer,text)"
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC", 0))
		actor(func() {
			s := connectbyError(q, "42501", "permission denied for function connectby", "")
			s.Args = good.Args
			connectbyRun(t, p, b, held, s)
			s.Args = []any{nil, 0, "~"}
			connectbyRun(t, p, b, held, s)
			for i, v := range views {
				connectbyRun(t, p, b, viewHeld[i], connectbyError(v, "42501", "permission denied for function connectby", ""))
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+sig+" TO cb_reader", 0))
		actor(func() { connectbyRun(t, p, b, held, good) })
		run(fixture.Exec("REVOKE SELECT ON cb_tree FROM cb_reader", 0))
		internal := "SELECT k, p FROM cb_tree WHERE p = 'row2' AND k IS NOT NULL AND k <> p ORDER BY pos"
		actor(func() {
			s := connectbyNestedError(q, internal, "42501", "permission denied for table cb_tree", "")
			s.Args = good.Args
			connectbyRun(t, p, b, held, s)
			connectbyRun(t, p, b, held, connectbySelect(3, q, nil, nil, 0, "~"))
			for i, v := range views {
				s := connectbyNestedError(v, internal, "42501", "permission denied for table cb_tree", "")
				connectbyRun(t, p, b, viewHeld[i], s)
			}
		})
		run(fixture.Exec("GRANT SELECT ON cb_tree TO cb_reader", 0))
		run(fixture.Exec("ALTER TABLE cb_tree ENABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("CREATE TABLE cb_allow(k text)", 0))
		run(fixture.Exec("CREATE TABLE cb_gate(k text)", 0))
		run(fixture.Exec("INSERT INTO cb_allow VALUES('row2'),('row4'),('row6'),('row8'),('private')", 5))
		run(fixture.Exec("INSERT INTO cb_gate VALUES('row2'),('row4'),('row6'),('row8'),('private')", 5))
		run(fixture.Exec("GRANT SELECT ON cb_allow,cb_gate TO cb_reader", 0))
		run(fixture.Exec("ALTER TABLE cb_allow ENABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("CREATE POLICY cb_gate_visible ON cb_allow TO cb_reader USING (EXISTS(SELECT 1 FROM cb_gate WHERE cb_gate.k=cb_allow.k))", 0))
		run(fixture.Exec("CREATE POLICY cb_visible ON cb_tree TO cb_reader USING (EXISTS(SELECT 1 FROM cb_allow WHERE cb_allow.k=cb_tree.k))", 0))
		filtered := [][]any{{"row2", nil, int64(0), "row2", int64(1)}, {"row4", "row2", int64(1), "row2~row4", int64(2)}, {"row6", "row4", int64(2), "row2~row4~row6", int64(3)}, {"row8", "row6", int64(3), "row2~row4~row6~row8", int64(4)}}
		actor(func() {
			connectbyRun(t, p, b, held, connectbySelect(3, q, filtered, "row2", 0, "~"))
			for i, v := range views {
				connectbyRun(t, p, b, viewHeld[i], connectbySelect(3, v, filtered))
			}
		})
		// Private rows participate in traversal immediately and vanish on rollback.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO cb_tree VALUES('private','row8',1)", 1))
		private := append(append([][]any{}, connectbyTreeRows...), []any{"private", "row8", int64(4), "row2~row4~row6~row8~private", int64(7)})
		connectbyRun(t, p, b, held, connectbySelect(3, q, private, "row2", 0, "~"))
		run(fixture.Exec("ROLLBACK", 0))
		connectbyRun(t, p, b, held, good)
		// The outer INSERT captures only its target. Its runtime-named source
		// must capture cb_tree plus the recursively admitted cb_allow/cb_gate
		// closure, retaining both transaction-private inserts and deletions.
		run(fixture.Exec("CREATE TABLE cb_private_target(v integer)", 0))
		run(fixture.Exec("INSERT INTO cb_private_target VALUES(99)", 1))
		run(fixture.Exec("GRANT INSERT,SELECT ON cb_private_target TO cb_reader", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO cb_tree VALUES('private','row6',1)", 1))
		run(fixture.Exec("DELETE FROM cb_tree WHERE k='row8'", 1))
		actor(func() {
			run(fixture.Exec("INSERT INTO cb_private_target SELECT level FROM cb_part.connectby('cb_tree','k','p','row2',0) AS t(node text,parent text,level integer)", 4))
			run(fixture.Select("SELECT v FROM cb_private_target ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}, {int64(3)}, {int64(99)}}))
		})
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT v FROM cb_private_target ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		actor(func() { connectbyRun(t, p, b, held, connectbySelect(3, q, filtered, "row2", 0, "~")) })
		run(fixture.Exec("ALTER FUNCTION "+sig+" OWNER TO cb_reader", 0))
		actor(func() { connectbyRun(t, p, b, held, connectbySelect(3, q, filtered, "row2", 0, "~")) })
		if b.PG {
			// A fast pre-canceled pq command may expose a cursor without proving
			// server admission. Isolate its potentially interrupted transport.
			probe, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			stmt, err := probe.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			canceled := func(err error) {
				t.Helper()
				if err != nil && !errors.Is(err, context.Canceled) {
					// lib/pq can deliver the server's cancellation response even
					// for an already-canceled demand; this proves no admission.
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				}
			}
			rows, err := stmt.QueryContext(ctx, "row2", 0, "~")
			if rows != nil {
				canceled(rows.Close())
				canceled(rows.Err())
			}
			canceled(err)
			return
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := held.Statement.QueryContext(ctx, "row2", 0, "~")
		if rows != nil {
			rows.Close()
			t.Fatal("canceled native demand exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("ordinary canceled native demand", err)
		}
		connectbyRun(t, p, b, held, good)
		connectbyAdmittedCancellation(t, p, b, c)
	})
}
