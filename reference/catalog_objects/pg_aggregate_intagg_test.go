package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgAggregateInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, schema string) {
	t.Helper()
	for _, q := range []string{"CREATE SCHEMA " + schema, "CREATE EXTENSION intagg SCHEMA " + schema, "SET search_path TO public"} {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
	}
}
func pgAggregateIntaggIDs(t *testing.T, p *fixture.Pair, e fixture.Executor, schema string) [3]int64 {
	t.Helper()
	// Restrict independent inputs before joining; keep the actual owner and
	// namespace equalities without materializing whole-catalog cross products.
	q := `SELECT a.aggfnoid::oid::bigint,a.aggtransfn::oid::bigint,a.aggfinalfn::oid::bigint
FROM pg_aggregate a,
(SELECT oid,pronamespace FROM pg_proc WHERE proname='int_array_aggregate') p,
(SELECT oid FROM pg_namespace WHERE nspname=$1) n
WHERE p.oid=a.aggfnoid::oid AND n.oid=p.pronamespace`
	r, err := e.QueryContext(p.Ctx, q, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var ids [3]int64
	if !r.Next() {
		t.Fatal("installed aggregate missing", r.Err())
	}
	if err := r.Scan(&ids[0], &ids[1], &ids[2]); err != nil {
		t.Fatal(err)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("duplicate installed aggregate", r.Err())
	}
	for i, id := range ids {
		if id <= 0 || id > 4294967295 || id == 2333 || id == 2334 {
			t.Fatal("extension-local positive identity", ids)
		}
		for j := range i {
			if id == ids[j] {
				t.Fatal("helper/aggregate identities alias", ids)
			}
		}
	}
	return ids
}
func pgAggregateIntaggCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, schema string) [3]int64 {
	t.Helper()
	ids := pgAggregateIntaggIDs(t, p, e, schema)
	projection, _ := pgAggregateNumericProjection()
	q := fmt.Sprintf("SELECT %s FROM pg_aggregate WHERE aggfnoid::oid=%d", projection, ids[0])
	want := [][]any{{ids[0], "n", int64(0), ids[1], ids[2], int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), false, false, "r", "r", int64(0), int64(2281), int64(0), int64(0), int64(0), nil, nil}}
	pgAggregateBoth(t, p, b, e, pgAggregateDefinitionStep(q, want))
	pgAggregateBoth(t, p, b, e, fixture.Select(fmt.Sprintf("SELECT aggfnoid,aggtransfn,aggfinalfn,aggcombinefn FROM pg_aggregate WHERE aggfnoid::oid=%d", ids[0]), []string{"aggfnoid", "aggtransfn", "aggfinalfn", "aggcombinefn"}, []string{"REGPROC", "REGPROC", "REGPROC", "REGPROC"}, [][]any{{schema + ".int_array_aggregate", schema + ".int_agg_state", schema + ".int_agg_final_array", "-"}}))
	// Include every routine in the namespace, not just the expected aggregate:
	// unexpected aggregate membership must still change the asserted row bag.
	pgAggregateBoth(t, p, b, e, fixture.Select(`SELECT p.proname FROM pg_aggregate a,
(SELECT p.oid,p.proname FROM pg_proc p
JOIN (SELECT oid FROM pg_namespace WHERE nspname='`+schema+`') n ON n.oid=p.pronamespace) p
WHERE p.oid=a.aggfnoid::oid ORDER BY p.proname`, []string{"proname"}, []string{"NAME"}, [][]any{{"int_array_aggregate"}}))
	countQuery := "SELECT count(*) FROM pg_aggregate"
	if b.PG {
		countQuery += fmt.Sprintf(" WHERE aggfnoid::oid IN (%s,%d)", pgAggregateAllowlist(), ids[0])
	}
	pgAggregateBoth(t, p, b, e, fixture.Select(countQuery, []string{"count"}, []string{"INT8"}, [][]any{{int64(145)}}))
	pgAggregateBoth(t, p, b, e, fixture.Select(fmt.Sprintf(`SELECT a.aggfnoid::oid<>p.oid AS aggregate_distinct,a.aggtransfn::oid<>p.oid AS state_distinct,a.aggfinalfn::oid<>p.oid AS final_distinct
FROM (SELECT oid,pronamespace FROM pg_proc WHERE proname='int_array_enum') p,
(SELECT oid FROM pg_namespace WHERE nspname='%s') n,
(SELECT aggfnoid,aggtransfn,aggfinalfn FROM pg_aggregate WHERE aggfnoid::oid=%d) a
WHERE n.oid=p.pronamespace`, schema, ids[0]), []string{"aggregate_distinct", "state_distinct", "final_distinct"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	if !b.PG {
		pgAggregateBoth(t, p, b, e, fixture.Select("SELECT proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='"+schema+"' ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"int_array_aggregate"}, {"int_array_enum"}}))
	} else {
		pgAggregateBoth(t, p, b, e, fixture.Select(fmt.Sprintf("SELECT p.proname,n.nspname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid IN (%d,%d) ORDER BY proname", ids[1], ids[2]), []string{"proname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"int_agg_final_array", schema}, {"int_agg_state", schema}}))
	}
	return ids
}
func TestReferencePGAggregateIntaggIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pga_identity_addon", "Pga Aggregate")
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgAggregateDefinitions(t, p, b, c)
		pgAggregateInstall(t, p, b, c, "pga_identity_addon")
		ids := pgAggregateIntaggCheck(t, p, b, c, "pga_identity_addon")
		pgAggregateBoth(t, p, b, c, fixture.Select("SELECT 'pga_identity_addon.int_array_aggregate(integer)'::regprocedure::oid AS aggregate_id,'pga_identity_addon.int_agg_state(internal,integer)'::regprocedure::oid AS state_id,'pga_identity_addon.int_agg_final_array(internal)'::regprocedure::oid AS final_id", []string{"aggregate_id", "state_id", "final_id"}, []string{"OID", "OID", "OID"}, [][]any{{ids[0], ids[1], ids[2]}}))
		if !b.PG {
			// These lookup-only helpers require internal state arguments.
			// Native SQL rejects that pseudo-type before function admission;
			// do not pretend this is PostgreSQL's function-resolution result.
			for _, q := range []string{
				"SELECT pga_identity_addon.int_agg_state(NULL::internal,1)",
				"SELECT pga_identity_addon.int_agg_final_array(NULL::internal)",
			} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: fixture.Position(strings.Index(q, "internal") + 1)})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION intagg", 0))
		pgAggregateBoth(t, p, b, c, fixture.Select(fmt.Sprintf("SELECT aggfnoid::oid AS id FROM pg_aggregate WHERE aggfnoid::oid=%d", ids[0]), []string{"id"}, []string{"OID"}, [][]any{}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION intagg SCHEMA pga_identity_addon", 0))
		replacement := pgAggregateIntaggCheck(t, p, b, c, "pga_identity_addon")
		for i := range ids {
			if replacement[i] == ids[i] {
				t.Fatal("reinstall retained retired identity", ids, replacement)
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA pga_identity_addon CASCADE", 0))
		pgAggregateBoth(t, p, b, c, fixture.Select(fmt.Sprintf("SELECT aggfnoid::oid AS id FROM pg_aggregate WHERE aggfnoid::oid=%d", ids[0]), []string{"id"}, []string{"OID"}, [][]any{}))
		pgAggregateDefinitions(t, p, b, c)
		pgAggregateBoth(t, p, b, c, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname='pga_identity_addon'", []string{"nspname"}, []string{"NAME"}, [][]any{}))
		for _, id := range ids[1:] {
			pgAggregateBoth(t, p, b, c, fixture.Select("SELECT $1::oid::regproc AS retired", []string{"retired"}, []string{"REGPROC"}, [][]any{{fmt.Sprint(id)}}, id))
		}
		for _, q := range []string{`CREATE SCHEMA "Pga Aggregate"`, `CREATE EXTENSION intagg SCHEMA "Pga Aggregate"`} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		quoted := pgAggregateIntaggIDs(t, p, c, "Pga Aggregate")
		projection, _ := pgAggregateNumericProjection()
		q := fmt.Sprintf("SELECT %s FROM pg_aggregate WHERE aggfnoid::oid=%d", projection, quoted[0])
		record := [][]any{{quoted[0], "n", int64(0), quoted[1], quoted[2], int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), false, false, "r", "r", int64(0), int64(2281), int64(0), int64(0), int64(0), nil, nil}}
		pgAggregateBoth(t, p, b, c, pgAggregateDefinitionStep(q, record))
		pgAggregateBoth(t, p, b, c, fixture.Select(fmt.Sprintf("SELECT aggfnoid,aggtransfn,aggfinalfn FROM pg_aggregate WHERE aggfnoid::oid=%d", quoted[0]), []string{"aggfnoid", "aggtransfn", "aggfinalfn"}, []string{"REGPROC", "REGPROC", "REGPROC"}, [][]any{{`"Pga Aggregate".int_array_aggregate`, `"Pga Aggregate".int_agg_state`, `"Pga Aggregate".int_agg_final_array`}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DROP SCHEMA "Pga Aggregate" CASCADE`, 0))
		pgAggregateDefinitions(t, p, b, c)
	})
}

// Read retained native Rows through public database/sql; do not manufacture
// recorder events for a cursor captured before an owner-generation boundary.
func pgAggregateRetained(t *testing.T, r *sql.Rows, want [][]any, types []string) {
	t.Helper()
	defer r.Close()
	var got [][]any
	for r.Next() {
		cells := make([]any, len(types))
		ptrs := make([]any, len(types))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range cells {
			switch types[i] {
			case "REGPROC", "REGPROCEDURE", "_REGPROC", "_REGPROCEDURE":
				fixture.RegtypeRaw(t, false, v)
			default:
				fixture.AssertRawValue(t, false, types[i], v)
			}
			cells[i] = fixture.Normalize(v)
		}
		got = append(got, cells)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, want, false); !ok {
		t.Fatalf("retained rows %#v want %#v", got, want)
	}
}
