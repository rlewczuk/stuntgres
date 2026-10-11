package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Dependence is relative to a join boundary: a function's references satisfied
// inside an RHS subtree must not make that subtree depend on its enclosing LHS.
func TestReferenceRegexpJoinDependencyRightFull(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, join := range []string{"RIGHT", "FULL"} {
			outerRoutes := func(s fixture.Step) {
				nullable := []string{"id"}
				if join == "FULL" {
					nullable = s.Columns
				}
				s.CheckMetadata = regexpMetadataNullable(b.PG, nullable)
				regexpRoutes(t, p, b, s)
			}
			for _, empty := range []bool{false, true} {
				left := `(VALUES(1)) a(id)`
				var id any = int64(1)
				if empty {
					left = `(SELECT 1 AS id WHERE false) a`
					id = nil
				}
				q := `SELECT a.id,b.s,r.x FROM ` + left + ` ` + join + ` JOIN ((VALUES('a b')) b(s) CROSS JOIN regexp_split_to_table(b.s,' ') r(x)) ON true ORDER BY r.x`
				outerRoutes(regexpRows(b, q, []string{"id", "s", "x"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{id, "a b", "a"}, {id, "a b", "b"}}))

				// ON false tests independent RHS materialization and the enclosing
				// join's own null extension, not the inner function's correlation.
				q = `SELECT a.id,b.s,r.x FROM ` + left + ` ` + join + ` JOIN ((VALUES('a b')) b(s) CROSS JOIN regexp_split_to_table(b.s,' ') r(x)) ON false ORDER BY a.id NULLS LAST,r.x NULLS LAST`
				rows := [][]any{{nil, "a b", "a"}, {nil, "a b", "b"}}
				if join == "FULL" && !empty {
					rows = append([][]any{{int64(1), nil, nil}}, rows...)
				}
				outerRoutes(regexpRows(b, q, []string{"id", "s", "x"}, []string{"INT4", "TEXT", "TEXT"}, rows))
			}

			// Both selected SRF result families share the boundary rule. Order
			// by ordinality, never by an array's unrelated operator class.
			q := `SELECT a.id,b.s,r.m,r.o FROM (SELECT 1 AS id WHERE false) a ` + join + ` JOIN ((VALUES('ab')) b(s) CROSS JOIN regexp_matches(b.s,'(.)','g') WITH ORDINALITY r(m,o)) ON true ORDER BY r.o`
			outerRoutes(regexpRows(b, q, []string{"id", "s", "m", "o"}, []string{"INT4", "TEXT", "_TEXT", "INT8"}, [][]any{{nil, "ab", "{a}", int64(1)}, {nil, "ab", "{b}", int64(2)}}))

			// b is an inherited prefix of the c/function join, but is satisfied
			// within the enclosing RHS. No value from a is captured at any depth.
			q = `SELECT a.id,b.s,c.s AS cs,r.x FROM (VALUES(1)) a(id) ` + join + ` JOIN ((VALUES('a b')) b(s) CROSS JOIN ((VALUES('c d')) c(s) CROSS JOIN regexp_split_to_table(b.s||' '||c.s,' ') r(x))) ON true ORDER BY r.x`
			outerRoutes(regexpRows(b, q, []string{"id", "s", "cs", "x"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "a b", "c d", "a"}, {int64(1), "a b", "c d", "b"}, {int64(1), "a b", "c d", "c"}, {int64(1), "a b", "c d", "d"}}))

			// The unused b prefix must not be required by the deepest opener.
			q = `SELECT a.id,b.id AS bid,c.id AS cid,r.x FROM (SELECT 1 AS id WHERE false) a ` + join + ` JOIN ((VALUES(10,'unused')) b(id,s) CROSS JOIN ((VALUES(20,'c d')) c(id,s) CROSS JOIN regexp_split_to_table(c.s,' ') r(x))) ON true ORDER BY r.x`
			outerRoutes(regexpRows(b, q, []string{"id", "bid", "cid", "x"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{nil, int64(10), int64(20), "c"}, {nil, int64(10), int64(20), "d"}}))
		}
	})
}

// P is the already-visible ancestor/comma prefix, L the current join's left
// operand. Capturing P alone is legal under RIGHT/FULL; capturing L is not.
func TestReferenceRegexpJoinDependencyPrefixes(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, join := range []string{"RIGHT", "FULL"} {
			for _, empty := range []bool{false, true} {
				left := `(VALUES(1)) b(id)`
				var bid any = int64(1)
				if empty {
					left = `(SELECT 1 AS id WHERE false) b`
					bid = nil
				}
				q := `SELECT a.s,b.id,r.x FROM (VALUES('a b')) a(s),(` + left + ` ` + join + ` JOIN regexp_split_to_table(a.s,' ') r(x) ON true) ORDER BY r.x`
				s := regexpRows(b, q, []string{"s", "id", "x"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"a b", bid, "a"}, {"a b", bid, "b"}})
				nullable := []string{"id"}
				if join == "FULL" {
					nullable = []string{"id", "x"}
				}
				s.CheckMetadata = regexpMetadataNullable(b.PG, nullable)
				regexpRoutes(t, p, b, s)

				// Two leading unused slots plus two slots in a prove that the
				// captured prefix is read at its genuine offset, once per a row.
				q = `SELECT p.id,a.id AS aid,b.id AS bid,r.x,r.o FROM (VALUES(900,'unused')) p(id,s),(VALUES(1,'a b'),(2,'c d')) a(id,s),(` + left + ` ` + join + ` JOIN regexp_split_to_table(a.s,' ') WITH ORDINALITY r(x,o) ON true) ORDER BY a.id,r.o`
				s = regexpRows(b, q, []string{"id", "aid", "bid", "x", "o"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(900), int64(1), bid, "a", int64(1)}, {int64(900), int64(1), bid, "b", int64(2)}, {int64(900), int64(2), bid, "c", int64(1)}, {int64(900), int64(2), bid, "d", int64(2)}})
				nullable = []string{"bid"}
				if join == "FULL" {
					nullable = []string{"bid", "x", "o"}
				}
				s.CheckMetadata = regexpMetadataNullable(b.PG, nullable)
				regexpRoutes(t, p, b, s)
			}
		}

		// All of a, b and c are genuinely captured at nested INNER boundaries;
		// the widths and repeated ancestor rows distinguish every prefix slot.
		q := `SELECT a.id,b.id AS bid,c.id AS cid,r.x,r.o FROM (VALUES(1,'A'),(2,'Q')) a(id,s) CROSS JOIN ((VALUES(10,'B')) b(id,s) CROSS JOIN ((VALUES(100,'C')) c(id,s) CROSS JOIN regexp_split_to_table(a.s||' '||b.s||' '||c.s,' ') WITH ORDINALITY r(x,o))) ORDER BY a.id,r.o`
		regexpRoutes(t, p, b, regexpRows(b, q, []string{"id", "bid", "cid", "x", "o"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(10), int64(100), "A", int64(1)}, {int64(1), int64(10), int64(100), "B", int64(2)}, {int64(1), int64(10), int64(100), "C", int64(3)}, {int64(2), int64(10), int64(100), "Q", int64(1)}, {int64(2), int64(10), int64(100), "B", int64(2)}, {int64(2), int64(10), int64(100), "C", int64(3)}}))
	})
}

func TestReferenceRegexpJoinDependencyDiagnostics(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, join := range []string{"RIGHT", "FULL"} {
			for _, c := range []struct{ from, table, token string }{
				{`(VALUES('a b')) a(s) ` + join + ` JOIN ((VALUES(1)) b(id) CROSS JOIN regexp_split_to_table(a.s,' ') r(x)) ON true`, "a", "a.s,' '"},
				// The ancestor a reference is legal, the local-left b reference is
				// forbidden. Report b, not the earlier a capture or function alias.
				{`(VALUES('a')) a(s),((VALUES('b')) b(s) ` + join + ` JOIN regexp_split_to_table(a.s||' '||b.s,' ') r(x) ON true)`, "b", "b.s,' '"},
				{`regexp_split_to_table('a b',' ') a(x) ` + join + ` JOIN ((VALUES(1)) b(id) CROSS JOIN regexp_matches(a.x,'.','g') r(m)) ON true`, "a", "a.x,'.'"},
				// No alias: the offending FROM entry uses the selected function's
				// actual default table/column name, not an empty diagnostic name.
				{`regexp_split_to_table('a b',' ') ` + join + ` JOIN regexp_matches(regexp_split_to_table,'.','g') r(m) ON true`, "regexp_split_to_table", "regexp_split_to_table,'.'"},
				{`(VALUES('a b')) "Outer É"(s) ` + join + ` JOIN regexp_split_to_table("Outer É".s,' ') r(x) ON true`, "Outer É", `"Outer É".s,' '`},
			} {
				q := `SELECT /* é🙂 */ 1 FROM ` + c.from
				s := regexpBindError(q, "42P10", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, c.table), "", c.token)
				s.Diagnostic.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
				regexpRoutes(t, p, b, s)
			}
		}
	})
}

// Independently specified compact presentation, confirmed against pinned
// PostgreSQL 18.0 before the native join-dependency remedy.
func regexpJoinViewDefinition(join, table, column string) string {
	alias := ""
	if column != "s" {
		alias = " AS s"
	}
	return fmt.Sprintf(" SELECT a.id,\n    b.%s%s,\n    r.x\n   FROM (dep_view_a a\n     %s JOIN (%s b\n     CROSS JOIN LATERAL regexp_split_to_table(b.%s, ' '::text) r(x)) ON (true));", column, alias, join, table, column)
}

func TestReferenceRegexpJoinDependencyViews(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE dep_view_a(id INT)`, 0))
		run(fixture.Exec(`CREATE TABLE dep_view_b(s TEXT)`, 0))
		run(fixture.Exec(`INSERT INTO dep_view_b VALUES('a b')`, 1))
		for _, join := range []string{"RIGHT", "FULL"} {
			name := "dep_view_right"
			if join == "FULL" {
				name = "dep_view_full"
			}
			run(fixture.Exec(`CREATE VIEW `+name+` AS SELECT a.id,b.s,r.x FROM dep_view_a a `+join+` JOIN (dep_view_b b CROSS JOIN regexp_split_to_table(b.s,' ') r(x)) ON true`, 0))
		}
		check := func() {
			for _, name := range []string{"dep_view_right", "dep_view_full"} {
				regexpRoutes(t, p, b, fixture.Select(`SELECT id,s,x FROM `+name+` ORDER BY x`, []string{"id", "s", "x"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, "a b", "a"}, {nil, "a b", "b"}}))
			}
		}
		check()
		for _, c := range []struct{ name, join string }{{"dep_view_right", "RIGHT"}, {"dep_view_full", "FULL"}} {
			definition := regexpJoinViewDefinition(c.join, "dep_view_b", "s")
			step := regexpOne(b, `pg_get_viewdef('`+c.name+`'::regclass,false)`, "TEXT", definition)
			step.CheckMetadata = regexpMetadataNullable(b.PG, []string{"result"})
			regexpRoutes(t, p, b, step)
			// Replay the independently specified entire definition, then assert
			// actual execution, rather than only inspecting SQL substrings.
			run(fixture.Exec(`CREATE VIEW `+c.name+`_copy AS `+definition, 0))
			regexpRoutes(t, p, b, fixture.Select(`SELECT id,s,x FROM `+c.name+`_copy ORDER BY x`, []string{"id", "s", "x"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, "a b", "a"}, {nil, "a b", "b"}}))
		}
		const heldSQL = `SELECT id,s,x FROM dep_view_right ORDER BY x`
		stmt, err := b.DB.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: heldSQL}
		want := fixture.Select(heldSQL, []string{"id", "s", "x"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, "a b", "a"}, {nil, "a b", "b"}})
		run(fixture.Exec(`ALTER TABLE dep_view_b RENAME COLUMN s TO source`, 0))
		check()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec(`ALTER TABLE dep_view_b RENAME TO dep_view_b_renamed`, 0))
		check()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		for _, c := range []struct{ name, join string }{{"dep_view_right", "RIGHT"}, {"dep_view_full", "FULL"}} {
			step := regexpOne(b, `pg_get_viewdef('`+c.name+`'::regclass,false)`, "TEXT", regexpJoinViewDefinition(c.join, "dep_view_b_renamed", "source"))
			step.CheckMetadata = regexpMetadataNullable(b.PG, []string{"result"})
			regexpRoutes(t, p, b, step)
		}
		// Both original definitions and their independently replayed copies
		// remain real dependencies of the renamed selected-function argument.
		s := regexpRuntimeError(`DROP TABLE dep_view_b_renamed`, "2BP01", `cannot drop table dep_view_b_renamed because other objects depend on it`, `Use DROP ... CASCADE to drop the dependent objects too.`)
		s.SelectRows = false
		s.Diagnostic.Detail = "view dep_view_right depends on table dep_view_b_renamed\nview dep_view_full depends on table dep_view_b_renamed\nview dep_view_right_copy depends on table dep_view_b_renamed\nview dep_view_full_copy depends on table dep_view_b_renamed"
		regexpRoutes(t, p, b, s)
		check()
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"dep_view_right_copy", "dep_view_full_copy", "dep_view_right", "dep_view_full"} {
			run(fixture.Exec(`DROP VIEW `+name, 0))
		}
		run(fixture.Exec(`DROP TABLE dep_view_b_renamed`, 0))
		run(fixture.Exec(`DROP TABLE dep_view_a`, 0))
	})
}
