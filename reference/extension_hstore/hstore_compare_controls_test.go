package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const hstoreComparisonSchema = "hs_cmp_provider"

func hstoreComparisonControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA " + hstoreComparisonSchema,
		"CREATE EXTENSION hstore SCHEMA " + hstoreComparisonSchema,
		"SET search_path=public," + hstoreComparisonSchema,
	} {
		hstoreRun(t, p, b, c, fixture.Exec(q, 0))
	}
	hstoreComparisonTable(t, p, b, c)
	for _, q := range []string{
		"CREATE INDEX hs_cmp_btree ON hs_cmp(v " + hstoreComparisonSchema + ".btree_hstore_ops)",
		"CREATE INDEX hs_cmp_hash ON hs_cmp USING hash(v " + hstoreComparisonSchema + ".hash_hstore_ops)",
		"CREATE INDEX hs_cmp_expression ON hs_cmp(hstore_hash(v))",
		"CREATE TABLE hs_cmp_diag_empty(v hstore)",
		"CREATE VIEW hs_cmp_view AS SELECT id,v='b=>2,a=>1'::hstore AS equal,hstore_cmp(v,'a=>1,b=>2'::hstore) AS cmp,hstore_hash(v) AS hash,hstore_hash_extended(v,1::bigint) AS extended,v IN ('a=>1,b=>2'::hstore,NULL::hstore) AS member,CASE v WHEN 'a=>1,b=>2'::hstore THEN true ELSE false END AS chosen,NULLIF(v,'a=>1,b=>2'::hstore) IS NULL AS nullified FROM hs_cmp",
		"CREATE TABLE hs_cmp_stored(v hstore DEFAULT 'a=>1',c integer DEFAULT hstore_cmp('a=>bc'::hstore,'ab=>c'::hstore),CONSTRAINT hs_cmp_check CHECK (v<>'forbidden=>value'::hstore))",
	} {
		hstoreRun(t, p, b, c, fixture.Exec(q, 0))
	}
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_cmp_stored DEFAULT VALUES", 1))
	hstoreComparisonUniqueSetup(t, p, b, c)
}

func hstoreComparisonScalarChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT hstore_cmp($1::hstore,$2::hstore) AS cmp,hstore_hash($1::hstore) AS hash,hstore_hash_extended($1::hstore,1::bigint) AS extended,$1::hstore=$2::hstore AS eq"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		left, right any
		row         []any
	}{
		{"a=>bc", "ab=>c", []any{int64(-1), int64(-1685126002), int64(1277871365356202019), false}},
		{"b=>2,a=>1", "a=>1,b=>2", []any{int64(0), int64(-1077040027), int64(7681866774737967775), true}},
		{nil, "a=>NULL", []any{nil, nil, nil, nil}},
		{"", nil, []any{nil, int64(-1524351049), int64(3044001184559810835), nil}},
	} {
		s := fixture.Select(q, []string{"cmp", "hash", "extended", "eq"}, []string{"INT4", "INT4", "INT8", "BOOL"}, [][]any{tc.row}, tc.left, tc.right)
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
		hstoreRun(t, p, b, held, s)
	}
}

func hstoreComparisonControlChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreRun(t, p, b, c, fixture.Exec("SET search_path=public,"+hstoreComparisonSchema, 0))
	for _, s := range hstoreComparisonCatalogChecks(hstoreComparisonSchema) {
		hstoreRun(t, p, b, c, s)
	}
	for _, s := range hstoreComparisonConsumerChecks() {
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,equal,cmp,member,chosen,nullified FROM hs_cmp_view ORDER BY id", []string{"id", "equal", "cmp", "member", "chosen", "nullified"}, []string{"INT4", "BOOL", "INT4", "BOOL", "BOOL", "BOOL"}, [][]any{
			{int64(1), true, int64(0), true, true, true}, {int64(2), true, int64(0), true, true, true},
			{int64(3), nil, nil, nil, false, true}, {int64(4), false, int64(-1), nil, false, false},
			{int64(5), false, int64(-1), nil, false, false}, {int64(6), false, int64(-1), nil, false, false},
			{int64(7), false, int64(1), nil, false, false}, {int64(8), nil, nil, nil, false, true},
		}),
		fixture.Select("SELECT id,hash,extended FROM hs_cmp_view WHERE id IN (1,2,3,4,8) ORDER BY id", []string{"id", "hash", "extended"}, []string{"INT4", "INT4", "INT8"}, [][]any{
			{int64(1), int64(-1077040027), int64(7681866774737967775)}, {int64(2), int64(-1077040027), int64(7681866774737967775)}, {int64(3), nil, nil}, {int64(4), int64(-1524351049), int64(3044001184559810835)}, {int64(8), nil, nil},
		}),
		fixture.Select("SELECT v='a=>1'::hstore AS equal,c FROM hs_cmp_stored", []string{"equal", "c"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(-1)}}),
		fixture.Select("SELECT 'b=>2,a=>1'::"+hstoreComparisonSchema+".hstore OPERATOR("+hstoreComparisonSchema+".=) 'a=>1,b=>2'::"+hstoreComparisonSchema+".hstore AS eq,"+hstoreComparisonSchema+".hstore_cmp('a=>bc'::"+hstoreComparisonSchema+".hstore,'ab=>c'::"+hstoreComparisonSchema+".hstore) AS cmp", []string{"eq", "cmp"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(-1)}}),
	} {
		hstoreRun(t, p, b, c, s)
		hstoreRun(t, p, b, c, fixture.Prepared(s))
	}
	hstoreComparisonScalarChecks(t, p, b, c)
	hstoreComparisonHeldConsumer(t, p, b, c)
	hstoreComparisonUniqueChecks(t, p, b, c)
	hstoreComparisonDiagnosticChecks(t, p, b, c)
}

func hstoreComparisonRetainedWant() [][]any {
	return [][]any{{int64(4), ""}, {int64(6), `"a"=>""`}, {int64(5), `"a"=>NULL`}, {int64(1), `"a"=>"1", "b"=>"2"`}, {int64(2), `"a"=>"1", "b"=>"2"`}, {int64(7), `"a"=>"b"`}, {int64(3), nil}, {int64(8), nil}}
}

func hstoreComparisonRetain(t *testing.T, p *fixture.Pair, db *sql.DB) (*sql.Rows, []*sql.ColumnType) {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT id,v FROM hs_cmp ORDER BY v,id")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	hstoreColumns(t, false, columns, []string{"id", "v"}, []bool{false, true})
	return rows, columns
}

func hstoreComparisonSettings(t *testing.T, p *fixture.Pair, c *sql.Conn) [2]string {
	t.Helper()
	var settings [2]string
	for i, name := range []string{"search_path", "extra_float_digits"} {
		if err := c.QueryRowContext(p.Ctx, "SHOW "+name).Scan(&settings[i]); err != nil {
			t.Fatal(err)
		}
	}
	return settings
}

func hstoreComparisonReplay(t *testing.T, p *fixture.Pair, dump []byte) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clean := "DROP SCHEMA IF EXISTS " + hstoreComparisonSchema + " CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if _, err := c.ExecContext(p.Ctx, clean); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := c.ExecContext(p.Ctx, clean); err != nil {
			t.Error(err)
		}
	}()
	if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
		t.Fatalf("actual hstore comparison Dump SQL replay: %v", err)
	}
	pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
	hstoreComparisonControlChecks(t, p, pg, c)
}

func hstoreComparisonControlPhase(t *testing.T, phase string) {
	fixture.SchemaReferenceCleanup(t, hstoreComparisonSchema)
	hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreComparisonControlSetup(t, p, b, c)
		hstoreComparisonControlChecks(t, p, b, c)
		// TEXT wire parameters keep their public prepared signature across
		// genuine replacement of the dynamically allocated hstore type.
		q := "SELECT hstore_cmp($1::text::hstore,$2::text::hstore) AS cmp,hstore_hash($1::text::hstore) AS hash,hstore_hash_extended($1::text::hstore,1::bigint) AS extended,$1::text::hstore=$2::text::hstore AS eq"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		checkHeld := func() {
			for _, tc := range []struct {
				left, right any
				row         []any
			}{
				{"a=>bc", "ab=>c", []any{int64(-1), int64(-1685126002), int64(1277871365356202019), false}},
				{"b=>2,a=>1", "a=>1,b=>2", []any{int64(0), int64(-1077040027), int64(7681866774737967775), true}},
				{nil, "a=>1", []any{nil, nil, nil, nil}},
			} {
				s := fixture.Select(q, []string{"cmp", "hash", "extended", "eq"}, []string{"INT4", "INT4", "INT8", "BOOL"}, [][]any{tc.row}, tc.left, tc.right)
				hstoreRun(t, p, b, held, s)
			}
		}
		checkHeld()
		if b.PG {
			// Native controls have no PG analogue. Genuine SQL replacement still
			// exercises retained prepared consumers and the same qualified graph.
			for _, sql := range []string{"DROP VIEW hs_cmp_view", "DROP TABLE hs_cmp", "DROP TABLE hs_cmp_unique", "DROP TABLE hs_cmp_stored", "DROP TABLE hs_cmp_diag_empty", "DROP EXTENSION hstore", "DROP SCHEMA " + hstoreComparisonSchema} {
				hstoreRun(t, p, b, c, fixture.Exec(sql, 0))
			}
			hstoreComparisonControlSetup(t, p, b, c)
			hstoreComparisonControlChecks(t, p, b, c)
			checkHeld()
			return
		}
		dump := hstoreDump(t, p, p.Native)
		old, columns := hstoreComparisonRetain(t, p, b.DB)
		settings := hstoreComparisonSettings(t, p, c)
		if phase == "same" {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			if got := hstoreComparisonSettings(t, p, c); got != settings {
				t.Fatalf("Restore changed caller settings %v -> %v", settings, got)
			}
			hstoreComparisonControlChecks(t, p, b, c)
			checkHeld()
			hstoreConsume(t, old, columns, hstoreComparisonRetainedWant())
			hstoreComparisonReplay(t, p, dump)
			return
		}
		target := fixture.NewFixture(t, false, true)
		tc, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tc.Close()
		tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		// Public TEMP objects occupy real allocator identities and survive the
		// database replacement; don't assume independent allocators differ.
		for i := range 24 {
			hstoreRun(t, target, tb, tc, fixture.Exec("CREATE TEMP TABLE hs_cmp_occupied_"+strconv.Itoa(i)+"(v integer)", 0))
		}
		hstoreRun(t, target, tb, tc, fixture.Exec("INSERT INTO hs_cmp_occupied_0 VALUES (73)", 1))
		var retainedID int64
		if err := tc.QueryRowContext(target.Ctx, "SELECT oid::bigint FROM pg_class WHERE oid='pg_temp.hs_cmp_occupied_0'::regclass").Scan(&retainedID); err != nil {
			t.Fatal(err)
		}
		hstoreRun(t, target, tb, tc, fixture.Exec("CREATE EXTENSION hstore", 0))
		targetSettings := hstoreComparisonSettings(t, target, tc)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		if got := hstoreComparisonSettings(t, target, tc); got != targetSettings {
			t.Fatalf("fresh Restore changed settings %v -> %v", targetSettings, got)
		}
		hstoreComparisonControlChecks(t, target, tb, tc)
		hstoreRun(t, target, tb, tc, fixture.Select("SELECT oid::bigint AS id FROM pg_class WHERE oid='pg_temp.hs_cmp_occupied_0'::regclass", []string{"id"}, []string{"INT8"}, [][]any{{retainedID}}))
		hstoreRun(t, target, tb, tc, fixture.Select("SELECT v FROM hs_cmp_occupied_0", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		hstoreConsume(t, old, columns, hstoreComparisonRetainedWant())
		if phase == "fresh" {
			return
		}
		before := hstoreDump(t, target, target.Native)
		frozen, metadata := hstoreComparisonRetain(t, target, target.Memory)
		targetSettings = hstoreComparisonSettings(t, target, tc)
		identities := hstoreComparisonIdentities(t, target, tc)
		if phase == "failed" {
			bad, want := hstoreStagedSQL()
			fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &want)
		} else {
			ctx, cancel := context.WithCancel(target.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled Restore: %v", err)
			}
		}
		if got := hstoreComparisonSettings(t, target, tc); got != targetSettings {
			t.Fatalf("rejected Restore changed settings %v -> %v", targetSettings, got)
		}
		if got := hstoreComparisonIdentities(t, target, tc); !reflect.DeepEqual(got, identities) {
			t.Fatalf("rejected Restore changed member identities %v -> %v", identities, got)
		}
		hstoreComparisonControlChecks(t, target, tb, tc)
		hstoreConsume(t, frozen, metadata, hstoreComparisonRetainedWant())
		if after := hstoreDump(t, target, target.Native); !bytes.Equal(before, after) {
			t.Fatalf("rejected Restore changed dump\nbefore=%s\nafter=%s", before, after)
		}
	})
}

func TestReferenceHstoreComparisonControls(t *testing.T) {
	for _, phase := range []string{"same", "fresh", "failed", "canceled"} {
		t.Run(phase, func(t *testing.T) { hstoreComparisonControlPhase(t, phase) })
	}
}
