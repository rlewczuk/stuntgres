package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllIndexesIdentityAndMetadata(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexStatCreate(t, p, b, c, "stat_identity", "stat_identity_a")
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX stat_identity_b ON stat_identity(n)", 0)
		a := indexStatGet(t, p, c, "stat_identity_a")
		second := indexStatGet(t, p, c, "stat_identity_b")
		if a.Parent <= 0 || a.Index <= 0 || a.Parent == a.Index || second.Parent != a.Parent || second.Index == a.Index || a.Schema != "public" || a.Table != "stat_identity" || a.Name != "stat_identity_a" {
			t.Fatal("exact independent index identity", a, second)
		}
		indexStatWant(t, a, 0, 0, 0)
		indexStatWant(t, second, 0, 0, 0)
		var parent, index int64
		if err := c.QueryRowContext(p.Ctx, "SELECT indrelid,indexrelid FROM pg_index WHERE indexrelid='stat_identity_a'::regclass").Scan(&parent, &index); err != nil || parent != a.Parent || index != a.Index {
			t.Fatal("pg_index relationship", parent, index, err)
		}
		indexStatViewDefinition(t, p, b, c)
		for _, empty := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				q := indexStatSelect
				if empty {
					q += " AND FALSE"
				}
				var rows *sql.Rows
				var err error
				var st *sql.Stmt
				if prepared {
					st, err = c.PrepareContext(p.Ctx, q)
					if err == nil {
						rows, err = st.QueryContext(p.Ctx, "stat_identity_a")
					}
				} else {
					rows, err = c.QueryContext(p.Ctx, q, "stat_identity_a")
				}
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				indexStatMetadata(t, columns, b.PG)
				count := 0
				for rows.Next() {
					raw := make([]any, 9)
					dest := make([]any, 9)
					for i := range raw {
						dest[i] = &raw[i]
					}
					if err := rows.Scan(dest...); err != nil {
						t.Fatal(err)
					}
					for i, v := range raw {
						if i == 6 {
							if v != nil {
								t.Fatal("absent last scan not NULL", v)
							}
							continue
						}
						if b.PG && (i < 5) {
							if _, ok := v.([]byte); !ok {
								t.Fatalf("pq raw %s=%T", indexStatNames[i], v)
							}
						} else if i >= 2 && i <= 4 {
							if _, ok := v.(string); !ok {
								t.Fatalf("native raw NAME %T", v)
							}
						} else if _, ok := v.(int64); !ok {
							t.Fatalf("raw integer %s=%T", indexStatNames[i], v)
						}
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				indexStatMetadata(t, columns, b.PG)
				if st != nil {
					st.Close()
				}
				want := 1
				if empty {
					want = 0
				}
				if count != want {
					t.Fatal("metadata cursor row count", count, want)
				}
			}
		}
		indexStatPartition(t, p, c)
		fixture.StatisticsExec(t, p, b, c, "ALTER INDEX stat_identity_a RENAME TO stat_identity_renamed", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA stat_identity_schema", 0)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE stat_identity SET SCHEMA stat_identity_schema", 0)
		moved := indexStatGet(t, p, c, "stat_identity_renamed")
		if moved.Index != a.Index || moved.Parent != a.Parent || moved.Schema != "stat_identity_schema" {
			t.Fatal("rename/move reminted identity", moved, a)
		}
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX stat_identity_schema.stat_identity_renamed", 0)
		if _, err := indexStatRead(p.Ctx, c, "stat_identity_renamed"); err != sql.ErrNoRows {
			t.Fatal("drop retained row", err)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX stat_identity_renamed ON stat_identity_schema.stat_identity USING hash(n)", 0)
		fresh := indexStatGet(t, p, c, "stat_identity_renamed")
		if fresh.Index == a.Index || fresh.Parent != a.Parent {
			t.Fatal("recreated relationship", fresh)
		}
		indexStatWant(t, fresh, 0, 0, 0)
	})
}
func indexStatPartition(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	read := func(predicate string) map[int64]bool {
		rows, err := c.QueryContext(p.Ctx, "SELECT indexrelid FROM pg_stat_all_indexes"+predicate)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[int64]bool{}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			if out[id] {
				t.Fatal("duplicate index identity", id)
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := read("")
	sys := read(" WHERE schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'")
	user := read(" WHERE schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast'")
	if len(sys)+len(user) != len(all) {
		t.Fatal("system/user partition cardinality")
	}
	for id := range all {
		if sys[id] == user[id] {
			t.Fatal("not disjoint/exhaustive", id)
		}
	}
}

func TestReferencePGStatAllIndexesEvents(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexStatCreate(t, p, b, c, "stat_events", "stat_events_hash")
		fixture.StatisticsCreateTables(t, p, b, c, "stat_events_marker")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		expected := [3]int64{}
		for i, kind := range []string{"hit", "miss", "residual", "seq", "disabled", "prepare_only", "null"} {
			writer, retire := fixture.StatisticsReportingConn(t, p, b)
			indexStatPrefer(t, p, b, writer)
			q := "SELECT payload FROM stat_events WHERE n=$1"
			arg := any(2)
			want := []string{"a", "b"}
			switch kind {
			case "miss":
				arg = 99
				want = nil
			case "residual":
				q = "SELECT payload FROM stat_events WHERE n=$1 AND payload='a'"
				want = []string{"a"}
			case "seq":
				fixture.StatisticsExec(t, p, b, writer, "SET enable_seqscan=on", 0)
				fixture.StatisticsExec(t, p, b, writer, "SET enable_indexscan=off", 0)
			case "disabled":
				fixture.StatisticsExec(t, p, b, writer, "SET track_counts=off", 0)
			case "null":
				arg = nil
				want = nil
			}
			method := indexStatPlan(t, p, writer, q, arg)
			delta := [3]int64{}
			if kind == "residual" && !b.PG {
				if !strings.Contains(method, "Access Method: Full Scan") {
					t.Fatal("native residual scope changed", method)
				}
			} else if kind != "seq" && kind != "null" && kind != "disabled" && kind != "prepare_only" {
				entries := int64(2)
				if kind == "miss" {
					entries = 0
				}
				delta = indexStatDelta(t, b, method, 1, entries)
			}
			if kind == "seq" {
				if b.PG && !strings.Contains(method, "Seq Scan") {
					t.Fatal("negative witness not sequential", method)
				}
				if !b.PG && !strings.Contains(method, "Access Method: Full Scan") {
					t.Fatal("negative witness not full", method)
				}
			}
			if kind == "prepare_only" {
				stmt, err := writer.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				stmt.Close()
			} else {
				indexStatPayloads(t, p, b, writer, q, want, arg)
			}
			for j := range expected {
				expected[j] += delta[j]
			}
			// Restore collection for the independent publication witness, not the scan.
			fixture.StatisticsExec(t, p, b, writer, "SET track_counts=on", 0)
			fixture.StatisticsExec(t, p, b, writer, "INSERT INTO stat_events_marker VALUES(1)", 1)
			retire()
			fixture.StatisticsAwaitTableCount(t, p, b, "stat_events_marker", "n_tup_ins", int64(i+1))
			indexStatAwait(t, p, b, "stat_events_hash", expected[0], expected[1], expected[2])
		}
	})
}

func TestReferencePGStatAllIndexesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		indexStatCreate(t, p, b, c, "stat_index_smoke", "stat_index_smoke_hash")
		fixture.StatisticsCreateTables(t, p, b, c, "stat_index_smoke_marker")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		initial := indexStatGet(t, p, c, "stat_index_smoke_hash")
		indexStatWant(t, initial, 0, 0, 0)
		writer, retire := fixture.StatisticsReportingConn(t, p, b)
		indexStatPrefer(t, p, b, writer)
		q := "SELECT payload FROM stat_index_smoke WHERE n=$1"
		method := indexStatPlan(t, p, writer, q, 2)
		counts := indexStatDelta(t, b, method, 1, 2)
		missMethod := indexStatPlan(t, p, writer, q, 99)
		missDelta := indexStatDelta(t, b, missMethod, 1, 0)
		for i := range counts {
			counts[i] += missDelta[i]
		}
		stmt, err := writer.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			arg  int
			rows [][]any
		}{{2, [][]any{{"a"}, {"b"}}}, {99, nil}} {
			step := fixture.Select(q, []string{"payload"}, []string{"TEXT"}, tc.rows, tc.arg)
			step.Bag = true
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: writer, Statement: stmt, SQL: q}, b.Recorder, step)
		}
		stmt.Close()
		fixture.StatisticsExec(t, p, b, writer, "INSERT INTO stat_index_smoke_marker VALUES(1)", 1)
		retire()
		fixture.StatisticsAwaitTableCount(t, p, b, "stat_index_smoke_marker", "n_tup_ins", 1)
		positive := indexStatAwait(t, p, b, "stat_index_smoke_hash", counts[0], counts[1], counts[2])
		if !b.PG && !positive.Last.Valid {
			t.Fatal("native smoke had no real positive scan")
		}
		negative, retireNegative := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, negative, "SET enable_seqscan=on", 0)
		fixture.StatisticsExec(t, p, b, negative, "SET enable_indexscan=off", 0)
		fixture.StatisticsExec(t, p, b, negative, "SET enable_bitmapscan=off", 0)
		method = indexStatPlan(t, p, negative, q, 2)
		if b.PG && !strings.Contains(method, "Seq Scan") || !b.PG && !strings.Contains(method, "Access Method: Full Scan") {
			t.Fatal("smoke negative method", method)
		}
		indexStatPayloads(t, p, b, negative, q, []string{"a", "b"}, 2)
		fixture.StatisticsExec(t, p, b, negative, "INSERT INTO stat_index_smoke_marker VALUES(2)", 1)
		retireNegative()
		fixture.StatisticsAwaitTableCount(t, p, b, "stat_index_smoke_marker", "n_tup_ins", 2)
		after := indexStatAwait(t, p, b, "stat_index_smoke_hash", counts[0], counts[1], counts[2])
		if !positive.equal(after) {
			t.Fatal("sequential scan changed target index", positive, after)
		}
		fixture.StatisticsExec(t, p, b, c, "CREATE INDEX stat_index_smoke_btree ON stat_index_smoke(n)", 0)
		indexStatWant(t, indexStatGet(t, p, c, "stat_index_smoke_btree"), 0, 0, 0)
		rows, err := c.QueryContext(p.Ctx, indexStatSelect, "stat_index_smoke_hash")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		indexStatMetadata(t, columns, b.PG)
		if !rows.Next() {
			t.Fatal("positive metadata row missing")
		}
		raw := make([]any, 9)
		dest := make([]any, 9)
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if counts[0] > 0 {
			if _, ok := raw[6].(time.Time); !ok {
				t.Fatalf("nonnull TIMESTAMPTZ raw %T", raw[6])
			}
		}
		rows.Close()
		indexStatPartition(t, p, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("UPDATE pg_stat_all_indexes SET idx_scan=0 WHERE FALSE", indexStatMutationDiagnostic("UPDATE")))
		fixture.StatisticsExec(t, p, b, c, "DROP INDEX stat_index_smoke_hash", 0)
		if _, err := indexStatRead(p.Ctx, c, "stat_index_smoke_hash"); err != sql.ErrNoRows {
			t.Fatal("smoke dropped index remains", err)
		}
	})
}
