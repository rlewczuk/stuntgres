package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func xactBlockExplain(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "EXPLAIN "+query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if plan.Len() == 0 {
		t.Fatal("empty EXPLAIN")
	}
	return plan.String()
}

func xactBlockIndexRead(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, selected bool) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 3 {
		t.Fatal("index witness metadata", columns, err)
	}
	names := []string{"id", xactBlockNames[0], xactBlockNames[1]}
	for i, column := range columns {
		kind, scan := "INT8", reflect.TypeFor[int64]()
		if i == 0 {
			kind = "INT4"
			if b.PG {
				scan = reflect.TypeFor[int32]()
			}
		}
		if column.Name() != names[i] || column.DatabaseTypeName() != kind || column.ScanType() != scan {
			t.Fatal("index witness column", i, column.Name(), column.DatabaseTypeName(), column.ScanType())
		}
		if i > 0 {
			if _, known := column.Nullable(); known {
				t.Fatal("computed counter claimed known nullability")
			}
		}
	}
	if !rows.Next() {
		t.Fatal("index witness missing singleton row", rows.Err())
	}
	var id, fetched, hit int64
	if err := rows.Scan(&id, &fetched, &hit); err != nil {
		t.Fatal(err)
	}
	if id != 2 || fetched < 0 || hit < 0 || hit > fetched || selected && fetched == 0 {
		t.Fatal("reached index singleton and pending requests", id, fetched, hit, selected)
	}
	if !b.PG && (fetched != 1 || hit != 1) {
		t.Fatal("native index request must add exactly fetched1/hit1", fetched, hit)
	}
	if rows.Next() || rows.Err() != nil || rows.NextResultSet() {
		t.Fatal("index witness cardinality", rows.Err())
	}
}

func TestReferenceStatsXactBlocksReads(t *testing.T) {
	t.Run("LivePreparedAndPeer", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			statsCreate(t, p, b, c, "xact_read_a", "xact_read_b", "xact_read_empty")
			statsExec(t, p, b, c, "INSERT INTO xact_read_a VALUES(1),(2)", 2)
			statsExec(t, p, b, c, "INSERT INTO xact_read_b VALUES(9)", 1)
			a, other, empty := xactBlockOID(t, p, c, "xact_read_a"), xactBlockOID(t, p, c, "xact_read_b"), xactBlockOID(t, p, c, "xact_read_empty")
			peer := statActivityConn(t, p, b)
			xactBlockBegin(t, p, b, c)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			base, untouched := xactBlockRead(t, p, c, nil, a), xactBlockRead(t, p, c, nil, other)
			getter, err := c.PrepareContext(p.Ctx, xactBlockSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer getter.Close()
			const query = "SELECT id FROM xact_read_a ORDER BY id"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			// Preparing/describing a SELECT and EXPLAIN-only must not open data.
			xactBlockExplain(t, p, c, query)
			xactBlockWant(t, xactBlockRead(t, p, c, getter, a), base)
			xactBlockRows(t, p, b, c, query, 1, 2)
			first := xactBlockRead(t, p, c, getter, a)
			xactBlockAdvanced(t, b, base, first)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, other), untouched)
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			second := xactBlockRead(t, p, c, getter, a)
			xactBlockAdvanced(t, b, first, second)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, other), untouched)
			xactBlockRows(t, p, b, peer, query, 1, 2)
			xactBlockWant(t, xactBlockRead(t, p, c, getter, a), second)
			beforeEmpty := xactBlockRead(t, p, c, nil, empty)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_read_empty ORDER BY id")
			if !b.PG {
				xactBlockWant(t, xactBlockRead(t, p, c, nil, empty), beforeEmpty)
			}
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_read_a WHERE false ORDER BY id")
			if !b.PG {
				xactBlockWant(t, xactBlockRead(t, p, c, nil, a), second)
			}
		})
	})
	t.Run("PrivateTempViewAndRewrite", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			xactBlockBegin(t, p, b, c)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			for _, tc := range []struct {
				name, create string
				value        int64
			}{
				{"xact_private", "CREATE TABLE xact_private(id integer)", 7},
				{"xact_temp", "CREATE TEMP TABLE xact_temp(id integer)", 8},
			} {
				statsExec(t, p, b, c, tc.create, 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+tc.name+" VALUES($1)", 1, tc.value))
				oid := xactBlockOID(t, p, c, tc.name)
				before := xactBlockRead(t, p, c, nil, oid)
				xactBlockRows(t, p, b, c, "SELECT id FROM "+tc.name+" ORDER BY id", tc.value)
				xactBlockAdvanced(t, b, before, xactBlockRead(t, p, c, nil, oid))
				statsExec(t, p, b, c, "CREATE VIEW "+tc.name+"_view AS SELECT id FROM "+tc.name, 0)
				view := xactBlockOID(t, p, c, tc.name+"_view")
				baseView, baseTable := xactBlockRead(t, p, c, nil, view), xactBlockRead(t, p, c, nil, oid)
				xactBlockRows(t, p, b, c, "SELECT id FROM "+tc.name+"_view ORDER BY id", tc.value)
				xactBlockAdvanced(t, b, baseTable, xactBlockRead(t, p, c, nil, oid))
				xactBlockWant(t, xactBlockRead(t, p, c, nil, view), baseView)
			}
			statsExec(t, p, b, c, "SAVEPOINT xact_private_mark", 0)
			statsExec(t, p, b, c, "CREATE TABLE xact_savepoint_private(id integer)", 0)
			statsExec(t, p, b, c, "INSERT INTO xact_savepoint_private VALUES(11)", 1)
			private := xactBlockOID(t, p, c, "xact_savepoint_private")
			before := xactBlockRead(t, p, c, nil, private)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_savepoint_private ORDER BY id", 11)
			xactBlockAdvanced(t, b, before, xactBlockRead(t, p, c, nil, private))
			statsExec(t, p, b, c, "ROLLBACK TO xact_private_mark", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, private), xactBlockPair{})
			// A private storage replacement is still a real admitted source.
			statsExec(t, p, b, c, "CREATE TABLE xact_rewritten(id integer)", 0)
			statsExec(t, p, b, c, "INSERT INTO xact_rewritten VALUES(12)", 1)
			statsExec(t, p, b, c, "TRUNCATE xact_rewritten", 0)
			statsExec(t, p, b, c, "INSERT INTO xact_rewritten VALUES(13)", 1)
			rewritten := xactBlockOID(t, p, c, "xact_rewritten")
			before = xactBlockRead(t, p, c, nil, rewritten)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_rewritten ORDER BY id", 13)
			xactBlockAdvanced(t, b, before, xactBlockRead(t, p, c, nil, rewritten))
		})
	})
	t.Run("Inheritance", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, query := range []string{"CREATE TABLE xact_parent(id integer)", "CREATE TABLE xact_leaf_a() INHERITS(xact_parent)", "CREATE TABLE xact_leaf_b() INHERITS(xact_parent)", "CREATE TABLE xact_leaf_empty() INHERITS(xact_parent)"} {
				statsExec(t, p, b, c, query, 0)
			}
			statsExec(t, p, b, c, "INSERT INTO xact_leaf_a VALUES(1),(2)", 2)
			statsExec(t, p, b, c, "INSERT INTO xact_leaf_b VALUES(9)", 1)
			parent, a, other, empty := xactBlockOID(t, p, c, "xact_parent"), xactBlockOID(t, p, c, "xact_leaf_a"), xactBlockOID(t, p, c, "xact_leaf_b"), xactBlockOID(t, p, c, "xact_leaf_empty")
			xactBlockBegin(t, p, b, c)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			bp, ba, bb, be := xactBlockRead(t, p, c, nil, parent), xactBlockRead(t, p, c, nil, a), xactBlockRead(t, p, c, nil, other), xactBlockRead(t, p, c, nil, empty)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_parent ORDER BY id", 1, 2, 9)
			xactBlockAdvanced(t, b, ba, xactBlockRead(t, p, c, nil, a))
			xactBlockAdvanced(t, b, bb, xactBlockRead(t, p, c, nil, other))
			if !b.PG {
				xactBlockWant(t, xactBlockRead(t, p, c, nil, parent), bp)
				xactBlockWant(t, xactBlockRead(t, p, c, nil, empty), be)
			}
		})
	})
	for _, method := range []string{"hash", "bloom"} {
		t.Run("Index_"+method, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if method == "bloom" {
					statsExec(t, p, b, c, "CREATE EXTENSION bloom", 0)
					defer c.ExecContext(context.Background(), "DROP EXTENSION bloom CASCADE")
				}
				statsCreate(t, p, b, c, "xact_indexed")
				statsExec(t, p, b, c, "INSERT INTO xact_indexed VALUES(1),(2),(9)", 3)
				statsExec(t, p, b, c, "CREATE INDEX xact_chosen ON xact_indexed USING "+method+"(id)", 0)
				statsExec(t, p, b, c, "CREATE INDEX xact_unchosen ON xact_indexed USING hash((id+1))", 0)
				chosen, unchosen := xactBlockOID(t, p, c, "xact_chosen"), xactBlockOID(t, p, c, "xact_unchosen")
				reader, retire := statsReportingConn(t, p, b)
				defer retire()
				statsExec(t, p, b, reader, "SET enable_seqscan=off", 0)
				statsExec(t, p, b, reader, "SET max_parallel_workers_per_gather=0", 0)
				// Existing native index eligibility is implicit-only. Project the
				// counters after the actual read, before ordinary terminal publication,
				// rather than changing that eligibility or relying on flush timing.
				query := fmt.Sprintf("SELECT id,pg_stat_get_xact_blocks_fetched(%d::oid),pg_stat_get_xact_blocks_hit(%d::oid) FROM xact_indexed WHERE id=2", chosen, chosen)
				plan := xactBlockExplain(t, p, reader, query)
				if !b.PG && !strings.Contains(plan, "Index: xact_chosen") {
					t.Fatal("native chosen index absent", plan)
				}
				selected := !b.PG || strings.Contains(plan, "xact_chosen")
				xactBlockIndexRead(t, p, b, reader, query, selected)
				if !b.PG {
					xactBlockWant(t, xactBlockRead(t, p, reader, nil, unchosen), xactBlockPair{})
				}
				before := xactBlockRead(t, p, reader, nil, chosen)
				step := fixture.Select("SELECT id FROM xact_indexed WHERE id=$1", []string{"id"}, []string{"INT4"}, nil, nil)
				step.Prepared = true
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, step)
				if !b.PG {
					xactBlockWant(t, xactBlockRead(t, p, reader, nil, chosen), before)
				}
			})
		})
	}
}
