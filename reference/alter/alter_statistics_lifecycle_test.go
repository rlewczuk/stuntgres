package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterStatisticsLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, q := range []string{
		"CREATE TABLE statistics_life(retired integer,a integer,b integer)",
		"ALTER TABLE statistics_life ALTER retired SET STATISTICS 7, ALTER retired SET(n_distinct=3), ALTER a SET STATISTICS 0, ALTER a SET(n_distinct=0), ALTER b SET STATISTICS 7, ALTER b SET(n_distinct=2,n_distinct_inherited=-0.5)",
		"ALTER TABLE statistics_life RENAME COLUMN b TO renamed",
		"ALTER TABLE statistics_life DROP COLUMN retired, ADD COLUMN retired integer",
		"ALTER TABLE statistics_life ALTER renamed TYPE bigint",
		`CREATE TABLE "Statistics Quoted"("a""\name" integer)`,
		`ALTER TABLE "Statistics Quoted" ALTER "a""\name" SET STATISTICS 7, ALTER "a""\name" SET(n_distinct=0)`,
	} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO statistics_life VALUES(1,2,NULL)", 1))
	saved := statisticsMetadata("statistics_life", []any{"a", int64(0), "{n_distinct=0}"}, []any{"renamed", int64(7), "{n_distinct=2,n_distinct_inherited=-0.5}"}, []any{"retired", nil, nil})
	p.Step(t, saved)
	dropped := fixture.Select("SELECT attnum,attstattarget,attoptions FROM pg_attribute WHERE attrelid='statistics_life'::regclass AND attisdropped ORDER BY attnum", []string{"attnum", "attstattarget", "attoptions"}, []string{"INT2", "INT2", "_TEXT"}, [][]any{{int64(1), nil, nil}})
	p.Step(t, dropped)
	data := fixture.Prepared(fixture.Select("SELECT a,renamed,retired FROM statistics_life ORDER BY a", []string{"a", "renamed", "retired"}, []string{"INT4", "INT8", "INT4"}, [][]any{{int64(1), int64(2), nil}}))
	p.Step(t, data)
	for _, q := range []string{"CREATE TABLE statistics_parent(a integer)", "CREATE TABLE statistics_child() INHERITS(statistics_parent)", "ALTER TABLE statistics_parent ALTER a SET STATISTICS 7, ALTER a SET(n_distinct=2)", "ALTER TABLE ONLY statistics_child ALTER a SET STATISTICS 0, ALTER a SET(n_distinct_inherited=-0.5)"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	// Native logical controls serialize configuration and data, not physical
	// PostgreSQL samples, pages, WAL, or an external pg_dump process.
	quoted := statisticsMetadata(`"Statistics Quoted"`, []any{"a\"\\name", int64(7), "{n_distinct=0}"})
	p.Step(t, quoted)
	before := storageDump(t, p)
	unchanged := func() {
		t.Helper()
		if !bytes.Equal(storageDump(t, p), before) {
			t.Fatal("rejected restore changed configuration/data")
		}
	}
	bad := `CREATE TABLE public.statistics_restore(a integer); ALTER TABLE ONLY public.statistics_restore ALTER a SET STATISTICS 7; ALTER TABLE ONLY public.statistics_restore ALTER a SET(n_distinct=-2);`
	err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
	want := fixture.ControlsReferenceDiagnostic("22023", `value -2 out of bounds for option "n_distinct"`)
	want.Detail = statisticsBounds
	fixture.AssertDiagnostic(t, err, &want)
	unchanged()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unchanged()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storageDump(t, p), before) {
		t.Fatal("statistics redump is not deterministic")
	}
	for _, s := range []fixture.Step{saved, dropped, data, quoted, statisticsMetadata("statistics_parent", []any{"a", int64(7), "{n_distinct=2}"}), statisticsMetadata("statistics_child", []any{"a", int64(0), "{n_distinct_inherited=-0.5}"})} {
		fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, s)
	}
}

func TestReferenceAlterStatisticsSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE statistics_t(a integer,b integer)", 0))
	p.Step(t, fixture.Exec(statisticsInsert(20), 20))
	held := make([]fixture.HeldStatement, 0, len(p.Backends()))
	for _, b := range p.Backends() {
		query := statisticsData(20).Query
		statement, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { statement.Close() })
		held = append(held, fixture.HeldStatement{Executor: b.DB, Statement: statement, SQL: query})
	}
	p.Step(t, statisticsMetadata("statistics_t", []any{"a", nil, nil}, []any{"b", nil, nil}))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 3", 0)))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_t ALTER a SET(n_distinct=2)", 0))
	p.Step(t, fixture.Exec("ANALYZE statistics_t", 0))
	p.Step(t, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}, []any{"b", nil, nil}))
	for _, b := range p.Backends() {
		statisticsEstimate(t, p, b.DB, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 10)
	}
	for i, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, held[i], b.Recorder, statisticsData(20))
	}
	p.Step(t, fixture.Select("SELECT a FROM statistics_t WHERE a=1", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	p.Step(t, statisticsError("ALTER TABLE statistics_t ALTER a SET STATISTICS 7, ALTER a SET(bogus=1)", "22023", `unrecognized parameter "bogus"`))
	p.Step(t, statisticsMetadata("statistics_t", []any{"a", int64(3), "{n_distinct=2}"}, []any{"b", nil, nil}))
	for _, b := range p.Backends() {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"BEGIN", "ALTER TABLE statistics_t ALTER a SET(n_distinct=-0.5)", "ROLLBACK"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		c.Close()
		var notices []fixture.Diagnostic
		_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, rec, fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS 10001", 0))
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "lowering statistics target to 10000"}
		if len(notices) != 1 || notices[0] != want {
			t.Fatal(notices)
		}
	}
	p.Step(t, statisticsMetadata("statistics_t", []any{"a", int64(10000), "{n_distinct=2}"}, []any{"b", nil, nil}))
	p.Step(t, fixture.Exec("ALTER TABLE statistics_t ALTER a SET STATISTICS DEFAULT", 0))
	saved := statisticsMetadata("statistics_t", []any{"a", nil, "{n_distinct=2}"}, []any{"b", nil, nil})
	p.Step(t, saved)
	before := storageDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(before)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, saved)
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, statisticsData(20))
	fixture.RunStep(t, p.Ctx, p.Memory, p.Backends()[0].Recorder, fixture.Exec("ANALYZE statistics_t", 0))
	statisticsEstimate(t, p, p.Memory, "EXPLAIN SELECT a FROM statistics_t WHERE a=1", 10)
}
