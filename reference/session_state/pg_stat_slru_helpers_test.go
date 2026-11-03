package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const slruSQL = "SELECT * FROM pg_catalog.pg_stat_slru ORDER BY name"
var slruNames = []string{"name", "blks_zeroed", "blks_hit", "blks_read", "blks_written", "blks_exists", "flushes", "truncates", "stats_reset"}
var slruTypes = []string{"TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ"}
var slruCategories = []string{"commit_timestamp", "multixact_member", "multixact_offset", "notify", "other", "serializable", "subtransaction", "transaction"}
type slruRow struct { technical [7]int64; reset time.Time }
type slruFamily struct { rows map[string]slruRow; pg bool }

func slruMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, slruNames) { t.Fatal("nine PG18 names", names, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 9 { t.Fatal("nine descriptors", types, err) }
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 0 { scan = reflect.TypeFor[string]() }
		if i == 8 { scan = reflect.TypeFor[time.Time]() }
		if typ.Name() != slruNames[i] || typ.DatabaseTypeName() != slruTypes[i] || typ.ScanType() != scan { t.Fatal("exact typed metadata", i, typ) }
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg { t.Fatal("nullable metadata", i, nullable, known) }
		length, known := typ.Length()
		if i == 0 {
			if length != math.MaxInt64 || !known {
				t.Fatalf("unbounded TEXT length %d/%t", length, known)
			}
		} else if length != 0 || known {
			t.Fatalf("column %d length %d/%t", i, length, known)
		}
		if p, s, known := typ.DecimalSize(); p != 0 || s != 0 || known { t.Fatal("precision", i, p, s, known) }
	}
}
func slruRead(t *testing.T, rows *sql.Rows, pg bool) slruFamily {
	t.Helper(); defer rows.Close(); slruMetadata(t, rows, pg)
	family := slruFamily{rows: make(map[string]slruRow), pg: pg}
	var names []string
	for rows.Next() {
		var name string; var row slruRow
		targets := []any{&name}
		for i := range row.technical { targets = append(targets, &row.technical[i]) }
		targets = append(targets, &row.reset)
		if err := rows.Scan(targets...); err != nil { t.Fatal("nine non-NULL cells", err) }
		if row.reset.IsZero() || row.reset.Nanosecond()%1000 != 0 { t.Fatal("genuine microsecond reset", name, row) }
		for _, n := range row.technical { if n < 0 { t.Fatal("negative physical count", name, row) } }
		if !pg && row.technical != [7]int64{} { t.Fatal("absent physical SLRU must remain zero", name, row) }
		if _, exists := family.rows[name]; exists { t.Fatal("duplicate category", name) }
		family.rows[name] = row; names = append(names, name)
	}
	if rows.Err() != nil { t.Fatal(rows.Err()) }
	sort.Strings(names)
	if !reflect.DeepEqual(names, slruCategories) { t.Fatal("exact eight-category bag", names) }
	return family
}
func slruGet(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) slruFamily {
	t.Helper(); rows, err := e.QueryContext(p.Ctx, slruSQL); if err != nil { t.Fatal(err) }; return slruRead(t, rows, b.PG)
}
func slruSame(t *testing.T, before, after slruFamily) {
	t.Helper()
	for name, old := range before.rows {
		// PostgreSQL's active physical categories may move during any SQL. The
		// inactive other bucket is the independent public stability witness.
		if before.pg && name != "other" { continue }
		if got := after.rows[name]; !got.reset.Equal(old.reset) || got.technical != old.technical { t.Fatal("unexpected category change", name, old, got) }
	}
}
func slruEvent(t *testing.T, before, after slruFamily, lower, upper time.Time) {
	t.Helper()
	old, fresh := before.rows["other"], after.rows["other"]
	if !fresh.reset.After(old.reset) || fresh.reset.Before(lower) || fresh.reset.After(upper) || fresh.technical != [7]int64{} { t.Fatal("genuine bounded reset and seven zeros", lower, old, fresh, upper) }
}
func slruHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement { t.Helper(); return prefetchHeld(t, p, c, query) }
func slruClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time { t.Helper(); return prefetchClock(t, p, c) }
func slruDistinctClock(t *testing.T, p *fixture.Pair, c *sql.Conn, old time.Time) time.Time {
	t.Helper()
	for { now := slruClock(t, p, c); if now.After(old) { return now }; if err := p.Ctx.Err(); err != nil { t.Fatal(err) } }
}
func slruSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper(); fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM slru_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}
