package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Routine aliases use the existing explicit RegtypeRaw string/bytes contract;
// the generic OID catalog helper does not cover REGPROCEDURE or alias arrays.
func pgAggregateRoutineBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, s fixture.Step) {
	t.Helper()
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		switch s.Types[column] {
		case "REGPROC", "REGPROCEDURE", "_REGPROC", "_REGPROCEDURE":
			fixture.RegtypeRaw(t, b.PG, value)
			return true
		default:
			return false
		}
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range columns {
			switch s.Types[i] {
			case "REGPROC", "REGPROCEDURE", "_REGPROC", "_REGPROCEDURE":
				scan := reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
				length, lengthKnown := col.Length()
				precision, scale, decimalKnown := col.DecimalSize()
				nullable, nullableKnown := col.Nullable()
				if col.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown || b.PG && (nullable || nullableKnown) {
					t.Fatalf("routine metadata column=%d scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", i, col.ScanType(), length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown)
				}
			}
		}
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
}

func pgAggregateStoreSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	pgAggregateInstall(t, p, b, e, "pga_store")
	for _, q := range []string{
		"CREATE DOMAIN pga_store.routine_value AS regprocedure",
		"CREATE TYPE pga_store.routine_record AS (p regprocedure)",
		"CREATE TABLE pga_store.values_subject(id INT,r regproc,p pga_store.routine_value,a regproc[],c pga_store.routine_record,u regproc)",
		"CREATE TABLE pga_store.defaults_subject(id INT,r regproc DEFAULT 'pga_store.int_agg_state(internal,integer)'::regprocedure::regproc,p regprocedure DEFAULT 'pga_store.int_agg_final_array(internal)'::regprocedure)",
		"CREATE VIEW pga_store.helper_view AS SELECT 'pga_store.int_agg_state(internal,integer)'::regprocedure::regproc AS r,'pga_store.int_agg_final_array(internal)'::regprocedure AS p",
		"CREATE TABLE pga_store.source(id INT)",
	} {
		fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(`INSERT INTO pga_store.values_subject VALUES(1,'pga_store.int_agg_state','pga_store.int_agg_final_array(internal)','{pga_store.int_agg_state,pga_store.int_agg_final_array}','("pga_store.int_agg_final_array(internal)")','4294967295'),(2,NULL,NULL,NULL,NULL,NULL)`, 2))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("INSERT INTO pga_store.defaults_subject(id) VALUES(1)", 1))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("INSERT INTO pga_store.source VALUES(1)", 1))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(`CREATE MATERIALIZED VIEW pga_store.helper_mv AS SELECT id,'("pga_store.int_agg_final_array(internal)")'::pga_store.routine_record AS c FROM pga_store.source`, 1))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("UPDATE pga_store.source SET id=2", 1))
}
func pgAggregateStoreCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, mvID int64) {
	t.Helper()
	pgAggregateIntaggCheck(t, p, b, e, "pga_store")
	pgAggregateRoutineBoth(t, p, b, e, fixture.Select("SELECT id,r,p,a,(c).p AS member,u FROM pga_store.values_subject ORDER BY id", []string{"id", "r", "p", "a", "member", "u"}, []string{"INT4", "REGPROC", "REGPROCEDURE", "_REGPROC", "REGPROCEDURE", "REGPROC"}, [][]any{
		{int64(1), "pga_store.int_agg_state", "pga_store.int_agg_final_array(internal)", "{pga_store.int_agg_state,pga_store.int_agg_final_array}", "pga_store.int_agg_final_array(internal)", "4294967295"},
		{int64(2), nil, nil, nil, nil, nil},
	}))
	for _, name := range []string{"defaults_subject", "helper_view"} {
		pgAggregateRoutineBoth(t, p, b, e, fixture.Select("SELECT r,p FROM pga_store."+name, []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"pga_store.int_agg_state", "pga_store.int_agg_final_array(internal)"}}))
	}
	pgAggregateRoutineBoth(t, p, b, e, fixture.Select("SELECT id,(c).p AS p FROM pga_store.helper_mv", []string{"id", "p"}, []string{"INT4", "REGPROCEDURE"}, [][]any{{mvID, "pga_store.int_agg_final_array(internal)"}}))
	pgAggregateBoth(t, p, b, e, fixture.Select("SELECT r::oid='pga_store.int_agg_state(internal,integer)'::regprocedure::oid AS state_link,p::oid='pga_store.int_agg_final_array(internal)'::regprocedure::oid AS final_link,(c).p::oid='pga_store.int_agg_final_array(internal)'::regprocedure::oid AS composite_link FROM pga_store.values_subject WHERE id=1", []string{"state_link", "final_link", "composite_link"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
}
func pgAggregateStoreRefresh(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("INSERT INTO pga_store.defaults_subject(id) VALUES(2)", 1))
	pgAggregateBoth(t, p, b, e, fixture.Select("SELECT id,r::oid='pga_store.int_agg_state(internal,integer)'::regprocedure::oid AS state_link,p::oid='pga_store.int_agg_final_array(internal)'::regprocedure::oid AS final_link FROM pga_store.defaults_subject ORDER BY id", []string{"id", "state_link", "final_link"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, true}}))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("DELETE FROM pga_store.defaults_subject WHERE id=2", 1))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec("REFRESH MATERIALIZED VIEW pga_store.helper_mv", 0))
	pgAggregateStoreCheck(t, p, b, e, 2)
}
func pgAggregatePersistence(t *testing.T, p *fixture.Pair, b fixture.Backend, emitted *[]byte) {
	t.Helper()
	c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	pgAggregateStoreSetup(t, p, b, c)
	pgAggregateStoreCheck(t, p, b, c, 1)
	ids := pgAggregateIntaggIDs(t, p, c, "pga_store")
	// Installation is also exercised as an aggregate, not just as metadata.
	pgAggregateBoth(t, p, b, c, fixture.Select("SELECT pga_store.int_array_aggregate(v ORDER BY v) AS a FROM (VALUES(1),(2),(3)) AS x(v)", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}))
	if b.PG {
		pgAggregateStoreRefresh(t, p, b, c)
		if len(*emitted) == 0 {
			t.Fatal("PostgreSQL replay requires the actual nonempty native helper Dump")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA pga_store CASCADE", 0))
		if _, err := c.ExecContext(p.Ctx, string(*emitted)); err != nil {
			t.Fatal("actual helper identity Dump PostgreSQL replay", err)
		}
		s := fixture.MaterializedViewUnpopulated("helper_mv")
		s.Query = "SELECT * FROM pga_store.helper_mv"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		pgAggregateStoreRefresh(t, p, b, c)
		return
	}
	*emitted = pgAggregateDump(t, p)
	retained, err := b.DB.QueryContext(p.Ctx, "SELECT r,p FROM pga_store.helper_view")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	meta, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	catalogRows, err := b.DB.QueryContext(p.Ctx, pgAggregateProbe, fmt.Sprint(ids[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer catalogRows.Close()
	pgAggregateMetadata(t, catalogRows, false)
	catalogMeta, err := catalogRows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := b.DB.PrepareContext(p.Ctx, pgAggregateProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: reader, SQL: pgAggregateProbe}
	check := func() { pgAggregateStoreCheck(t, p, b, c, 1) }
	pgAggregateControlFailures(t, p, c, "pga_store", *emitted, check)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgAggregateBuiltinCount(t, p, b, c)
	fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, [][]any{}, fmt.Sprint(ids[0])))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(*emitted)); err != nil {
		t.Fatal(err)
	}
	check()
	current := pgAggregateIntaggIDs(t, p, c, "pga_store")
	raw := [][]any{{"pga_store.int_array_aggregate", "n", int64(0), "pga_store.int_agg_state", "pga_store.int_agg_final_array", "-", "-", "-", "-", "-", "-", false, false, "r", "r", int64(0), int64(2281), int64(0), int64(0), int64(0), nil, nil}}
	fixture.PgDescriptionExecutorRows(t, p, b, held, pgAggregateRawStep(b, pgAggregateProbe, raw, fmt.Sprint(current[0])))
	pgAggregateRetained(t, catalogRows, raw, pgAggregateTypes)
	pgAggregateSavedMetadata(t, catalogMeta, false)
	// Restore semantics, not incidental view casts or SQL formatting, must
	// survive another dump cycle. Replay this actual redump below.
	redump := pgAggregateDump(t, p)
	pgAggregateRetained(t, retained, [][]any{{"pga_store.int_agg_state", "pga_store.int_agg_final_array(internal)"}}, []string{"REGPROC", "REGPROCEDURE"})
	for i, typ := range []string{"REGPROC", "REGPROCEDURE"} {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal("retained helper metadata", meta[i])
		}
	}
	pgAggregateStoreRefresh(t, p, b, c)
	// Only genuine live TEMP identities survive replacement. Ordinary receiver
	// tables are discarded and cannot establish an occupied-ID witness.
	// Measure collisions against the current owners that produced redump.
	receiving := fixture.NewFixture(t, false, true)
	target := fixture.SequenceCoreConn(t, receiving.Ctx, receiving.Memory)
	collisions := make(map[int64]string)
	maxID := current[0]
	for _, id := range current {
		if id > maxID {
			maxID = id
		}
	}
	for i := range 128 {
		name := fmt.Sprintf("pga_displacement_%d", i)
		fixture.RunStep(t, receiving.Ctx, target, receiving.MemoryRecord, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 41", 0))
		var id int64
		if err := target.QueryRowContext(receiving.Ctx, "SELECT 'pg_temp."+name+"'::regclass::oid::bigint").Scan(&id); err != nil {
			t.Fatal(err)
		}
		collisions[id] = name
		if id >= maxID {
			break
		}
	}
	for _, id := range current {
		name := collisions[id]
		if name == "" {
			t.Fatal("fixture lacks an observed retained aggregate/helper collision", id, collisions)
		}
		fixture.RunStep(t, receiving.Ctx, target, receiving.MemoryRecord, fixture.Select("SELECT nextval('pg_temp."+name+"') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(41)}}))
	}
	if err := stuntgres.Restore(receiving.Ctx, receiving.Native, bytes.NewReader(redump)); err != nil {
		t.Fatal(err)
	}
	targetBackend := fixture.Backend{Name: "receiving", DB: receiving.Memory, Recorder: receiving.MemoryRecord}
	receiver := pgAggregateIntaggIDs(t, receiving, target, "pga_store")
	for i, id := range current {
		if receiver[i] == id {
			t.Fatal("genuinely collided receiving identity was not remapped", current, receiver)
		}
		name := collisions[id]
		fixture.RunStep(t, receiving.Ctx, target, receiving.MemoryRecord, fixture.Select("SELECT 'pg_temp."+name+"'::regclass::oid::bigint AS id,nextval('pg_temp."+name+"') AS v", []string{"id", "v"}, []string{"INT8", "INT8"}, [][]any{{id, int64(42)}}))
	}
	pgAggregateStoreCheck(t, receiving, targetBackend, target, 1)
	pgAggregateStoreRefresh(t, receiving, targetBackend, target)
	// A target's unrelated user numeric identity must not become an owned alias.
	pgAggregateBoth(t, receiving, targetBackend, target, fixture.Select("SELECT u FROM pga_store.values_subject WHERE id=1", []string{"u"}, []string{"REGPROC"}, [][]any{{"4294967295"}}))
}
func TestReferencePGAggregateIdentityPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pga_store")
	var emitted []byte
	pgAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) { pgAggregatePersistence(t, p, b, &emitted) })
}
