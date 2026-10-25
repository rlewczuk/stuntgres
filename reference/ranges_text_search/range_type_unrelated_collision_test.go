package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeTypeUnrelatedCollisionFeasible(t *testing.T) {
	schemas := []string{"range_feasible_0", "range_feasible_1", "range_feasible_2", "range_feasible_3", "range_feasible_4", "range_feasible_5"}
	for _, schema := range schemas {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		for _, schema := range schemas {
			run(fixture.Exec("CREATE SCHEMA "+schema, 0))
		}
		for _, q := range []string{
			"CREATE TABLE range_feasible_0.lo(k integer)",
			"CREATE TYPE feasible_span AS RANGE(SUBTYPE=regclass,SUBTYPE_OPCLASS=pg_catalog.oid_ops,MULTIRANGE_TYPE_NAME=feasible_many)",
			"CREATE TABLE feasible_saved(k integer,r feasible_span,m feasible_many,a feasible_span[])",
		} {
			run(fixture.Exec(q, 0))
		}
		var lo, firstSchema int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'range_feasible_0.lo'::regclass::oid::bigint,'range_feasible_0'::regnamespace::oid::bigint").Scan(&lo, &firstSchema); err != nil {
			t.Fatal(err)
		}
		numeric := lo + 1
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE oid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, numeric))
		span := "public.feasible_span('range_feasible_0.lo'::regclass," + fmt.Sprint(numeric) + "::oid::regclass,'[]')"
		singleton := func(endpoint string) string { return "public.feasible_span(" + endpoint + "," + endpoint + ",'[]')" }
		multi := "public.feasible_many(" + singleton("'range_feasible_0.lo'::regclass") + "," + singleton(fmt.Sprint(numeric)+"::oid::regclass") + ")"
		run(fixture.Exec("INSERT INTO feasible_saved VALUES(1,"+span+","+multi+",ARRAY["+span+"])", 1))
		text := "[range_feasible_0.lo," + fmt.Sprint(numeric) + "]"
		many := "{[range_feasible_0.lo,range_feasible_0.lo],[" + fmt.Sprint(numeric) + "," + fmt.Sprint(numeric) + "]}"
		saved := fixture.Select("SELECT k,lower(r)::oid::bigint AS lo,upper(r)::oid::bigint AS hi,r::text AS r,m::text AS m,a[1]::text AS a,isempty(r) AS empty FROM feasible_saved WHERE k=1", []string{"k", "lo", "hi", "r", "m", "a", "empty"}, []string{"INT4", "INT8", "INT8", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), lo, numeric, text, many, text, false}})
		rangeTypeRemedyRoutes(t, p, b, c, saved)
		statement, err := c.PrepareContext(p.Ctx, saved.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held, err := statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if b.PG {
			fixture.RangeConsumeHeld(t, held, saved)
			return
		}
		var script bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
			t.Fatal(err)
		}
		target := fixture.NewFixture(t, false, true)
		owner, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, s) }
		targetRun(fixture.Exec("CREATE TEMP TABLE feasible_live(k integer)", 0))
		targetRun(fixture.Exec("INSERT INTO feasible_live VALUES(7)", 1))
		// Establish the actual unrelated collision and that all retained TEMP family
		// identities precede, rather than occupy, the constrained real endpoint.
		precondition := fixture.Select("SELECT c.relnamespace::bigint=$1::bigint AS namespace_collision,c.oid::bigint<$2::bigint AS relation_before,c.reltype::bigint<$2::bigint AS rowtype_before,t.typarray::bigint<$2::bigint AS array_before FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_temp.feasible_live'::regclass", []string{"namespace_collision", "relation_before", "rowtype_before", "array_before"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, firstSchema, lo)
		targetRun(precondition)
		identityQuery := "SELECT c.oid::bigint AS rel_oid,c.reltype::bigint AS row_oid,c.relnamespace::bigint AS ns_oid,t.typarray::bigint AS array_oid FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_temp.feasible_live'::regclass"
		var tempRelation, tempRow, tempNamespace, tempArray int64
		if err := owner.QueryRowContext(target.Ctx, identityQuery).Scan(&tempRelation, &tempRow, &tempNamespace, &tempArray); err != nil {
			t.Fatal(err)
		}
		retainedIDs := fixture.Select(identityQuery, []string{"rel_oid", "row_oid", "ns_oid", "array_oid"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{tempRelation, tempRow, tempNamespace, tempArray}})
		live := fixture.Select("SELECT k FROM feasible_live", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}})
		liveStatement, err := owner.PrepareContext(target.Ctx, live.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer liveStatement.Close()
		liveRows, err := owner.QueryContext(target.Ctx, live.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer liveRows.Close()
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
			t.Fatal("unrelated collision manufactured a numeric no-fit", err)
		}
		targetRun(saved)
		targetRun(fixture.Select("SELECT 'range_feasible_0'::regnamespace::oid::bigint<>$1::bigint AS reminted_namespace,'range_feasible_0.lo'::regclass::oid::bigint=$2::bigint AS retained_endpoint", []string{"reminted_namespace", "retained_endpoint"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, firstSchema, lo))
		targetRun(live)
		fixture.RangeConsumeHeld(t, liveRows, live)
		targetRun(retainedIDs)
		preparedLive, err := liveStatement.QueryContext(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RangeConsumeHeld(t, preparedLive, live)
		targetRun(fixture.Exec("INSERT INTO feasible_saved VALUES(2,"+span+","+multi+",ARRAY["+span+"])", 1))
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(redump.Bytes())); err != nil {
			t.Fatal("feasible redump replacement", err)
		}
		targetRun(saved)
		targetRun(live)
		targetRun(retainedIDs)
		targetRun(fixture.Select("SELECT k,r::text AS r,m::text AS m,a[1]::text AS a FROM feasible_saved WHERE k=2", []string{"k", "r", "m", "a"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2), text, many, text}}))
		fixture.RangeConsumeHeld(t, held, saved)
		again, err := statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RangeConsumeHeld(t, again, saved)
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(script.Bytes(), unchanged.Bytes()) {
			t.Fatal("destination changed source", err)
		}
	})
}
