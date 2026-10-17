package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceComparisonPreparedLifecycle(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_refresh(id integer,n name)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_refresh VALUES(1,'a')", 1))
		q := "SELECT n < $1::varchar AS v FROM comparison_refresh WHERE id=$2::integer"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			value any
			want  any
		}{{"b", true}, {"a", false}, {nil, nil}, {"b", true}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.value, 1))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ALTER TABLE comparison_refresh ADD COLUMN extra text", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE comparison_refresh SET n='z' WHERE id=1", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}}, "b", 1))
		temporal := "SELECT $1::timestamp = $2::timestamptz AS v"
		ts, err := b.DB.PrepareContext(p.Ctx, temporal)
		if err != nil {
			t.Fatal(err)
		}
		defer ts.Close()
		temporalHeld := fixture.HeldStatement{Executor: b.DB, Statement: ts, SQL: temporal}
		for _, tc := range []struct {
			zone string
			want bool
		}{{"UTC", true}, {"America/New_York", false}, {"UTC", true}} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			fixture.RunStep(t, p.Ctx, temporalHeld, b.Recorder, fixture.Select(temporal, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, "2000-01-01 00:00:00", "2000-01-01 00:00:00+00"))
		}
	})
}

func comparisonStoredRows(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT id,ok FROM comparison_saved ORDER BY id", []string{"id", "ok"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}}))
	comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT eq,ordered,distinct_value FROM comparison_saved_view", []string{"eq", "ordered", "distinct_value"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}

func TestReferenceComparisonStoredControls(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE TYPE comparison_saved_enum AS ENUM ('z','a')",
			"CREATE DOMAIN comparison_saved_domain AS varchar",
			"CREATE TYPE comparison_saved_pair AS (x integer,y integer)",
			"CREATE TABLE comparison_saved(id integer,n name,v comparison_saved_domain,ok boolean DEFAULT (TIME '01:00'=INTERVAL '1 hour'),CONSTRAINT comparison_saved_check CHECK(n<=v))",
			"INSERT INTO comparison_saved VALUES(1,'a','b',DEFAULT),(2,'a','a',FALSE),(3,NULL,NULL,NULL)",
			"CREATE VIEW comparison_saved_view AS SELECT 'a'::name='a'::comparison_saved_domain AS eq,'z'::comparison_saved_enum<'a'::comparison_saved_enum AS ordered,ROW(1,NULL)::comparison_saved_pair IS DISTINCT FROM ROW(1,0)::comparison_saved_pair AS distinct_value",
		} {
			affected := int64(0)
			if len(q) >= 6 && q[:6] == "INSERT" {
				affected = 3
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, affected))
		}
		comparisonStoredRows(t, p, b)
		// Explicit native-control phase; shared SQL expectations above/below remain
		// independently specified for the real oracle, with no fake PG control API.
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			comparisonStoredRows(t, p, b)
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if dump.String() != redump.String() {
				t.Fatal("comparison logical replay changed dump")
			}
		}
	})
}
