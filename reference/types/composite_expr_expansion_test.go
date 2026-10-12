package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprExpansion(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7,z)',NULL),(2,NULL,NULL),(3,'(,)',NULL)`, 3))
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}, {nil, nil}, {nil, nil}})
		compositeExprBoth(t, p, b, c, `SELECT (v).*,(v).i AS again,(v).* FROM holder WHERE k=1`, []string{"i", "t", "again", "i", "t"}, []string{"INT4", "TEXT", "INT4", "INT4", "TEXT"}, [][]any{{int64(7), "z", int64(7), int64(7), "z"}})
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder WHERE k=0`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{})
		compositeExprBoth(t, p, b, c, `SELECT (ROW()).*`, []string{}, []string{}, [][]any{{}})
		compositeExprBoth(t, p, b, c, `SELECT (ROW()).* FROM holder ORDER BY k`, []string{}, []string{}, [][]any{{}, {}, {}})
		compositeExprBoth(t, p, b, c, `SELECT (ROW()).* FROM holder WHERE false`, []string{}, []string{}, [][]any{})
		compositeExprBoth(t, p, b, c, `SELECT (CASE WHEN k=1 THEN v ELSE NULL::pair END).* FROM holder ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}, {nil, nil}, {nil, nil}})
	})
}

func TestReferenceCompositeExprValuesAndInsertExpansion(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		compositeExprBoth(t, p, b, c, `VALUES ((ROW(1,'x')::pair).*)`, []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}})
		compositeExprBoth(t, p, b, c, `VALUES ((ROW(1,'x')::pair).*),(2,'y')`, []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}})
		compositeExprBoth(t, p, b, c, `VALUES ((NULL::pair).*),((ROW(2,'y')::pair).*)`, []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}, {int64(2), "y"}})
		compositeExprError(t, p, c, `VALUES ((ROW(1,'x')::pair).*),(ROW(2,'y'))`, "42601", "VALUES lists must all be the same length", "ROW(2")
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE expanded(i integer,t text DEFAULT 'default')`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expanded VALUES ((ROW(1,'x')::pair).*),(2,'y')`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO expanded SELECT (ROW($1,$2)::pair).*`, 1, 3, "z")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO expanded VALUES ((ROW($1,$2)::pair).*)`, 1, 4, "prepared")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expanded VALUES (5,DEFAULT)`, 1))
		compositeExprBoth(t, p, b, c, `SELECT i,t FROM expanded ORDER BY i`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), "z"}, {int64(4), "prepared"}, {int64(5), "default"}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE expanded_identity(k integer GENERATED ALWAYS AS IDENTITY,i integer,t text)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expanded_identity(i,t) VALUES ((ROW(6,'identity')::pair).*)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO expanded_identity(i,t) VALUES ((ROW($1,$2)::pair).*)`, 1, 7, "held")))
		compositeExprBoth(t, p, b, c, `SELECT i,t FROM expanded_identity ORDER BY i`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(6), "identity"}, {int64(7), "held"}})
	})
}

func TestReferenceCompositeExprZeroWidthValues(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TABLE expanded_one(i integer)`,
			`CREATE TABLE expansion_sentinel(k integer,t text)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expanded_one VALUES (91)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO expansion_sentinel VALUES (7,'untouched')`, 1))
		assertUnchanged := func(t *testing.T) {
			t.Helper()
			compositeExprBoth(t, p, b, c, `SELECT i FROM expanded_one ORDER BY i`, []string{"i"}, []string{"INT4"}, [][]any{{int64(91)}})
			compositeExprBoth(t, p, b, c, `SELECT k,t FROM expansion_sentinel ORDER BY k`, []string{"k", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "untouched"}})
		}
		// Pinned PostgreSQL18 reports no position when the later expanded
		// list is empty, and the first later expression for the reverse.
		for _, tc := range []struct {
			name, query, position string
		}{
			{"values_nonempty_empty", `VALUES (1), ((ROW()).*)`, ""},
			{"values_empty_nonempty", `VALUES ((ROW()).*), (1)`, "22"},
			{"insert_nonempty_empty", `INSERT INTO expanded_one VALUES (1), ((ROW()).*)`, ""},
			{"insert_empty_nonempty", `INSERT INTO expanded_one VALUES ((ROW()).*), (1)`, "47"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				want := fixture.CompositeReferenceDiagnostic("42601", "VALUES lists must all be the same length")
				want.Position = tc.position
				for _, route := range []string{"exec", "query", "prepare"} {
					t.Run(route, func(t *testing.T) {
						var err error
						switch route {
						case "exec":
							var result sql.Result
							result, err = c.ExecContext(p.Ctx, tc.query)
							if result != nil {
								t.Error("failed direct execution exposed result")
							}
						case "query":
							var rows *sql.Rows
							rows, err = c.QueryContext(p.Ctx, tc.query)
							if rows != nil {
								rows.Close()
								t.Error("failed direct query exposed cursor")
							}
						case "prepare":
							var stmt *sql.Stmt
							stmt, err = c.PrepareContext(p.Ctx, tc.query)
							if stmt != nil {
								stmt.Close()
								t.Error("analysis failure exposed prepared handle")
							}
						}
						fixture.AssertDiagnostic(t, err, &want)
						// Each failed route must leave both tables unchanged and
						// the same physical connection usable, not just the last.
						assertUnchanged(t)
					})
				}
			})
		}
		// Empty expansion remains a real one-row, zero-column result rather
		// than being excluded to avoid the unequal-width diagnostic.
		compositeExprBoth(t, p, b, c, `VALUES ((ROW()).*)`, []string{}, []string{}, [][]any{{}})
		assertUnchanged(t)
	})
}

func TestReferenceCompositeExprNestedStars(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL)`, 1))
		compositeExprBoth(t, p, b, c, `SELECT ROW((v).*,9) AS expanded,ROW(v,9) AS nested FROM holder`, []string{"expanded", "nested"}, []string{"RECORD", "RECORD"}, [][]any{{"(7,z,9)", `("(7,z)",9)`}})
		compositeExprBoth(t, p, b, c, `SELECT num_nulls(h.*) AS n,num_nonnulls(h.*) AS nn FROM holder h`, []string{"n", "nn"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}})
		compositeExprBoth(t, p, b, c, `SELECT (h).* FROM holder h`, []string{"k", "v", "w"}, []string{"INT4", "", ""}, [][]any{{int64(1), "(7,z)", nil}})
		compositeExprBoth(t, p, b, c, `SELECT ROW(h.*) AS expanded FROM holder h`, []string{"expanded"}, []string{"RECORD"}, [][]any{{`(1,"(7,z)",)`}})
		compositeExprBoth(t, p, b, c, `SELECT h.*::text AS whole FROM holder h`, []string{"whole"}, []string{"TEXT"}, [][]any{{`(1,"(7,z)",)`}})
		for _, q := range []string{`SELECT num_nulls((h).*) FROM holder h`, `SELECT ((h).*)::text FROM holder h`, `SELECT CASE WHEN true THEN (h).* ELSE NULL END FROM holder h`} {
			compositeExprError(t, p, c, q, "0A000", `row expansion via "*" is not supported here`, "h")
		}
	})
}

func TestReferenceCompositeExprReturning(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL),(2,NULL,NULL)`, 2))
		// PostgreSQL18 resolves fields/stars on the scalar whole-row OLD
		// expression to the default RETURNING row, unlike qualified old.v.
		compositeExprSelect(t, p, b, c, fixture.Select(`UPDATE holder SET v.i=8 WHERE k=1 RETURNING old::text AS predecessor,new::text AS replacement,(old).*`, []string{"predecessor", "replacement", "k", "v", "w"}, []string{"TEXT", "TEXT", "INT4", "", ""}, [][]any{{`(1,"(7,z)",)`, `(1,"(8,z)",)`, int64(1), "(8,z)", nil}}))
		compositeExprSelect(t, p, b, c, fixture.Prepared(fixture.Select(`UPDATE holder SET v.i=$1 WHERE k=1 RETURNING old::text AS predecessor,new::text AS replacement,(old).*`, []string{"predecessor", "replacement", "k", "v", "w"}, []string{"TEXT", "TEXT", "INT4", "", ""}, [][]any{{`(1,"(8,z)",)`, `(1,"(7,z)",)`, int64(1), "(7,z)", nil}}, 7)))
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE returning_shape(k integer)`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO returning_shape VALUES(1)`, 1))
		compositeExprSelect(t, p, b, c, fixture.Select(`UPDATE returning_shape SET k=2 RETURNING old.k AS before,(old).k AS parenthesized,(old).*,old::text AS whole`, []string{"before", "parenthesized", "k", "whole"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), int64(2), "(1)"}}))
		compositeExprSelect(t, p, b, c, fixture.Prepared(fixture.Select(`UPDATE returning_shape SET k=$1 RETURNING old.k AS before,(old).k AS parenthesized,(old).*,old::text AS whole`, []string{"before", "parenthesized", "k", "whole"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(3), int64(3), "(2)"}}, 3)))
		compositeExprSelect(t, p, b, c, fixture.Select(`UPDATE returning_shape SET k=4 RETURNING k(old) AS old_function,k(new) AS new_function,(old).k AS parenthesized,old.k AS before,old::text AS whole`, []string{"old_function", "new_function", "parenthesized", "before", "whole"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(4), int64(4), int64(4), int64(3), "(3)"}}))
		compositeExprSelect(t, p, b, c, fixture.Prepared(fixture.Select(`UPDATE returning_shape SET k=$1 RETURNING k(old) AS old_function,k(new) AS new_function,(old).k AS parenthesized,old.k AS before,old::text AS whole`, []string{"old_function", "new_function", "parenthesized", "before", "whole"}, []string{"INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(5), int64(5), int64(5), int64(4), "(4)"}}, 5)))
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO returning_shape VALUES(6)`, 1))
			step := fixture.Select(`DELETE FROM returning_shape WHERE k=6 RETURNING (new).k AS parenthesized,(new).*,new.k AS qualified,new::text AS whole,k(new) AS function,(old).*,num_nulls(new) AS null_whole,ROW(new)::text AS constructed,num_nulls(ROW(new)) AS present_constructor`, []string{"parenthesized", "k", "qualified", "whole", "function", "k", "null_whole", "constructed", "present_constructor"}, []string{"INT4", "INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(6), int64(6), nil, nil, int64(6), int64(6), int64(1), "()", int64(0)}})
			step.Prepared = prepared
			compositeExprSelect(t, p, b, c, step)
		}
		compositeExprSelect(t, p, b, c, fixture.Select(`UPDATE holder SET v.i=8,v.t='new' WHERE k=2 RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(8), "new"}}))
		compositeExprSelect(t, p, b, c, fixture.Prepared(fixture.Select(`UPDATE holder SET v.i=$1 WHERE k=2 RETURNING (old.v).i AS before,(new.v).i AS after`, []string{"before", "after"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}, 9)))
		compositeExprSelect(t, p, b, c, fixture.Prepared(fixture.Select(`DELETE FROM holder WHERE k=1 RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "z"}})))
		compositeExprSelect(t, p, b, c, fixture.Select(`DELETE FROM holder WHERE k=0 RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{}))
		// Pinned18 positions zero-width RETURNING at the expression's opening parenthesis.
		compositeExprError(t, p, c, `UPDATE holder SET k=k RETURNING (ROW()).*`, "42601", "RETURNING must have at least one column", "(ROW")
		compositeExprBoth(t, p, b, c, `SELECT (v).* FROM holder`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "new"}})
	})
}

func TestReferenceCompositeExprCorrelatedReturning(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE t(k integer)`)
		for _, prepared := range []bool{false, true} {
			// Each route starts from the same predecessor, rather than comparing
			// direct and prepared mutations against different transition rows.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO t VALUES(1)`, 1))
			run := func(q string, names, types []string, rows [][]any) {
				t.Helper()
				step := fixture.Select(q, names, types, rows)
				step.Prepared = prepared
				compositeExprSelect(t, p, b, c, step)
			}
			// tests95 independently pins whole transition values versus members
			// projected from them: the latter use the default RETURNING row.
			run(`UPDATE t SET k=2 RETURNING (SELECT old::text) AS whole_old,(SELECT new::text) AS whole_new,(SELECT (old).k) AS projected_old,(SELECT k(old)) AS function_old,(SELECT old.k) AS qualified_old`,
				[]string{"whole_old", "whole_new", "projected_old", "function_old", "qualified_old"},
				[]string{"TEXT", "TEXT", "INT4", "INT4", "INT4"},
				[][]any{{"(1)", "(2)", int64(2), int64(2), int64(1)}})
			run(`UPDATE t SET k=3 RETURNING WITH(OLD AS o,NEW AS n) (SELECT o::text) AS whole_old,(SELECT n::text) AS whole_new,(SELECT (o).k) AS projected_old,(SELECT k(o)) AS function_old,(SELECT o.k) AS qualified_old,(SELECT o::text FROM (VALUES(9)) o(k)) AS shadow`,
				[]string{"whole_old", "whole_new", "projected_old", "function_old", "qualified_old", "shadow"},
				[]string{"TEXT", "TEXT", "INT4", "INT4", "INT4", "TEXT"},
				[][]any{{"(2)", "(3)", int64(3), int64(3), int64(2), "(9)"}})
			run(`DELETE FROM t RETURNING (SELECT new::text) AS whole_new,(SELECT (new).k) AS projected_new,(SELECT k(new)) AS function_new,(SELECT new.k) AS qualified_new,(SELECT num_nulls(new)) AS null_new,(SELECT old::text) AS whole_old`,
				[]string{"whole_new", "projected_new", "function_new", "qualified_new", "null_new", "whole_old"},
				[]string{"TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT"},
				[][]any{{nil, int64(3), int64(3), nil, int64(1), "(3)"}})
			compositeExprBoth(t, p, b, c, `SELECT k FROM t`, []string{"k"}, []string{"INT4"}, [][]any{})

			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO t VALUES(3)`, 1))
			run(`DELETE FROM t RETURNING WITH(OLD AS o,NEW AS n) (SELECT n::text) AS whole_new,(SELECT (n).k) AS projected_new,(SELECT k(n)) AS function_new,(SELECT n.k) AS qualified_new,(SELECT num_nulls(n)) AS null_new,(SELECT o::text) AS whole_old,(SELECT o::text FROM (VALUES(9)) o(k)) AS shadow`,
				[]string{"whole_new", "projected_new", "function_new", "qualified_new", "null_new", "whole_old", "shadow"},
				[]string{"TEXT", "INT4", "INT4", "INT4", "INT4", "TEXT", "TEXT"},
				[][]any{{nil, int64(3), int64(3), nil, int64(1), "(3)", "(9)"}})
			compositeExprBoth(t, p, b, c, `SELECT k FROM t`, []string{"k"}, []string{"INT4"}, [][]any{})

			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO t VALUES(1)`, 1))
			// A scalar star beneath a cast stays one transition datum; ROW
			// expands its star argument. A grandchild must retain the actual
			// RETURNING owner, while nearer exposed columns take precedence.
			run(`UPDATE t SET k=2 RETURNING (SELECT old.*::text) AS star_old,(SELECT new.*::text) AS star_new,(SELECT ROW(old.*)::text) AS row_old,(SELECT ROW(new.*)::text) AS row_new,(SELECT (SELECT old::text)) AS deep_old,(SELECT old FROM (VALUES(9)) s(old)) AS column_shadow,(SELECT old::text FROM (VALUES(9)) old(k)) AS source_shadow`,
				[]string{"star_old", "star_new", "row_old", "row_new", "deep_old", "column_shadow", "source_shadow"},
				[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "TEXT"},
				[][]any{{"(1)", "(2)", "(1)", "(2)", "(1)", int64(9), "(9)"}})
			run(`DELETE FROM t RETURNING WITH(OLD AS o,NEW AS n) (SELECT o.*::text) AS star_old,(SELECT n.*::text) AS star_new,(SELECT ROW(o.*)::text) AS row_old,(SELECT ROW(n.*)::text) AS row_new,(SELECT (SELECT n::text)) AS deep_new,(SELECT n FROM (VALUES(9)) s(n)) AS column_shadow`,
				[]string{"star_old", "star_new", "row_old", "row_new", "deep_new", "column_shadow"},
				[]string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4"},
				[][]any{{"(2)", nil, "(2)", "()", nil, int64(9)}})
			compositeExprBoth(t, p, b, c, `SELECT k FROM t`, []string{"k"}, []string{"INT4"}, [][]any{})
		}
		// Root's pinned diagnostic oracle specifies complete logical records.
		// Ambiguous nearer columns cannot fall back to a transition whole row;
		// explicit aliases hide the default names even inside a child query.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO t VALUES(1)`, 1))
		compositeExprError(t, p, c, `UPDATE t SET k=2 RETURNING (SELECT old FROM (VALUES(9,10)) s(old,old))`, "42702", `column reference "old" is ambiguous`, "old FROM")
		compositeExprError(t, p, c, `UPDATE t SET k=2 RETURNING WITH(OLD AS o,NEW AS n) (SELECT old::text)`, "42703", `column "old" does not exist`, "old::text")
		compositeExprError(t, p, c, `DELETE FROM t RETURNING WITH(OLD AS o,NEW AS n) (SELECT new::text)`, "42703", `column "new" does not exist`, "new::text")
		compositeExprBoth(t, p, b, c, `SELECT k FROM t`, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})
	})
}
