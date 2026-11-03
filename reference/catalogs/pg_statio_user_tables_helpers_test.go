package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func statioUserTableOne(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64) statioTableRow {
	t.Helper()
	rows := statioTableRows(t, p, b, e, statioUserTableProbe, oid)
	if len(rows) != 1 || rows[0].oid != oid { t.Fatal("actual USER identity", oid, rows) }
	return rows[0]
}

func statioUserTableHeld(t *testing.T, p *fixture.Pair, c *sql.Conn) (fixture.HeldStatement, *sql.Stmt) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, statioUserTableProbe)
	if err != nil { t.Fatal(err) }
	return fixture.HeldStatement{Executor:c, Statement:stmt, SQL:statioUserTableProbe}, stmt
}

// Call inside one observer snapshot when comparing changing physical counters.
func statioUserTableCompanions(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64) statioTableRow {
	t.Helper()
	user := statioUserTableOne(t,p,b,e,oid)
	all := statioTableOne(t,p,b,e,oid)
	if user != all { t.Fatal("named ALL/USER eleven-cell equality", user, all) }
	if sys := statioTableRows(t,p,b,e,"SELECT * FROM pg_catalog.pg_statio_sys_tables WHERE relid=$1::oid",oid); len(sys)!=0 { t.Fatal("named SYS exclusion",sys) }
	return user
}

func statioUserTableIndexSum(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,oid int64) statioTableRow {
	t.Helper()
	user:=statioUserTableOne(t,p,b,e,oid)
	rows,err:=e.QueryContext(p.Ctx,"SELECT sum(idx_blks_read)::bigint,sum(idx_blks_hit)::bigint FROM pg_statio_all_indexes WHERE relid=$1::oid",oid)
	if err!=nil {t.Fatal(err)}
	defer rows.Close()
	var read,hit sql.NullInt64
	if !rows.Next() {t.Fatal(rows.Err())}
	if err:=rows.Scan(&read,&hit);err!=nil {t.Fatal(err)}
	if rows.Next() || rows.Err()!=nil {t.Fatal(rows.Err())}
	if read!=user.blocks[2] || hit!=user.blocks[3] {t.Fatal("USER actual child-index aggregate",user,read,hit)}
	return user
}

// Retire the seed's physical owner and observe its tuple publication before
// taking a heap baseline. A live setup writer must not publish late and be
// mistaken for the subsequent SELECT's delta.
func statioUserTableSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, oid int64, query string, count int64) {
	t.Helper()
	writer, retire := fixture.StatisticsReportingConn(t, p, b)
	fixture.StatisticsExec(t, p, b, writer, query, count)
	retire()
	probe, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer probe.Close()
	fixture.StatisticsExec(t, p, b, probe, "SET stats_fetch_consistency=none", 0)
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		var inserts int64
		if err := probe.QueryRowContext(ctx, "SELECT n_tup_ins FROM pg_catalog.pg_stat_user_tables WHERE relid=$1::oid", oid).Scan(&inserts); err != nil { t.Fatal("seed publication", err) }
		if inserts == count { return }
		select {
		case <-ctx.Done(): t.Fatal("seed publication expired", oid, inserts, count, ctx.Err())
		case <-tick.C:
		}
	}
}

func statioUserTableHeapTotal(t *testing.T, r statioTableRow) int64 {
	t.Helper()
	if !r.blocks[0].Valid || !r.blocks[1].Valid { t.Fatal("heap access counters must be numeric", r) }
	return r.blocks[0].Int64 + r.blocks[1].Int64
}

// Call only after statioTableWork has closed its dedicated writer pool. This
// independent none-mode reader establishes a published, strictly positive
// delta, without assuming any fixed PostgreSQL physical page magnitude.
func statioUserTablePublished(t *testing.T, p *fixture.Pair, b fixture.Backend, before statioTableRow) statioTableRow {
	t.Helper()
	probe, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer probe.Close()
	fixture.StatisticsExec(t, p, b, probe, "SET stats_fetch_consistency=none", 0)
	ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		rows, err := probe.QueryContext(ctx, statioUserTableProbe, before.oid)
		if err != nil { t.Fatal("USER heap publication", err) }
		statioTableMetadata(t, rows, b.PG)
		if !rows.Next() { rows.Close(); t.Fatal("USER publication identity missing", before.oid, rows.Err()) }
		got := statioTableScan(t, rows, b.PG)
		extra := rows.Next()
		err = rows.Err()
		rows.Close()
		if extra || err != nil { t.Fatal("USER publication identity", got, extra, err) }
		if got.schema != before.schema || got.name != before.name || got.blocks[0].Int64 < before.blocks[0].Int64 || got.blocks[1].Int64 < before.blocks[1].Int64 {
			t.Fatal("published heap counters must be monotonic for the same identity", before, got)
		}
		if statioUserTableHeapTotal(t, got) > statioUserTableHeapTotal(t, before) { return got }
		select {
		case <-ctx.Done(): t.Fatal("USER heap publication expired without positive delta", before, got, ctx.Err())
		case <-tick.C:
		}
	}
}
