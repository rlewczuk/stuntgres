package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"sa_owner", "sa_table", "sa_schema"})
	fixture.SchemaReferenceCleanup(t, "sa_destination")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, q := range []string{"CREATE ROLE sa_owner", "CREATE ROLE sa_table", "CREATE ROLE sa_schema"} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO sa_table", 0))
	}
	native := p.Backends()[0]
	conn, err := native.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, native.Recorder, step) }
	for _, q := range []string{"CREATE SCHEMA sa_destination AUTHORIZATION sa_schema", "GRANT USAGE ON SCHEMA sa_destination TO sa_owner", "CREATE TABLE public.sa_input(a INT,hole INT,b INT)", "ALTER TABLE sa_input DROP COLUMN hole", "CREATE STATISTICS public.sa_before ON a,b FROM sa_input", "CREATE STATISTICS public.sa_expression (ndistinct) ON a,(b+1) FROM sa_input", "CREATE STATISTICS public.sa_max (ndistinct) ON a,b FROM sa_input", "CREATE STATISTICS public.sa_zero (ndistinct) ON a,b FROM sa_input"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO sa_input VALUES(1,10),(1,10),(2,20),(2,20)", 4))
	run(fixture.Exec("ANALYZE sa_input", 0))
	run(fixture.Exec("INSERT INTO sa_input VALUES(3,30)", 1))
	for _, q := range []string{"ALTER STATISTICS sa_before RENAME TO sa_after", "ALTER STATISTICS sa_after SET SCHEMA sa_destination", "ALTER STATISTICS sa_expression SET SCHEMA sa_destination", "ALTER STATISTICS sa_max SET SCHEMA sa_destination", "ALTER STATISTICS sa_zero SET SCHEMA sa_destination", "ALTER STATISTICS sa_destination.sa_after OWNER TO sa_owner", "ALTER STATISTICS sa_destination.sa_expression OWNER TO sa_owner", "ALTER STATISTICS sa_destination.sa_max OWNER TO sa_owner", "ALTER STATISTICS sa_destination.sa_zero OWNER TO sa_owner", "ALTER STATISTICS sa_destination.sa_max SET STATISTICS 10000", "ALTER STATISTICS sa_destination.sa_zero SET STATISTICS 0", "ALTER TABLE sa_input OWNER TO sa_table"} {
		run(fixture.Exec(q, 0))
	}
	stale := func(pg bool) fixture.Step {
		return ndistinctRows(pg, []any{"sa_after", false, `{"1, 3": 2}`}, []any{"sa_expression", false, `{"1, -1": 2}`}, []any{"sa_max", false, `{"1, 3": 2}`}, []any{"sa_zero", false, `{"1, 3": 2}`})
	}
	run(stale(false))
	run(statisticsAlterExpressionSummary("sa_expression"))
	dependencies := ndistinctRawResult(false, fixture.Select("SELECT stxddependencies FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='sa_after'", []string{"stxddependencies"}, []string{"PG_DEPENDENCIES"}, [][]any{{`{"1 => 3": 1.000000, "3 => 1": 1.000000}`}}), 0)
	run(dependencies)
	mcvAssert(t, ndistinctScenario{run: run, conn: conn, pair: p}, "sa_after", mcvExpected{"1,10", .5, .25}, mcvExpected{"2,20", .5, .25})
	old, err := p.Native.QueryContext(p.Ctx, "SELECT stxdndistinct FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='sa_after'")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err = stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Len() == 0 || strings.Contains(saved.String(), "CREATE STATISTICS public.sa_before") {
		t.Fatal("obsolete dump identity")
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			executor := fixture.Executor(b.DB)
			recorder := b.Recorder
			if b.PG {
				replay := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer replay.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := replay.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("actual emitted definition SQL", err)
				}
				if _, err := replay.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
					t.Fatal(err)
				}
				executor = replay
			} else {
				driver := stuntgres.NewDriver()
				connector, err := driver.OpenConnector(t.Name() + "-collision")
				if err != nil {
					t.Fatal(err)
				}
				other := sql.OpenDB(connector)
				defer other.Close()
				if _, err = other.ExecContext(p.Ctx, "CREATE ROLE sa_schema; CREATE ROLE sa_table; CREATE ROLE sa_owner; CREATE TABLE collision(v INT)"); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(p.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
				recorded, rec := fixture.RecordedNativePool(t, connector)
				fixture.RunStep(t, p.Ctx, recorded, rec, stale(false))
				fixture.RunStep(t, p.Ctx, recorded, rec, expressionJoint(false, "sa_expression", `{"1, -1": 2}`, 1))
				fixture.RunStep(t, p.Ctx, recorded, rec, statisticsAlterExpressionSummary("sa_expression"))
				fixture.RunStep(t, p.Ctx, recorded, rec, dependencies)
				factsConn, err := recorded.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer factsConn.Close()
				mcvAssert(t, ndistinctScenario{conn: factsConn, pair: p}, "sa_after", mcvExpected{"1,10", .5, .25}, mcvExpected{"2,20", .5, .25})
				var again bytes.Buffer
				if err = stuntgres.Dump(p.Ctx, other, &again); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved.Bytes(), again.Bytes()) {
					t.Fatal("metadata/state redump changed", saved.String(), again.String())
				}
				executor = recorded
				recorder = rec
			}
			fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select("SELECT s.stxname,n.nspname,r.rolname,s.stxkeys,s.stxkind,s.stxstattarget FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace JOIN pg_roles r ON r.oid=s.stxowner ORDER BY s.stxname", []string{"stxname", "nspname", "rolname", "stxkeys", "stxkind", "stxstattarget"}, []string{"NAME", "NAME", "NAME", "INT2VECTOR", "_CHAR", "INT2"}, [][]any{{"sa_after", "sa_destination", "sa_owner", "1 3", "{d,f,m}", nil}, {"sa_expression", "sa_destination", "sa_owner", "1", "{d,e}", nil}, {"sa_max", "sa_destination", "sa_owner", "1 3", "{d}", int64(10000)}, {"sa_zero", "sa_destination", "sa_owner", "1 3", "{d}", int64(0)}}))
		})
	}
	if !old.Next() {
		t.Fatal(old.Err())
	}
	var value string
	if err = old.Scan(&value); err != nil || value != `{"1, 3": 2}` {
		t.Fatal("old detached facts", value, err)
	}
	if meta[0].DatabaseTypeName() != "PG_NDISTINCT" {
		t.Fatal(meta[0])
	}
}
