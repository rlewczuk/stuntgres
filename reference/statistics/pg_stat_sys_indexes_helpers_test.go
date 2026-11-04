package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const sysIndexSelect = "SELECT * FROM pg_catalog.pg_stat_sys_indexes WHERE indexrelname=$1"
const sysIndexPredicate = "schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'"
const userIndexPredicate = "schemaname NOT IN ('pg_catalog','information_schema') AND schemaname !~ '^pg_toast'"

func sysIndexOwned(t *testing.T, b fixture.Backend, name string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, schema := range []string{"information_schema", "public"} {
			if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+schema+"."+name+" CASCADE"); err != nil {
				t.Error("owned SYS index fixture cleanup", err)
			}
		}
	})
}

func sysIndexCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema, name string) {
	t.Helper()
	sysIndexOwned(t, b, name) // cleanup precedes even the first setup statement
	indexStatCreate(t, p, b, c, schema+"."+name, name+"_hash")
}

func sysIndexRead(ctx context.Context, c *sql.Conn, name string) (indexStatRow, error) {
	var r indexStatRow
	err := c.QueryRowContext(ctx, sysIndexSelect, name).Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
	return r, err
}

func sysIndexGet(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	r, err := sysIndexRead(p.Ctx, c, name)
	if err != nil {
		t.Fatal(name, err)
	}
	return r
}

func sysIndexHeld(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, name string) (indexStatRow, error) {
	t.Helper()
	var r indexStatRow
	err := stmt.QueryRowContext(p.Ctx, name).Scan(&r.Parent, &r.Index, &r.Schema, &r.Table, &r.Name, &r.Scans, &r.Last, &r.Entries, &r.Fetches)
	return r, err
}

// Compare identities with multiplicity, independently on each backend. Bootstrap
// inventories and initdb allocator sequences are deliberately not compared.
func sysIndexPartition(t *testing.T, p *fixture.Pair, c *sql.Conn, positive, negative string) {
	t.Helper()
	type identity struct {
		parent, index int64
		schema, table, name string
	}
	read := func(source, predicate string) map[identity]int {
		t.Helper()
		q := "SELECT relid,indexrelid,schemaname,relname,indexrelname FROM pg_catalog."+source
		if predicate != "" {
			q += " WHERE "+predicate
		}
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[identity]int{}
		ids := map[int64]bool{}
		for rows.Next() {
			var r identity
			if err := rows.Scan(&r.parent, &r.index, &r.schema, &r.table, &r.name); err != nil {
				t.Fatal(err)
			}
			if r.parent <= 0 || r.index <= 0 || ids[r.index] {
				t.Fatal("invalid/duplicate index identity", r)
			}
			ids[r.index] = true
			out[r]++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	all := read("pg_stat_all_indexes", "")
	sys := read("pg_stat_sys_indexes", "")
	s := read("pg_stat_all_indexes", sysIndexPredicate)
	u := read("pg_stat_user_indexes", "")
	wantUser := read("pg_stat_all_indexes", userIndexPredicate)
	if !reflect.DeepEqual(u, wantUser) {
		t.Fatal("USER differs from exact release predicate", u, wantUser)
	}
	if !reflect.DeepEqual(sys, s) {
		t.Fatal("SYS differs from exact release predicate", sys, s)
	}
	union := map[identity]int{}
	positiveSeen, negativeSeen := false, false
	for r, n := range s {
		if u[r] != 0 {
			t.Fatal("S and U overlap", r)
		}
		union[r] += n
		if r.name == positive && r.schema == "information_schema" {
			positiveSeen = true
		}
		if r.name == negative {
			t.Fatal("public negative admitted to SYS", r)
		}
	}
	for r, n := range u {
		union[r] += n
		if r.name == negative && r.schema == "public" {
			negativeSeen = true
		}
	}
	if !reflect.DeepEqual(union, all) || !positiveSeen || !negativeSeen {
		t.Fatal("exact identity bag partition/owned witnesses", union, all, positiveSeen, negativeSeen)
	}
}

func sysIndexShared(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
	fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
	defer c.ExecContext(context.Background(), "ROLLBACK")
	sys, all := sysIndexGet(t, p, c, name), indexStatGet(t, p, c, name)
	if !sys.equal(all) {
		t.Fatal("SYS and ALL must share exact provider facts", sys, all)
	}
	fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
	return sys
}

// Every phase retires its reporter and awaits a separate insert publication
// marker, including EXPLAIN/Prepare-only and sequential zero-delta phases.
func sysIndexAccesses(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) indexStatRow {
	t.Helper()
	marker := name+"_marker"
	sysIndexOwned(t, b, marker)
	fixture.StatisticsCreateTables(t, p, b, c, marker)
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
	expected := [3]int64{}
	q := "SELECT payload FROM information_schema."+name+" WHERE n=$1"
	var positive indexStatRow
	for i, phase := range []string{"explain_prepare", "direct", "held", "miss", "sequential"} {
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		indexStatPrefer(t, p, b, w)
		if phase == "sequential" {
			for _, setting := range []string{"enable_seqscan=on", "enable_indexscan=off", "enable_bitmapscan=off"} {
				fixture.StatisticsExec(t, p, b, w, "SET "+setting, 0)
			}
		}
		arg, entries := 2, int64(2)
		want := [][]any{{"a"}, {"b"}}
		if phase == "miss" {
			arg, entries, want = 99, 0, nil
		}
		method := indexStatPlan(t, p, w, q, arg)
		if phase == "sequential" && ((b.PG && !strings.Contains(method, "Seq Scan")) || (!b.PG && !strings.Contains(method, "Access Method: Full Scan"))) {
			t.Fatal("sequential negative requires actual sequential path", method)
		}
		delta := [3]int64{}
		if phase != "sequential" && phase != "explain_prepare" {
			delta = indexStatDelta(t, b, method, 1, entries)
		}
		if phase == "held" || phase == "miss" || phase == "explain_prepare" {
			stmt, err := w.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if phase != "explain_prepare" {
				step := fixture.Select(q, []string{"payload"}, []string{"TEXT"}, want, arg)
				step.Bag = true
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: w, Statement: stmt, SQL: q}, b.Recorder, step)
			}
			stmt.Close()
		} else {
			indexStatPayloads(t, p, b, w, q, []string{"a", "b"}, arg)
		}
		for j := range expected {
			expected[j] += delta[j]
		}
		fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+marker+" VALUES(1)", 1)
		retire()
		fixture.StatisticsAwaitTableCount(t, p, b, marker, "n_tup_ins", int64(i+1))
		indexStatAwait(t, p, b, name+"_hash", expected[0], expected[1], expected[2])
		current := sysIndexShared(t, p, b, c, name+"_hash")
		indexStatWant(t, current, expected[0], expected[1], expected[2])
		if phase == "direct" && !b.PG {
			indexStatWant(t, current, 1, 2, 2)
		}
		if phase == "sequential" && !current.equal(positive) {
			t.Fatal("published sequential access changed index facts or instant", positive, current)
		}
		positive = current
	}
	return positive
}
