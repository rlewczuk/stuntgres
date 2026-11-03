package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

var indexStatNames = []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname", "idx_scan", "last_idx_scan", "idx_tup_read", "idx_tup_fetch"}
var indexStatTypes = []string{"OID", "OID", "NAME", "NAME", "NAME", "INT8", "TIMESTAMPTZ", "INT8", "INT8"}

const indexStatSelect = "SELECT * FROM pg_catalog.pg_stat_all_indexes WHERE indexrelname=$1"

type indexStatRow struct {
	Parent, Index       int64
	Schema, Table, Name string
	Scans               int64
	Last                sql.NullTime
	Entries, Fetches    int64
}

func (r indexStatRow) equal(other indexStatRow) bool {
	// TIMESTAMPTZ equality is instant equality, not time.Location pointer
	// identity: native export constructs the captured output zone per value.
	return r.Parent == other.Parent && r.Index == other.Index &&
		r.Schema == other.Schema && r.Table == other.Table && r.Name == other.Name &&
		r.Scans == other.Scans && r.Entries == other.Entries && r.Fetches == other.Fetches &&
		r.Last.Valid == other.Last.Valid && (!r.Last.Valid || r.Last.Time.Equal(other.Last.Time))
}

func indexStatRead(ctx context.Context, c *sql.Conn, name string) (indexStatRow, error) {
	var r indexStatRow
	err := c.QueryRowContext(ctx, indexStatSelect, name).Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
	return r, err
}
func indexStatGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	r, err := indexStatRead(p.Ctx, c, name)
	if err != nil {
		t.Fatal(name, err)
	}
	return r
}
func indexStatWant(t *testing.T, r indexStatRow, scans, entries, fetches int64) {
	t.Helper()
	if r.Scans != scans || r.Entries != entries || r.Fetches != fetches || (scans == 0) == r.Last.Valid {
		t.Fatalf("index statistics %+v want %d/%d/%d", r, scans, entries, fetches)
	}
}
func indexStatAwait(t *testing.T, p *fixture.Pair, b fixture.Backend, name string, scans, entries, fetches int64) indexStatRow {
	t.Helper()
	probe := fixture.IdentityConnection(t, p.Ctx, b.DB)
	fixture.StatisticsExec(t, p, b, probe, "SET stats_fetch_consistency=none", 0)
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var last indexStatRow
	for {
		var err error
		last, err = indexStatRead(ctx, probe, name)
		if err != nil {
			t.Fatal("index publication", name, last, err)
		}
		if last.Scans == scans && last.Entries == entries && last.Fetches == fetches {
			indexStatWant(t, last, scans, entries, fetches)
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("index publication %s last=%+v want=%d/%d/%d: %v", name, last, scans, entries, fetches, ctx.Err())
		case <-tick.C:
		}
	}
}
func indexStatCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table, index string) {
	t.Helper()
	for _, step := range []fixture.Step{fixture.Exec("CREATE TABLE "+table+"(n integer,payload text)", 0), fixture.Exec("ALTER TABLE "+table+" SET(autovacuum_enabled=false)", 0), fixture.Exec("INSERT INTO "+table+" VALUES(2,'a'),(1,'x'),(2,'b'),(NULL,NULL),(3,'z')", 5), fixture.Exec("CREATE INDEX "+index+" ON "+table+" USING hash(n)", 0)} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
}
func indexStatPrefer(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"SET enable_seqscan=off", "SET enable_bitmapscan=off", "SET enable_indexonlyscan=off"} {
		fixture.StatisticsExec(t, p, b, c, q, 0)
	}
}
func indexStatPlan(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "EXPLAIN (COSTS OFF) "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(lines, "\n")
	t.Logf("observed access plan:\n%s", text)
	return text
}
func indexStatSimple(t *testing.T, b fixture.Backend, method string) bool {
	t.Helper()
	if b.PG {
		return strings.Contains(method, "Index Scan using ")
	}
	if !strings.Contains(method, "Access Method: Hash Candidates") {
		t.Fatal("native positive witness did not choose genuine hash", method)
	}
	return true
}
func indexStatPayloads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want []string, args ...any) {
	t.Helper()
	rows := make([][]any, 0, len(want))
	for _, s := range want {
		rows = append(rows, []any{s})
	}
	step := fixture.Select(q, []string{"payload"}, []string{"TEXT"}, rows, args...)
	step.Bag = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
}
func indexStatMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 9 {
		t.Fatal("column count", len(columns))
	}
	for i, c := range columns {
		if c.Name() != indexStatNames[i] || c.DatabaseTypeName() != indexStatTypes[i] {
			t.Fatal("typed view metadata", i, c.Name(), c.DatabaseTypeName())
		}
		nullable, known := c.Nullable()
		if pg {
			if nullable || known {
				t.Fatal("pq invented nullability", i)
			}
		} else if !nullable || !known {
			t.Fatal("native view nullable metadata", i)
		}
		if _, ok := c.Length(); ok {
			t.Fatal("unexpected length", i)
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("unexpected precision", i)
		}
		want := reflect.TypeFor[int64]()
		if i < 5 {
			if pg {
				want = reflect.TypeFor[any]()
			} else if i >= 2 {
				want = reflect.TypeFor[string]()
			}
		}
		if i == 6 {
			want = reflect.TypeFor[time.Time]()
		}
		if c.ScanType() != want {
			t.Fatal("adapter scan type", i, c.ScanType(), want)
		}
	}
}
func indexStatMutationDiagnostic(op string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[op]
	verb := map[string]string{"INSERT": "insert into", "UPDATE": "update", "DELETE": "delete from"}[op]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: fmt.Sprintf("cannot %s view %q", verb, "pg_stat_all_indexes"), Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + op + " trigger or an unconditional ON " + op + " DO INSTEAD rule."}
}

func indexStatAwaitPositive(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		r, err := indexStatRead(ctx, c, name)
		if err != nil {
			t.Fatal("positive publication", r, err)
		}
		if r.Scans > 0 && r.Last.Valid {
			return r
		}
		select {
		case <-ctx.Done():
			t.Fatal("positive publication expired", r, ctx.Err())
		case <-tick.C:
		}
	}
}

// Counter ownership follows the observed method, never just SET preferences.
func indexStatDelta(t *testing.T, b fixture.Backend, method string, searches, entries int64) [3]int64 {
	t.Helper()
	if indexStatSimple(t, b, method) {
		return [3]int64{searches, entries, entries}
	}
	if strings.Contains(method, "Bitmap Index Scan") {
		return [3]int64{searches, entries, 0}
	}
	if strings.Contains(method, "Seq Scan") || strings.Contains(method, "Result") {
		return [3]int64{}
	}
	t.Fatal("unclassified actual index access method", method)
	return [3]int64{}
}

func indexStatViewDefinition(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const expected = ` SELECT c.oid AS relid,
    i.oid AS indexrelid,
    n.nspname AS schemaname,
    c.relname,
    i.relname AS indexrelname,
    pg_stat_get_numscans(i.oid) AS idx_scan,
    pg_stat_get_lastscan(i.oid) AS last_idx_scan,
    pg_stat_get_tuples_returned(i.oid) AS idx_tup_read,
    pg_stat_get_tuples_fetched(i.oid) AS idx_tup_fetch
   FROM (((pg_class c
     JOIN pg_index x ON ((c.oid = x.indrelid)))
     JOIN pg_class i ON ((i.oid = x.indexrelid)))
     LEFT JOIN pg_namespace n ON ((n.oid = c.relnamespace)))
  WHERE (c.relkind = ANY (ARRAY['r'::"char", 't'::"char", 'm'::"char"]));`
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_stat_all_indexes'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{expected}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	const pretty = ` SELECT c.oid AS relid,
    i.oid AS indexrelid,
    n.nspname AS schemaname,
    c.relname,
    i.relname AS indexrelname,
    pg_stat_get_numscans(i.oid) AS idx_scan,
    pg_stat_get_lastscan(i.oid) AS last_idx_scan,
    pg_stat_get_tuples_returned(i.oid) AS idx_tup_read,
    pg_stat_get_tuples_fetched(i.oid) AS idx_tup_fetch
   FROM pg_class c
     JOIN pg_index x ON c.oid = x.indrelid
     JOIN pg_class i ON i.oid = x.indexrelid
     LEFT JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind = ANY (ARRAY['r'::"char", 't'::"char", 'm'::"char"]);`
	for _, prepared := range []bool{false, true} {
		step := fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_stat_all_indexes'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{pretty}})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
}
