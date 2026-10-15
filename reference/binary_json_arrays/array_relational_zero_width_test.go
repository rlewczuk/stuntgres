package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalZeroWidthComposite(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TYPE ar_zero AS ()",
			"CREATE DOMAIN ar_zero_domain AS ar_zero",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, typ := range []string{"ar_zero", "ar_zero_domain"} {
			t.Run(typ, func(t *testing.T) {
				for _, tc := range []struct {
					literal string
					rows    [][]any
				}{
					{"'{(),NULL}'", [][]any{{int64(1)}, {int64(2)}}},
					{"'{}'", [][]any{}},
					{"NULL", [][]any{}},
				} {
					expr := tc.literal + "::" + typ + "[]"
					s := fixture.Select("SELECT n FROM unnest("+expr+") WITH ORDINALITY AS u(n) ORDER BY n", []string{"n"}, []string{"INT8"}, tc.rows)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonTypeMetadataCheck(t, rows, b.PG, s.Columns, s.Types) }
					arrayRelationalBoth(t, p, b, c, s)
					// No exposed composite fields or ordinality: row presence must
					// still be independent of the function's zero-column span.
					markers := make([][]any, len(tc.rows))
					for i := range markers {
						markers[i] = []any{int64(7)}
					}
					plain := fixture.Select("SELECT 7 AS marker FROM unnest("+expr+") AS u", []string{"marker"}, []string{"INT4"}, markers)
					plain.Bag = true
					arrayRelationalBoth(t, p, b, c, plain)
				}
				for _, source := range []string{
					"unnest('{(),NULL,()}'::" + typ + "[],ARRAY[10])",
					"unnest(ARRAY[10],'{(),NULL,()}'::" + typ + "[])",
					"ROWS FROM (unnest('{(),NULL,()}'::" + typ + "[]),unnest(ARRAY[10]))",
					"ROWS FROM (unnest('{(),NULL,()}'::" + typ + "[],ARRAY[10]))",
				} {
					arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,n FROM "+source+" WITH ORDINALITY AS u(x,n) ORDER BY n", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}, {nil, int64(2)}, {nil, int64(3)}}))
				}
				for _, source := range []string{
					"unnest('{()}'::" + typ + "[],ARRAY[10,20,30])",
					"ROWS FROM (unnest('{()}'::" + typ + "[],ARRAY[10,20,30]))",
				} {
					arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,n FROM "+source+" WITH ORDINALITY AS u(x,n) ORDER BY n", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}, {int64(20), int64(2)}, {int64(30), int64(3)}}))
				}
				arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT n FROM unnest('{(),NULL,()}'::"+typ+"[],'{NULL}'::"+typ+"[]) WITH ORDINALITY AS u(n) ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
				for _, source := range []string{
					"unnest('{}'::" + typ + "[],ARRAY[10])",
					"unnest(NULL::" + typ + "[],ARRAY[10])",
				} {
					arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,n FROM "+source+" WITH ORDINALITY AS u(x,n) ORDER BY n", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(10), int64(1)}}))
				}
				arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT x,n FROM unnest('{}'::"+typ+"[],NULL::int[]) WITH ORDINALITY AS u(x,n)", []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{}))
				q := "SELECT n FROM unnest($1::" + typ + "[]) WITH ORDINALITY AS u(n) ORDER BY n"
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				for _, tc := range []struct {
					value any
					rows  [][]any
				}{
					{"{(),NULL}", [][]any{{int64(1)}, {int64(2)}}},
					{"{}", [][]any{}},
					{nil, [][]any{}},
					{"{NULL,()}", [][]any{{int64(1)}, {int64(2)}}},
				} {
					s := fixture.Select(q, []string{"n"}, []string{"INT8"}, tc.rows, tc.value)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
				}
			})
		}
		for _, q := range []string{
			"CREATE TABLE ar_zero_source(id INT,a ar_zero[],d ar_zero_domain[])",
			"CREATE VIEW ar_zero_view AS SELECT id,n FROM ar_zero_source,unnest(a,d) WITH ORDINALITY AS u(n)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ar_zero_source VALUES (1,'{(),NULL}','{NULL,(),NULL}'),(2,'{}',NULL),(3,NULL,'{()}')", 3))
		check := fixture.Select("SELECT id,n FROM ar_zero_view ORDER BY id,n", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(2)}, {int64(1), int64(3)}, {int64(3), int64(1)}})
		arrayRelationalBoth(t, p, b, c, check)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			retained, err := c.QueryContext(p.Ctx, check.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			arrayRelationalBoth(t, p, b, c, check)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if dump.String() != redump.String() {
				t.Fatalf("zero-field stored replay changed dump\nfirst=%s\nsecond=%s", dump.String(), redump.String())
			}
			if p.Postgres != nil {
				// Replay the actual native-emitted SQL against PostgreSQL too.
				// Remove types as well as consumers before its own backend scenario.
				defer func() {
					for _, q := range []string{"DROP VIEW IF EXISTS ar_zero_view", "DROP TABLE IF EXISTS ar_zero_source", "DROP DOMAIN IF EXISTS ar_zero_domain", "DROP TYPE IF EXISTS ar_zero"} {
						if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual PostgreSQL replay of zero-field emitted SQL", err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(check))
			}
		}
	})
}
