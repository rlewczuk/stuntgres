package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native controls have no PostgreSQL API equivalent. This phase belongs to the
// same dual-mode lifetime scenario, using actual emitted persistent SQL.
func xactUserNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xactUserCreate(t, p, b, c, "xut_control")
	xactUserSeed(t, p, b, "xut_control")
	fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
	tableStatSeqPrefer(t, p, b, c)
	for range 2 {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT payload FROM public.xut_control WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
	}
	fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
	// Pending and legitimately published read work must add to the exact total.
	const totalSQL = "SELECT x.seq_scan+s.seq_scan AS scans,x.seq_tup_read+s.seq_tup_read AS tuples FROM pg_stat_xact_user_tables x JOIN pg_stat_user_tables s ON s.relid=x.relid WHERE x.relname='xut_control'"
	total := func(scans, tuples int64) {
		t.Helper()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(totalSQL, []string{"scans", "tuples"}, []string{"INT8", "INT8"}, [][]any{{scans, tuples}}))
	}
	total(2, 6)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	emitted := bytes.Clone(dump.Bytes())
	for _, name := range []string{"pg_stat_xact_user_tables", "pg_stat_user_tables"} {
		if bytes.Contains(emitted, []byte(name)) { t.Fatal("dump serialized statistics history", name) }
	}
	keeper := fixture.IdentityConnection(t, p.Ctx, b.DB)
	fixture.StatisticsExec(t, p, b, keeper, "CREATE TEMP TABLE xut_control_temp(n integer)", 0)
	fixture.StatisticsExec(t, p, b, keeper, "BEGIN", 0)
	fixture.StatisticsExec(t, p, b, keeper, "INSERT INTO xut_control_temp VALUES(7)", 1)
	xactTableEnd(t, p, b, keeper, "COMMIT")
	fixture.StatisticsExec(t, p, b, keeper, "BEGIN", 0)
	fixture.RunStep(t, p.Ctx, keeper, b.Recorder, fixture.Select("SELECT n FROM xut_control_temp", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	temp := xactUserGet(t, p, keeper, "xut_control_temp")
	if temp.Seq != 1 || temp.Read != 1 { t.Fatal("positive local TEMP read batch", temp) }
	fixture.StatisticsExec(t, p, b, keeper, "ROLLBACK", 0)
	old := xactUserGet(t, p, c, "xut_control")
	unchanged := func() {
		t.Helper()
		if got := xactUserGet(t, p, c, "xut_control"); got.ID != old.ID { t.Fatal("failed Restore changed identity", got, old) }
		total(2, 6)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM public.xut_control ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
		if got := xactUserGet(t, p, keeper, "xut_control_temp"); got.ID != temp.ID { t.Fatal("failed Restore changed attached TEMP", got, temp) }
	}
	bad := append(bytes.Clone(emitted), []byte("\nCREATE TABLE xut_bad(n no_such_type);")...)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil { t.Fatal("late invalid Restore accepted") }
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) { t.Fatal("canceled Restore", err) }
	unchanged()
	rows, err := keeper.QueryContext(p.Ctx, xactUserSelect, "xut_control")
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil { t.Fatal(err) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	var missing xactTableRow
	if err := c.QueryRowContext(p.Ctx, xactUserSelect, "xut_control").Scan(missing.destinations()...); err != sql.ErrNoRows { t.Fatal("Reset retained persistent member", err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil { t.Fatal(err) }
	if !rows.Next() { t.Fatal("retained USER result lost across controls", rows.Err()) }
	var retained xactTableRow
	if err := rows.Scan(retained.destinations()...); err != nil { t.Fatal(err) }
	rows.Close()
	xactTableMetadata(t, columns, false)
	if retained.ID != old.ID || retained.Schema != "public" || retained.Name != "xut_control" { t.Fatal("retained identity changed", retained, old) }
	total(0, 0)
	fresh := xactUserGet(t, p, c, "xut_control")
	xactTableCounts(t, fresh, 0, 0, 0)
	if fresh.Seq != 0 || fresh.Read != 0 { t.Fatal("persistent pending crossed replacement", fresh) }
	if got := xactUserGet(t, p, keeper, "xut_control_temp"); got.ID != temp.ID || got.Seq != 0 || got.Read != 0 { t.Fatal("TEMP pending crossed replacement", got, temp) }
	fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM public.xut_control ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
	var view, rowType, array int64
	if err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_xact_user_tables'::regclass").Scan(&view, &rowType, &array); err != nil || view != 12170 || rowType != 12172 || array != 12171 { t.Fatal("USER reinstall identities", view, rowType, array, err) }
}
