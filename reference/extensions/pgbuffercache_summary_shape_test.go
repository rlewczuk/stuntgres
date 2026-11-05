package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var bcsSummaryNames = []string{"buffers_used", "buffers_unused", "buffers_dirty", "buffers_pinned", "usagecount_avg"}
var bcsSummaryTypes = []string{"INT4", "INT4", "INT4", "INT4", "FLOAT8"}
var bcsUsageNames = []string{"usage_count", "buffers", "dirty", "pinned"}
var bcsUsageTypes = []string{"INT4", "INT4", "INT4", "INT4"}
var bcsRoutines = []string{"pg_buffercache_summary", "pg_buffercache_usage_counts"}

func bcsQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, summary, view bool) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	names, types := bcsUsageNames, bcsUsageTypes
	if summary {
		names, types = bcsSummaryNames, bcsSummaryTypes
	}
	return bcRows(t, rows, b.PG, view, names, types)
}

func bcsExact(t *testing.T, got, want [][]any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatal("logical cache aggregate", got, "want", want)
	}
}

func TestReferencePgBuffercacheSummaryShape(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		capacity := bcCapacity(t, p, c)
		for i, name := range bcsRoutines {
			t.Run(name, func(t *testing.T) {
				names, types := bcsUsageNames, bcsUsageTypes
				if i == 0 {
					names, types = bcsSummaryNames, bcsSummaryTypes
				}
				q := "SELECT * FROM bc_part." + name + "()"
				if i == 1 {
					q += " ORDER BY usage_count"
				}
				st, err := c.PrepareContext(p.Ctx, q)
				fixture.State(t, err, "")
				defer st.Close()
				held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				for _, route := range []string{"direct", "fresh", "held", "held_again"} {
					t.Run(route, func(t *testing.T) {
						var source fixture.Executor = c
						if route == "fresh" {
							fresh, err := c.PrepareContext(p.Ctx, q)
							fixture.State(t, err, "")
							defer fresh.Close()
							source = fixture.HeldStatement{Executor: c, Statement: fresh, SQL: q}
						} else if strings.HasPrefix(route, "held") {
							source = held
						}
						got := bcsQuery(t, p, b, source, q, i == 0, false)
						if i == 0 {
							bcsSummaryBounds(t, got, capacity, false)
						} else {
							bcsUsageBounds(t, got, capacity)
						}
					})
				}
				empty := fixture.Select("SELECT * FROM bc_part."+name+"() WHERE false", names, types, nil)
				empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { bcMetadata(t, rows, b.PG, false, names, types) }
				bcBoth(t, p, b, c, empty)
				aliases := []string{"a", "b", "c", "d"}
				if i == 0 {
					aliases = append(aliases, "e")
				}
				empty.Query = "SELECT * FROM bc_part." + name + "() AS s(" + strings.Join(aliases, ",") + ") WHERE false"
				empty.Columns = aliases
				empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) { bcMetadata(t, rows, b.PG, false, aliases, types) }
				bcBoth(t, p, b, c, empty)
			})
		}
		// Scalar summary is a non-NULL RECORD, not pages' anonymous row error.
		q := "SELECT bc_part.pg_buffercache_summary() AS result"
		for _, prepared := range []bool{false, true} {
			st, err := c.PrepareContext(p.Ctx, q)
			fixture.State(t, err, "")
			var source fixture.Executor = c
			if prepared {
				source = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			}
			rows, err := source.QueryContext(p.Ctx, q)
			fixture.State(t, err, "")
			cols, err := rows.ColumnTypes()
			if err != nil || len(cols) != 1 || cols[0].Name() != "result" || cols[0].DatabaseTypeName() != "RECORD" {
				t.Fatal("scalar RECORD metadata", cols, err)
			}
			if !rows.Next() {
				t.Fatal("scalar summary missing", rows.Err())
			}
			var text string
			if err := rows.Scan(&text); err != nil || !strings.HasPrefix(text, "(") || !strings.HasSuffix(text, ")") || len(strings.Split(text[1:len(text)-1], ",")) != 5 {
				t.Fatal("scalar composite encoding", text, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("summary scalar cardinality", rows.Err())
			}
			rows.Close()
			st.Close()
		}
		bcBoth(t, p, b, c, fixture.Select("SELECT (bc_part.pg_buffercache_summary()).buffers_used>=0 AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}))
		// A target-list SRF returns six records; no portable row order is assumed.
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM (SELECT bc_part.pg_buffercache_usage_counts() AS bucket) s", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
	})
}
