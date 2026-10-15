package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterGeneratedExpressionBoundaries(t *testing.T) {
	for _, action := range []string{`ALTER TABLE parent ALTER b SET EXPRESSION AS(a+2)`, `ALTER TABLE ONLY parent ALTER b SET EXPRESSION AS(a+2)`, `ALTER TABLE ONLY parent ALTER b DROP EXPRESSION`, `ALTER TABLE child ALTER b DROP EXPRESSION`, `ALTER TABLE parent ALTER b DROP EXPRESSION`} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct/", true: "prepared/"}[prepared]+action, func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, base func(fixture.Step)) {
					run := func(s fixture.Step) { s.Prepared = prepared; base(s) }
					run(fixture.Exec(`CREATE TABLE parent(a INT,b INT GENERATED ALWAYS AS(a+1) STORED)`, 0))
					run(fixture.Exec(`CREATE TABLE child() INHERITS(parent)`, 0))
					run(fixture.Exec(`INSERT INTO parent(a) VALUES(1)`, 1))
					run(fixture.Exec(`INSERT INTO child(a) VALUES(2)`, 1))
					switch action {
					case `ALTER TABLE ONLY parent ALTER b DROP EXPRESSION`:
						run(fixture.ImplicitError(action, fixture.Diagnostic{Code: "0A000", Message: "ALTER TABLE / DROP EXPRESSION must be applied to child tables too"}))
					case `ALTER TABLE child ALTER b DROP EXPRESSION`:
						run(fixture.ImplicitError(action, fixture.Diagnostic{Code: "42P16", Message: "cannot drop generation expression from inherited column"}))
					default:
						run(fixture.Exec(action, 0))
						parentValue, childValue := int64(3), int64(4)
						if action == `ALTER TABLE ONLY parent ALTER b SET EXPRESSION AS(a+2)` {
							childValue = 3
						}
						if action == `ALTER TABLE parent ALTER b DROP EXPRESSION` {
							parentValue = 2
							childValue = 3
							run(fixture.Exec(`UPDATE child SET a=9`, 1))
						}
						run(alterGeneratedRows(`SELECT * FROM ONLY parent`, []any{int64(1), parentValue}))
						a := int64(2)
						if action == `ALTER TABLE parent ALTER b DROP EXPRESSION` {
							a = 9
						}
						run(alterGeneratedRows(`SELECT * FROM child`, []any{a, childValue}))
					}
				})
			})
		}
	}
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		q := `CREATE TABLE partitioned(a INT,b INT GENERATED ALWAYS AS(a+1) STORED) PARTITION BY RANGE(a)`
		if !b.PG {
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "partitioned table constraint combination is unsupported"}))
			run(fixture.Exec(`CREATE TABLE partitioned(a INT,b INT) PARTITION BY RANGE(a)`, 0))
		} else {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`CREATE TABLE leaf PARTITION OF partitioned DEFAULT`, 0))
		if b.PG {
			run(fixture.Exec(`INSERT INTO partitioned(a) VALUES(2)`, 1))
		} else {
			run(fixture.Exec(`INSERT INTO partitioned VALUES(2,3)`, 1))
		}
		for _, action := range []string{`SET EXPRESSION AS(a+2)`, `DROP EXPRESSION`} {
			q := `ALTER TABLE partitioned ALTER b ` + action
			if b.PG {
				run(fixture.Exec(q, 0))
			} else {
				run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"}))
			}
		}
		value := int64(3)
		if b.PG {
			value = 4
		}
		run(alterGeneratedRows(`SELECT * FROM partitioned`, []any{int64(2), value}))
	})
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec(`CREATE TABLE g(a INT,b INT GENERATED ALWAYS AS(a+1) VIRTUAL)`, 0))
		run(fixture.Exec(`INSERT INTO g(a) VALUES(2)`, 1))
		// Publication catalogs, replication, server-side SQL functions, physical
		// filenodes/statistics/WAL and external pg_dump/pg_restore are not native
		// mechanisms. Only PostgreSQL can exercise their release restrictions.
		if b.PG {
			run(fixture.Exec(`CREATE PUBLICATION alter_generated_pub FOR TABLE g`, 0))
			defer b.DB.ExecContext(p.Ctx, `DROP PUBLICATION alter_generated_pub`)
			d := alterGeneratedVirtualDiagnostic("SET EXPRESSION")
			d.Message += " in tables that are part of a publication"
			run(fixture.ImplicitError(`ALTER TABLE g ALTER b SET EXPRESSION AS(a+2)`, d))
			run(fixture.Exec(`DROP PUBLICATION alter_generated_pub`, 0))
			run(fixture.Exec(`CREATE FUNCTION public.alter_generated_udf(INT) RETURNS INT LANGUAGE SQL IMMUTABLE AS 'SELECT $1+1'`, 0))
			defer b.DB.ExecContext(p.Ctx, `DROP FUNCTION public.alter_generated_udf(INT)`)
			q := `ALTER TABLE g ALTER b SET EXPRESSION AS(public.alter_generated_udf(a))`
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "generation expression uses user-defined function", Detail: "Virtual generated columns that make use of user-defined functions are not yet supported."}))
		} else {
			run(fixture.ImplicitError(`CREATE PUBLICATION alter_generated_pub FOR TABLE g`, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "PUBLICATION"`, Position: "8"}))
			run(fixture.ImplicitError(`CREATE FUNCTION public.alter_generated_udf(INT) RETURNS INT LANGUAGE SQL IMMUTABLE AS 'SELECT $1+1'`, fixture.Diagnostic{Code: "0A000", Message: "CREATE syntax is unsupported", Position: "8"}))
		}
		run(alterGeneratedRows(`SELECT * FROM g`, []any{int64(2), int64(3)}))
		run(fixture.Exec(`CREATE TYPE alter_generated_row AS(a INT,b INT)`, 0))
		run(fixture.Exec(`CREATE TABLE typed_g OF alter_generated_row`, 0))
		run(fixture.Exec(`INSERT INTO typed_g VALUES(2,3)`, 1))
		run(fixture.ImplicitError(`ALTER TABLE typed_g ALTER b SET EXPRESSION AS(a+2)`, fixture.Diagnostic{Code: "55000", Message: `column "b" of relation "typed_g" is not a generated column`}))
		run(alterGeneratedRows(`SELECT * FROM typed_g`, []any{int64(2), int64(3)}))
	})
}
