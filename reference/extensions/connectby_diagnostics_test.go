package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConnectbyRawSQLAndQuoting(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { connectbyRun(t, p, b, c, s) }
		connectbyTree(t, p, b, c)
		run(fixture.Exec(`CREATE SCHEMA "CB Schema"`, 0))
		run(fixture.Exec(`CREATE TABLE "CB Schema"."Tree!"("Key" text,"Parent Key" text,"Order" integer)`, 0))
		root := "O'Reilly\\雪"
		child := "child'\\λ"
		run(fixture.Exec(`INSERT INTO "CB Schema"."Tree!" VALUES($1,NULL,7),($2,$1,2)`, 2, root, child))
		q := connectbyQuery(3, `'"CB Schema"."Tree!"','"Key"','"Parent Key"','"Order"',$1::text,0,'::'`)
		for _, setting := range []string{"on", "off"} {
			run(fixture.Exec("SET standard_conforming_strings TO "+setting, 0))
			s := connectbySelect(3, q, [][]any{{root, nil, int64(0), root, int64(1)}, {child, root, int64(1), root + "::" + child, int64(2)}}, root)
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("SET standard_conforming_strings TO on", 0))
		q = connectbyQuery(0, "'cb_tree','k::text','p','row2',1")
		run(connectbySelect(0, q, [][]any{{"row2", nil, int64(0)}, {"row5", "row2", int64(1)}, {"row4", "row2", int64(1)}}))
		q = connectbyQuery(0, "'cb_tree','1; --','p','1',1")
		run(connectbyError(q, "22023", "invalid connectby source data query", "The query must return at least two columns."))
		for _, test := range []struct {
			key          string
			node, parent any
		}{
			{"NULL::int, 1::int; --", nil, int64(1)},
			{"1::int, NULL::int; --", int64(1), nil},
			{"NULL::int, NULL::int; --", nil, nil},
		} {
			q = "SELECT * FROM cb_part.connectby('cb_tree'," + connectbyLiteral(test.key) + ",'p','0',1) AS t(node integer,parent integer,level integer)"
			run(fixture.Select(q, []string{"node", "parent", "level"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), nil, int64(0)}, {test.node, test.parent, int64(1)}}))
		}
		// Raw fragments remain statement lists. SPI uses the last SELECT's
		// descriptor/rows, not the earlier one-column result.
		key := "1; SELECT 'child'::text, 'root'::text; --"
		q = connectbyQuery(0, "'cb_tree',"+connectbyLiteral(key)+",'p','root',1")
		run(connectbySelect(0, q, [][]any{{"root", nil, int64(0)}, {"child", "root", int64(1)}}))
		run(fixture.Exec("SET application_name TO 'cb_original'", 0))
		run(fixture.Exec("CREATE TABLE cb_utility_guard(v integer)", 0))
		run(fixture.Exec("INSERT INTO cb_utility_guard VALUES(99)", 1))
		for _, test := range []struct{ command, tag string }{
			{"SET application_name TO 'cb_side_effect'", "SET"},
			{"CREATE TABLE cb_utility_created(v integer)", "CREATE TABLE"},
			{"TRUNCATE cb_utility_guard", "TRUNCATE TABLE"},
			{"COPY cb_missing TO '/cb_not_written'", "COPY"},
		} {
			key := "1; " + test.command + "; --"
			q := connectbyQuery(0, "'cb_tree',"+connectbyLiteral(key)+",'p','root',1")
			internal := "SELECT " + key + ", p FROM cb_tree WHERE p = 'root' AND " + key + " IS NOT NULL AND " + key + " <> p"
			s := connectbyNestedError(q, internal, "0A000", test.tag+" is not allowed in a non-volatile function", "")
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"cb_original"}}))
			run(fixture.Select("SELECT v FROM cb_utility_guard ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='cb_utility_created'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		// Unsupported SPI transaction/frontend COPY return a negative status,
		// not the read-only SQL error, and abandon the rest of the list.
		for _, command := range []string{"COMMIT", "SAVEPOINT cb_ignored", "COPY cb_missing TO STDOUT", "COPY cb_missing FROM STDIN"} {
			key := "1; " + command + "; SELECT missing FROM cb_missing; --"
			q := connectbyQuery(0, "'cb_tree',"+connectbyLiteral(key)+",'p','root',1")
			run(connectbySelect(0, q, [][]any{{"root", nil, int64(0)}}))
			run(fixture.Prepared(connectbySelect(0, q, [][]any{{"root", nil, int64(0)}})))
		}
		// Ordinary optimizable-command analysis still wins over read-only
		// classification; utility execution checks do not run at all.
		key = "1; INSERT INTO cb_missing VALUES(1); --"
		q = connectbyQuery(0, "'cb_tree',"+connectbyLiteral(key)+",'p','root',1")
		internalSQL := "SELECT " + key + ", p FROM cb_tree WHERE p = 'root' AND " + key + " IS NOT NULL AND " + key + " <> p"
		run(connectbyNestedError(q, internalSQL, "42P01", `relation "cb_missing" does not exist`, fmt.Sprint(strings.Index(internalSQL, "cb_missing")+1)))
		connectbyReadOnlyLocks(t, p, b, c)
		for _, test := range []struct{ args, sql, code, message, pos string }{
			{"'cb_missing','k','p','root',0", "SELECT k, p FROM cb_missing WHERE p = 'root' AND k IS NOT NULL AND k <> p", "42P01", `relation "cb_missing" does not exist`, "18"},
			{"'cb_tree','missing','p','root',0", "SELECT missing, p FROM cb_tree WHERE p = 'root' AND missing IS NOT NULL AND missing <> p", "42703", `column "missing" does not exist`, "8"},
			{"'cb_tree','k /* 雪 */','missing','root',0", "SELECT k /* 雪 */, missing FROM cb_tree WHERE missing = 'root' AND k /* 雪 */ IS NOT NULL AND k /* 雪 */ <> missing", "42703", `column "missing" does not exist`, "19"},
			{`'"CB Schema".Tree','"Key"','"Parent Key"','root',0`, `SELECT "Key", "Parent Key" FROM "CB Schema".Tree WHERE "Parent Key" = 'root' AND "Key" IS NOT NULL AND "Key" <> "Parent Key"`, "42P01", `relation "CB Schema.tree" does not exist`, "33"},
		} {
			q = connectbyQuery(0, test.args)
			s := connectbyNestedError(q, test.sql, test.code, test.message, test.pos)
			run(s)
			run(fixture.Prepared(s))
		}
		args := "'cb_tree','k','p','\"Absent\"','root',0"
		internal := `SELECT k, p FROM cb_tree WHERE p = 'root' AND k IS NOT NULL AND k <> p ORDER BY "Absent"`
		run(connectbyNestedError(connectbyQuery(2, args), internal, "42703", `column "Absent" does not exist`, "81"))
	})
}

func connectbyReadOnlyLocks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_locked AS SELECT k,p FROM cb_tree FOR SHARE NOWAIT", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_nested AS SELECT * FROM cb_readonly_locked", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_strong AS SELECT k,p FROM cb_tree FOR UPDATE NOWAIT", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_weak AS SELECT k,p FROM cb_readonly_strong FOR SHARE NOWAIT", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_weak_nested AS SELECT * FROM cb_readonly_weak", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_plain AS SELECT k,p FROM cb_tree", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_ordered AS SELECT k,p FROM cb_tree ORDER BY k", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_reversed AS SELECT a.k,a.p FROM cb_tree AS a CROSS JOIN cb_tree AS b FOR SHARE OF b NOWAIT FOR UPDATE OF a NOWAIT", 0))
	connectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_readonly_reversed_nested AS SELECT * FROM cb_readonly_reversed", 0))
	holder, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	connectbyRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
	defer holder.ExecContext(p.Ctx, "ROLLBACK")
	// Real conflicting tuple locks make NOWAIT an independent execution
	// witness: executing any nested row mark would report 55P03, not 0A000.
	connectbyRun(t, p, b, holder, fixture.Select("SELECT k FROM cb_tree ORDER BY k FOR UPDATE", []string{"k"}, []string{"TEXT"}, [][]any{{"row2"}, {"row4"}, {"row5"}, {"row6"}, {"row8"}, {"row9"}}))
	for index, test := range []struct{ relation, key, clause string }{
		{"(SELECT k,p FROM cb_tree FOR UPDATE NOWAIT) AS locked", "k", "FOR UPDATE"},
		{"(SELECT k,p FROM cb_tree FOR NO KEY UPDATE NOWAIT) AS locked", "k", "FOR NO KEY UPDATE"},
		{"(WITH locked AS (SELECT k,p FROM cb_tree FOR KEY SHARE NOWAIT) SELECT k,p FROM locked) AS nested", "k", "FOR KEY SHARE"},
		{"cb_tree", "(SELECT k FROM cb_tree WHERE k='row5' FOR SHARE NOWAIT), 'row2'::text; --", "FOR SHARE"},
		{"cb_readonly_nested", "k", "FOR SHARE"},
		// Effective tuple strengths merge through stored views, but a
		// preceding planned COPY/reference mark can give the plain SELECT tag.
		{"cb_readonly_strong FOR SHARE NOWAIT; --", "k", ""},
		{"cb_readonly_weak_nested", "k", ""},
		{"cb_readonly_weak_nested FOR KEY SHARE NOWAIT; --", "k", ""},
		// A later stronger but unrelated occurrence must not replace the
		// first occurrence's effective command-tag strength.
		{"(SELECT a.k,a.p FROM cb_tree AS a CROSS JOIN cb_tree AS b FOR SHARE OF a NOWAIT FOR UPDATE OF b NOWAIT) AS mixed", "k", "FOR SHARE"},
		// Inherited locking alone does not prevent a plain view's pullup;
		// an ordered view is a retained boundary with a reference marker.
		{"cb_readonly_plain FOR SHARE NOWAIT; --", "k", "FOR SHARE"},
		{"cb_readonly_ordered FOR SHARE NOWAIT; --", "k", ""},
		// Locking-clause insertion order is independent of FROM order.
		{"(SELECT a.k,a.p FROM cb_tree AS a CROSS JOIN cb_tree AS b FOR SHARE OF b NOWAIT FOR UPDATE OF a NOWAIT) AS reversed", "k", "FOR SHARE"},
		{"cb_readonly_reversed", "k", "FOR SHARE"},
		{"cb_readonly_reversed_nested", "k", "FOR SHARE"},
		// Parent-level markers precede an earlier-FROM child query's
		// markers; both levels independently start their order at one.
		{"(SELECT a.k,a.p FROM (SELECT k,p FROM cb_tree FOR SHARE NOWAIT) AS a CROSS JOIN cb_tree AS b FOR UPDATE OF b NOWAIT) AS parent_marked", "k", "FOR UPDATE"},
	} {
		t.Run(fmt.Sprintf("locking_%02d", index), func(t *testing.T) {
			args := connectbyLiteral(test.relation) + "," + connectbyLiteral(test.key) + ",'p','row2',1"
			query := connectbyQuery(0, args)
			internal := "SELECT " + test.key + ", p FROM " + test.relation + " WHERE p = 'row2' AND " + test.key + " IS NOT NULL AND " + test.key + " <> p"
			tag := "SELECT"
			if test.clause != "" {
				tag += " " + test.clause
			}
			step := connectbyNestedError(query, internal, "0A000", tag+" is not allowed in a non-volatile function", "")
			t.Run("direct", func(t *testing.T) { connectbyRun(t, p, b, c, step) })
			t.Run("prepared", func(t *testing.T) { connectbyRun(t, p, b, c, fixture.Prepared(step)) })
		})
	}
	// Analysis-only locking definitions are not planned row marks. This
	// succeeds even while the same holder would reject an executed NOWAIT.
	relation := "(WITH unused AS (SELECT k,p FROM cb_tree FOR UPDATE NOWAIT) SELECT k,p FROM cb_tree) AS unlocked"
	query := connectbyQuery(0, connectbyLiteral(relation)+",'k','p','row2',1")
	step := connectbySelect(0, query, [][]any{{"row2", nil, int64(0)}, {"row5", "row2", int64(1)}, {"row4", "row2", int64(1)}})
	connectbyRun(t, p, b, c, step)
	connectbyRun(t, p, b, c, fixture.Prepared(step))
}

func TestReferenceConnectbyReturnDiagnostics(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { connectbyRun(t, p, b, c, s) }
		connectbyTree(t, p, b, c)
		args := []string{"'cb_tree','k','p','row2',0", "'cb_tree','k','p','row2',0,'~'", "'cb_tree','k','p','pos','row2',0", "'cb_tree','k','p','pos','row2',0,'~'"}
		for v, a := range args {
			for _, width := range []int{2, 3, 4, 5, 6} {
				expected := 3
				if v == 1 || v == 2 {
					expected = 4
				}
				if v == 3 {
					expected = 5
				}
				if width == expected {
					continue
				}
				defs := []string{"node text", "parent text"}
				for i := 2; i < width; i++ {
					defs = append(defs, fmt.Sprintf("c%d text", i))
				}
				q := "SELECT * FROM cb_part.connectby(" + a + ") AS t(" + strings.Join(defs, ",") + ")"
				s := connectbyError(q, "42804", "invalid connectby return type", fmt.Sprintf("Return row must have %d columns, not %d.", expected, width))
				run(s)
				run(fixture.Prepared(s))
			}
			for _, test := range []struct{ old, new, detail string }{
				{"level integer", "level text", "Third return column (depth) must be type integer."},
				{"branch text", "branch integer", "Fourth return column (branch) must be type text."},
				{"serial integer", "serial text", map[bool]string{true: "Fifth return column (serial) must be type integer.", false: "Fourth return column (serial) must be type integer."}[v == 3]},
			} {
				if !strings.Contains(connectbyDefs[v], test.old) {
					continue
				}
				q := "SELECT * FROM cb_part.connectby(" + a + ") AS t(" + strings.Replace(connectbyDefs[v], test.old, test.new, 1) + ")"
				s := connectbyError(q, "42804", "invalid connectby return type", test.detail)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		q := "SELECT * FROM cb_part.connectby('cb_tree','k','p','row2',0)"
		s := connectbyError(q, "42601", `a column definition list is required for functions returning "record"`, "")
		s.Diagnostic.Position = "15"
		s.ErrorStage = ""
		run(s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		run(s)
		run(fixture.Exec("CREATE TABLE cb_numbers(k integer,p integer)", 0))
		run(fixture.Exec("INSERT INTO cb_numbers VALUES(2,1)", 1))
		for _, test := range []struct{ defs, detail string }{
			{"node text,parent integer,level integer", "Source key type integer does not match return key type text."},
			{"node integer,parent text,level integer", "Source parent key type integer does not match return parent key type text."},
		} {
			q = "SELECT * FROM cb_part.connectby('cb_numbers','k','p','1',0) AS t(" + test.defs + ")"
			run(connectbyError(q, "42804", "invalid connectby return type", test.detail))
		}
		q = "SELECT * FROM cb_part.connectby('cb_numbers','k','p','9',0) AS t(node text,parent text,level integer)"
		run(connectbySelect(0, q, [][]any{{"9", nil, int64(0)}}))
		q = "SELECT * FROM cb_part.connectby('cb_missing','k','p','oops',0) AS t(node integer,parent integer,level integer)"
		run(connectbyError(q, "22P02", `invalid input syntax for type integer: "oops"`, ""))
		run(fixture.Exec("CREATE TABLE cb_varchar(k varchar(5),p varchar(5))", 0))
		run(fixture.Exec("INSERT INTO cb_varchar VALUES('child','root')", 1))
		q = "SELECT * FROM cb_part.connectby('cb_varchar','k','p','root',0) AS t(node varchar,parent varchar,level integer)"
		s = fixture.Select(q, []string{"node", "parent", "level"}, []string{"VARCHAR", "VARCHAR", "INT4"}, [][]any{{"root", nil, int64(0)}, {"child", "root", int64(1)}})
		s.Bag = true
		run(s)
		q = "SELECT * FROM cb_part.connectby('cb_varchar','k','p','root',0) AS t(node varchar(6),parent varchar(5),level integer)"
		run(connectbyError(q, "42804", "invalid connectby return type", "Source key type character varying(5) does not match return key type character varying(6)."))
		for _, test := range []struct{ args, signature string }{
			{"1", "integer"},
			{"true,'k','p','root',0", "boolean, unknown, unknown, unknown, integer"},
			{"relname => 'cb_tree',keyid_fld => 'k',parent_keyid_fld => 'p',start_with => 'root',max_depth => 0", "relname => unknown, keyid_fld => unknown, parent_keyid_fld => unknown, start_with => unknown, max_depth => integer"},
			{"VARIADIC ARRAY['cb_tree','k','p','root']::text[]", "text[]"},
		} {
			q = "SELECT * FROM cb_part.connectby(" + test.args + ") AS t(node text,parent text,level integer)"
			s = connectbyError(q, "42883", "function cb_part.connectby("+test.signature+") does not exist", "")
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = "15"
			s.ErrorStage = ""
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		}
		// Conversion here occurs in nested SQL rather than synthetic-root input.
		q = "SELECT * FROM cb_part.connectby('cb_numbers','k','p','bad',0) AS t(node text,parent text,level integer)"
		internal := "SELECT k, p FROM cb_numbers WHERE p = 'bad' AND k IS NOT NULL AND k <> p"
		run(connectbyNestedError(q, internal, "22P02", `invalid input syntax for type integer: "bad"`, "39"))
		run(fixture.Exec("CREATE TABLE cb_mixed(k integer,p text)", 0))
		run(fixture.Exec("INSERT INTO cb_mixed VALUES(2,'1')", 1))
		q = "SELECT * FROM cb_part.connectby('cb_mixed','k,p FROM cb_mixed; --','p','1',1) AS t(node integer,parent text,level integer)"
		s = fixture.Select(q, []string{"node", "parent", "level"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), nil, int64(0)}, {int64(2), "1", int64(1)}})
		s.Bag = true
		run(s)
	})
}
