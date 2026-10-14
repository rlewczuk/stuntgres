package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprStoredConsumers(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE pair AS (i integer,t text)`,
			`CREATE TYPE outer_pair AS (p pair,n integer)`,
			`CREATE TABLE stored(k integer,v pair DEFAULT ROW(7,'default')::pair,w outer_pair DEFAULT ROW(ROW(8,'nested'),9)::outer_pair,g integer GENERATED ALWAYS AS ((w).p.i) STORED,CONSTRAINT positive CHECK ((v).i>0))`,
			`CREATE INDEX field_index ON stored (((v).i)) WHERE (v).i>0`,
			`CREATE VIEW expanded_view AS SELECT k,(v).* FROM stored`,
			`CREATE VIEW constructor_view AS SELECT k,ROW((v).i,(v).t)::pair AS v FROM stored`)
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE VIEW computed_row_view AS SELECT k,(ROW((v).i,(v).t)).* FROM stored`,
			`CREATE VIEW computed_cast_view AS SELECT k,(ROW((v).i,(v).t)::pair).* FROM stored`,
			`CREATE VIEW computed_coalesce_view AS SELECT k,(COALESCE(v,ROW(77,'fallback')::pair)).* FROM stored`,
			`CREATE VIEW nested_field_view AS SELECT k,(w).p.i AS nested_i FROM stored`,
			`CREATE TYPE collision_pair AS (num_nulls integer,t text)`,
			`CREATE TABLE builtin_capture(k integer,v pair DEFAULT ROW(9,'default')::pair,c collision_pair DEFAULT ROW(7,'collision')::collision_pair,n integer DEFAULT (ROW(1,'x')::pair).num_nulls)`,
			`CREATE VIEW builtin_capture_view AS SELECT k,(v).num_nulls AS fallback,(c).num_nulls AS field,num_nulls(c) AS function,(1).num_nulls AS scalar FROM builtin_capture`,
			`CREATE TABLE whole_collision(h integer,t text)`,
			`CREATE VIEW whole_source_view AS SELECT num_nulls(h.*) AS n,h.*::text AS whole FROM whole_collision h`,
			`CREATE VIEW whole_outer_view AS SELECT h.h AS k,(SELECT num_nulls(h.*)) AS n FROM whole_collision h`,
			`CREATE VIEW whole_derived_view AS SELECT h.h AS k,(SELECT h.*::text FROM (VALUES(NULL::integer,NULL::text)) h(i,t)) AS whole FROM whole_collision h`,
			`CREATE VIEW whole_alias_field_view AS SELECT (h).renamed AS picked,renamed(h) AS fn FROM whole_collision h(renamed,label)`,
			`CREATE TABLE whole_absent_keys(k integer)`,
			`CREATE TABLE whole_absent_value(i integer,t text)`,
			`CREATE VIEW whole_absence_view AS SELECT a.k,num_nulls(h.*) AS n,h.*::text AS whole FROM whole_absent_keys a LEFT JOIN whole_absent_value h ON a.k=1`,
			`CREATE VIEW whole_zero_view AS SELECT a.k,num_nulls(z.*) AS n,z.*::text AS whole FROM whole_absent_keys a LEFT JOIN (SELECT (ROW()).*) z ON a.k=1`)
		for _, step := range []fixture.Step{fixture.Exec(`INSERT INTO builtin_capture(k) VALUES(1)`, 1), fixture.Exec(`INSERT INTO whole_collision VALUES(7,'shadow')`, 1), fixture.Exec(`INSERT INTO whole_absent_keys VALUES(1),(2)`, 2), fixture.Exec(`INSERT INTO whole_absent_value VALUES(NULL,NULL)`, 1)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		compositeExprBoth(t, p, b, c, `SELECT n FROM builtin_capture`, []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT k,fallback,field,function,scalar FROM builtin_capture_view`, []string{"k", "fallback", "field", "function", "scalar"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(7), int64(0), int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT n,whole FROM whole_source_view`, []string{"n", "whole"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "(7,shadow)"}})
		compositeExprBoth(t, p, b, c, `SELECT k,n FROM whole_outer_view`, []string{"k", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(0)}})
		compositeExprBoth(t, p, b, c, `SELECT k,whole FROM whole_derived_view`, []string{"k", "whole"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "(,)"}})
		compositeExprBoth(t, p, b, c, `SELECT picked,fn FROM whole_alias_field_view`, []string{"picked", "fn"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}})
		for _, sample := range []struct{ view, whole string }{{"whole_absence_view", "(,)"}, {"whole_zero_view", "()"}} {
			compositeExprBoth(t, p, b, c, `SELECT k,n,whole FROM `+sample.view+` ORDER BY k`, []string{"k", "n", "whole"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(0), sample.whole}, {int64(2), int64(1), nil}})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO stored(k) VALUES(1)`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(`INSERT INTO stored(k,v,w) VALUES($1,ROW($2,$3),ROW(ROW($4,$5),$6))`, 1, 2, 10, "second", 11, "inner", 12)))
		compositeExprBoth(t, p, b, c, `SELECT k,(v).i,(v).t,g FROM stored ORDER BY k`, []string{"k", "i", "t", "g"}, []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(7), "default", int64(8)}, {int64(2), int64(10), "second", int64(11)}})
		compositeExprBoth(t, p, b, c, `SELECT k,i,t FROM expanded_view ORDER BY k`, []string{"k", "i", "t"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), "default"}, {int64(2), int64(10), "second"}})
		compositeExprBoth(t, p, b, c, `SELECT v FROM constructor_view ORDER BY k`, []string{"v"}, []string{""}, [][]any{{"(7,default)"}, {"(10,second)"}})
		for _, view := range []string{"computed_row_view", "computed_cast_view", "computed_coalesce_view"} {
			columns := []string{"k", "i", "t"}
			query := `SELECT k,i,t FROM ` + view + ` ORDER BY k`
			if view == "computed_row_view" {
				columns = []string{"k", "f1", "f2"}
				query = `SELECT k,f1,f2 FROM ` + view + ` ORDER BY k`
			}
			compositeExprBoth(t, p, b, c, query, columns, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(7), "default"}, {int64(2), int64(10), "second"}})
		}
		compositeExprBoth(t, p, b, c, `SELECT k,nested_i FROM nested_field_view ORDER BY k`, []string{"k", "nested_i"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(8)}, {int64(2), int64(11)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE MATERIALIZED VIEW materialized_fields AS SELECT k,(v).* FROM stored`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE stored SET v.i=20,w.p.i=21 WHERE k=2`, 1))
		compositeExprBoth(t, p, b, c, `SELECT g FROM stored WHERE k=2`, []string{"g"}, []string{"INT4"}, [][]any{{int64(21)}})
		compositeExprBoth(t, p, b, c, `SELECT i FROM materialized_fields WHERE k=2`, []string{"i"}, []string{"INT4"}, [][]any{{int64(10)}})
		fixture.CompositeReferenceSetup(t, p, b, c, `REFRESH MATERIALIZED VIEW materialized_fields`)
		compositeExprBoth(t, p, b, c, `SELECT i FROM materialized_fields WHERE k=2`, []string{"i"}, []string{"INT4"}, [][]any{{int64(20)}})
		d := fixture.CompositeReferenceDiagnostic("23514", `new row for relation "stored" violates check constraint "positive"`)
		d.Detail = `Failing row contains (1, (0,default), ("(8,nested)",9), 8).`
		d.Schema = "public"
		d.Table = "stored"
		d.Constraint = "positive"
		fixture.CompositeReferenceError(t, p, c, `UPDATE stored SET v.i=0 WHERE k=1`, "execute", d)
		compositeExprBoth(t, p, b, c, `SELECT (v).i FROM stored ORDER BY k`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(20)}})
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE ROLE composite_reader`)
		if b.PG {
			fixture.CompositeReferenceSetup(t, p, b, c, `GRANT USAGE ON SCHEMA public TO composite_reader`)
		} else {
			// Native schema GRANT is absent. Only namespace visibility changes;
			// stored remains bootstrap-owned with a real explicit reader ACL.
			fixture.CompositeReferenceSetup(t, p, b, c, `ALTER SCHEMA public OWNER TO composite_reader`)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `GRANT SELECT ON stored TO composite_reader`, `ALTER TABLE stored ENABLE ROW LEVEL SECURITY`, `CREATE POLICY field_policy ON stored FOR SELECT TO composite_reader USING ((v).i<10)`, `SET ROLE composite_reader`)
		if b.PG {
			compositeExprBoth(t, p, b, c, `SELECT (v).* FROM stored ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "default"}})
		} else {
			fixture.CompositeReferenceError(t, p, c, `SELECT (v).* FROM stored ORDER BY k`, "execute", fixture.CompositeReferenceDiagnostic("0A000", "row-security policy expression enforcement is unsupported"))
			fixture.CompositeReferenceSetup(t, p, b, c, `RESET ROLE`, `ALTER TABLE stored DISABLE ROW LEVEL SECURITY`, `SET ROLE composite_reader`)
			compositeExprBoth(t, p, b, c, `SELECT (v).* FROM stored ORDER BY k`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "default"}, {int64(20), "second"}})
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `RESET ROLE`, `DROP POLICY field_policy ON stored`, `REVOKE SELECT ON stored FROM composite_reader`)
		if b.PG {
			fixture.CompositeReferenceSetup(t, p, b, c, `REVOKE USAGE ON SCHEMA public FROM composite_reader`)
		} else {
			fixture.CompositeReferenceSetup(t, p, b, c, `ALTER SCHEMA public OWNER TO CURRENT_USER`)
		}
		fixture.CompositeReferenceSetup(t, p, b, c, `DROP ROLE composite_reader`)
		// Existing immutable-expression fences remain authoritative with new nodes.
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE SEQUENCE volatile_child`)
		fixture.CompositeReferenceError(t, p, c, `CREATE INDEX volatile_field ON stored (((ROW(nextval('volatile_child'),1)).f1))`, "execute", fixture.CompositeReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE"))
		fixture.CompositeReferenceError(t, p, c, `CREATE TABLE volatile_generated(g bigint GENERATED ALWAYS AS ((ROW(nextval('volatile_child'))).f1) STORED)`, "execute", fixture.CompositeReferenceDiagnostic("42P17", "generation expression is not immutable"))
	})
}

func TestReferenceCompositeExprStoredFieldPruning(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		t.Run("admission_fences", func(t *testing.T) {
			fixture.CompositeReferenceSetup(t, p, b, c,
				`CREATE SEQUENCE selected_volatile`,
				`CREATE TABLE admission_sentinel(i integer)`)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO admission_sentinel VALUES(7),(8)`, 2))
			for _, sample := range []struct {
				name, query, repair, drop, code, message string
			}{
				{"selected_generated", `CREATE TABLE selected_generated(g bigint GENERATED ALWAYS AS ((ROW(nextval('selected_volatile'),1)).f1) STORED)`, `CREATE TABLE selected_generated(g bigint)`, `DROP TABLE selected_generated`, "42P17", "generation expression is not immutable"},
				{"selected_index", `CREATE INDEX selected_field ON admission_sentinel (((ROW(nextval('selected_volatile'),i)).f1))`, `CREATE INDEX selected_field ON admission_sentinel(i)`, `DROP INDEX selected_field`, "42P17", "functions in index expression must be marked IMMUTABLE"},
				{"immutable_generated", `CREATE TABLE immutable_generated(i integer,g integer GENERATED ALWAYS AS ((ROW(i,1/0)).f1) STORED)`, `CREATE TABLE immutable_generated(i integer,g integer)`, `DROP TABLE immutable_generated`, "22012", "division by zero"},
				{"immutable_index", `CREATE INDEX immutable_field ON admission_sentinel (((ROW(i,1/0)).f1))`, `CREATE INDEX immutable_field ON admission_sentinel(i)`, `DROP INDEX immutable_field`, "22012", "division by zero"},
			} {
				t.Run(sample.name, func(t *testing.T) {
					fixture.CompositeReferenceError(t, p, c, sample.query, "execute", fixture.CompositeReferenceDiagnostic(sample.code, sample.message))
					compositeExprBoth(t, p, b, c, `SELECT i FROM admission_sentinel ORDER BY i`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
					compositeExprBoth(t, p, b, c, `SELECT last_value,is_called FROM selected_volatile`, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}})
					// Reusing the exact failed object name proves no DDL publication.
					fixture.CompositeReferenceSetup(t, p, b, c, sample.repair, sample.drop)
				})
			}
			fixture.CompositeReferenceSetup(t, p, b, c, `DROP TABLE admission_sentinel`, `DROP SEQUENCE selected_volatile`)
		})
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(route.name, func(t *testing.T) {
				table := "generated_projection_" + route.name
				sequence := "skipped_" + route.name
				run := func(backend fixture.Backend, executor fixture.Executor, step fixture.Step) {
					step.Prepared = route.prepared
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
				}
				rows := func(backend fixture.Backend, executor fixture.Executor, want [][]any) {
					compositeExprBoth(t, p, backend, executor, `SELECT i,g FROM `+table+` ORDER BY i`, []string{"i", "g"}, []string{"INT4", "INT4"}, want)
					compositeExprBoth(t, p, backend, executor, `SELECT last_value,is_called FROM `+sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}})
				}
				run(b, c, fixture.Exec(`CREATE SEQUENCE `+sequence, 0))
				run(b, c, fixture.Exec(`CREATE TABLE `+table+`(i integer,g integer GENERATED ALWAYS AS ((ROW(i,nextval('`+sequence+`'))).f1) STORED)`, 0))
				run(b, c, fixture.Exec(`CREATE INDEX selected_field_`+route.name+` ON `+table+` (((ROW(i,nextval('`+sequence+`'))).f1))`, 0))
				run(b, c, fixture.Exec(`INSERT INTO `+table+`(i) VALUES(7),(8)`, 2))
				rows(b, c, [][]any{{int64(7), int64(7)}, {int64(8), int64(8)}})
				run(b, c, fixture.Exec(`UPDATE `+table+` SET i=i+10`, 2))
				current := [][]any{{int64(17), int64(17)}, {int64(18), int64(18)}}
				rows(b, c, current)
				futureWrites := func(backend fixture.Backend, executor fixture.Executor) {
					run(backend, executor, fixture.Exec(`INSERT INTO `+table+`(i) VALUES(9)`, 1))
					run(backend, executor, fixture.Exec(`UPDATE `+table+` SET i=i+10 WHERE i>=17`, 2))
					rows(backend, executor, [][]any{{int64(9), int64(9)}, {int64(27), int64(27)}, {int64(28), int64(28)}})
				}
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					for range 2 {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						rows(b, c, current)
						futureWrites(b, c)
					}
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
							t.Fatal(err)
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("actual stored-field SQL replay", err, dump.String())
						}
						pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
						rows(pg, p.Postgres, current)
						futureWrites(pg, p.Postgres)
						if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					futureWrites(b, c)
				}
				fixture.CompositeReferenceSetup(t, p, b, c, `DROP TABLE `+table, `DROP SEQUENCE `+sequence)
			})
		}
	})
}

func TestReferenceCompositeExprBoundaries(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		compositeExprSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(1,'(7,z)',NULL)`, 1))
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN domain_pair AS pair`)
		compositeExprBoth(t, p, b, c, `SELECT ARRAY[ROW(1,'x')::pair]::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`{"(1,x)"}`}})
		for _, x := range []struct {
			q      string
			pg     fixture.Step
			native fixture.Diagnostic
			base   string
		}{
			{`SELECT ARRAY[ROW(1,2)]::text AS v`, fixture.Select(`SELECT ARRAY[ROW(1,2)]::text AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{`{"(1,2)"}`}}), fixture.CompositeReferenceDiagnostic("0A000", "unsupported array element type"), "ARRAY"},
			{`SELECT ROW(1,'x')::pair=ROW(1,'x')::pair AS same`, fixture.Select(`SELECT ROW(1,'x')::pair=ROW(1,'x')::pair AS same`, []string{"same"}, []string{"BOOL"}, [][]any{{true}}), fixture.CompositeReferenceDiagnostic("0A000", "composite comparison is unsupported"), "="},
			{`SELECT NULL::pair=NULL::pair AS same`, fixture.Select(`SELECT NULL::pair=NULL::pair AS same`, []string{"same"}, []string{"BOOL"}, [][]any{{nil}}), fixture.CompositeReferenceDiagnostic("0A000", "composite comparison is unsupported"), "="},
			{`SELECT ROW(1,'x')::domain_pair=ROW(1,'x')::domain_pair AS same`, fixture.Select(`SELECT ROW(1,'x')::domain_pair=ROW(1,'x')::domain_pair AS same`, []string{"same"}, []string{"BOOL"}, [][]any{{true}}), fixture.CompositeReferenceDiagnostic("0A000", "composite comparison is unsupported"), "="},
			{`MERGE INTO holder h USING (VALUES(3,30)) s(k,i) ON h.k=s.k WHEN NOT MATCHED THEN INSERT(k,v.i) VALUES(s.k,s.i)`, fixture.Exec(`MERGE INTO holder h USING (VALUES(3,30)) s(k,i) ON h.k=s.k WHEN NOT MATCHED THEN INSERT(k,v.i) VALUES(s.k,s.i)`, 0), fixture.CompositeReferenceDiagnostic("0A000", "MERGE partial insertion targets are unsupported"), "v.i"},
			{`UPDATE holder SET (k,v)=(1,ROW(7,'z')::pair) WHERE k=1`, fixture.Exec(`UPDATE holder SET (k,v)=(1,ROW(7,'z')::pair) WHERE k=1`, 1), fixture.CompositeReferenceDiagnostic("0A000", "row assignments are unsupported"), "(k"},
		} {
			t.Run(x.q, func(t *testing.T) {
				if b.PG {
					for _, prepared := range []bool{false, true} {
						s := x.pg
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				} else {
					x.native.Position = compositeExprPosition(x.q, x.base)
					fixture.CompositeReferenceError(t, p, c, x.q, "prepare", x.native)
				}
			})
		}
		compositeExprBoth(t, p, b, c, `MERGE INTO holder h USING (VALUES(1,40)) s(k,i) ON h.k=s.k WHEN MATCHED THEN UPDATE SET v.i=s.i RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(40), "z"}})
		// INSERT RETURNING is provided by the synchronized integration base;
		// composite expansion reuses that existing projection owner.
		compositeExprBoth(t, p, b, c, `INSERT INTO holder(k,v) VALUES(2,ROW(2,'new')) RETURNING (v).*`, []string{"i", "t"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "new"}})
		d := fixture.CompositeReferenceDiagnostic("42P16", `column "v" has pseudo-type record`)
		fixture.CompositeReferenceError(t, p, c, `CREATE TYPE generic_record AS (v record)`, "execute", d)
		if !b.PG {
			compositeExprBoth(t, p, b, c, `SELECT k,v FROM holder ORDER BY k`, []string{"k", "v"}, []string{"INT4", ""}, [][]any{{int64(1), "(40,z)"}, {int64(2), "(2,new)"}, {int64(2), "(2,new)"}})
		}
	})
}
