package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ginMaintControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE gin_maint_ctrl(id integer,a integer[],v tsvector)", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_ctrl_m ON gin_maint_ctrl USING gin(a,v) WHERE id>0", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_ctrl_e ON gin_maint_ctrl USING gin((a||ARRAY[9])) WHERE id>0", 0))
	run(fixture.Exec("INSERT INTO gin_maint_ctrl VALUES(1,'{2,2}','cat:1'),(2,'{2,2}','cat:1')", 2))
	run(ginMaintCleanIndex("gin_maint_ctrl_m", true))
	run(fixture.Exec("INSERT INTO gin_maint_ctrl VALUES(3,'{}',''),(4,NULL,NULL),(5,'{2,2}','cat:1')", 3))
	run(fixture.Exec("ALTER INDEX gin_maint_ctrl_m SET(fastupdate=off)", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_ctrl_empty ON gin_maint_ctrl USING gin(a) WHERE id<0", 0))
	run(fixture.Exec("CREATE INDEX gin_maint_ctrl_off ON gin_maint_ctrl USING gin(v) WITH(fastupdate=off)", 0))
}
func ginMaintControlMatches(run func(fixture.Step)) {
	run(ginIDs("SELECT id FROM public.gin_maint_ctrl ORDER BY id", []int64{1, 2, 3, 4, 5}))
	run(ginIDs("SELECT id FROM public.gin_maint_ctrl WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1, 2, 5}))
	run(ginIDs("SELECT id FROM public.gin_maint_ctrl WHERE v @@ 'cat'::tsquery ORDER BY id", []int64{1, 2, 5}))
}
func ginMaintControlClean(run func(fixture.Step)) {
	for _, name := range []string{"gin_maint_ctrl_m", "gin_maint_ctrl_e"} {
		run(ginMaintCleanIndex("public."+name, true))
		run(ginMaintCleanIndex("public."+name, false))
	}
	for _, name := range []string{"gin_maint_ctrl_empty", "gin_maint_ctrl_off"} {
		run(ginMaintCleanIndex("public."+name, false))
	}
	ginMaintControlMatches(run)
}
func TestReferenceGINMaintenanceControls(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		ginMaintControlSeed(run)
		ginMaintControlMatches(run)
		if b.PG {
			// PostgreSQL accepts the exact native script but ignores native comments;
			// ordinary replay builds main and has no physical-count parity promise.
			run(fixture.Exec("DROP TABLE gin_maint_ctrl", 0))
			if emitted != "" {
				if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
					t.Fatal(err)
				}
			} else {
				ginMaintControlSeed(run)
			}
			ginMaintControlMatches(run)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		eager, err := conn.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), "SELECT id FROM public.gin_maint_ctrl ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer eager.Close()
		// The original capture remains split even after its source is cleaned.
		ginMaintControlClean(run)
		run(fixture.Exec("CREATE TEMP TABLE gin_maint_ctrl(id integer,a integer[])", 0))
		run(fixture.Exec("CREATE INDEX gin_maint_ctrl_m ON gin_maint_ctrl USING gin(a)", 0))
		run(fixture.Exec("INSERT INTO gin_maint_ctrl VALUES(90,'{2}')", 1))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.DeleteReferenceConsume(t, false, eager, nil, ginIDs("SELECT id FROM public.gin_maint_ctrl ORDER BY id", []int64{1, 2, 3, 4, 5}))
		ginMaintControlClean(run)
		run(ginMaintCleanIndex("pg_temp.gin_maint_ctrl_m", true))
		run(ginMaintCleanIndex("pg_temp.gin_maint_ctrl_m", false))
		run(ginIDs("SELECT id FROM gin_maint_ctrl ORDER BY id", []int64{90}))
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/gin-maint-independent")
		if err != nil {
			t.Fatal(err)
		}
		target := sql.OpenDB(connector)
		defer target.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		other := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, step) }
		if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ginMaintControlClean(other)
		other(fixture.Exec("INSERT INTO gin_maint_ctrl VALUES(6,'{2}','cat:1')", 1))
		other(ginMaintCleanIndex("gin_maint_ctrl_m", false))
		other(ginMaintCleanIndex("gin_maint_ctrl_e", true))
		other(ginMaintCleanIndex("gin_maint_ctrl_e", false))
		ginMaintControlMatches(run)
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		marker := ""
		for _, line := range strings.Split(dump.String(), "\n") {
			if strings.HasPrefix(line, "-- stuntgres-gin-index-v1 ") {
				marker = line
				break
			}
		}
		if marker == "" {
			t.Fatal("Dump omitted split metadata")
		}
		for _, bad := range []string{dump.String() + "\n" + marker + "\n", dump.String() + "\n-- stuntgres-gin-index-v99 invalid\n", dump.String() + "\nALTER INDEX public.gin_maint_ctrl_m SET(gin_pending_list_limit=63);"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid late restore accepted")
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed restore changed destination", err)
			}
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("canceled restore changed destination", err)
		}
	})
}

// Native control phases are shared by smoke and reference controls; PostgreSQL
// has no native Restore API, so the ordinary SQL witness remains above.
func ginMaintSmokeReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(ginMaintCleanIndex("gin_maint_v", true))
	run(ginMaintCleanIndex("gin_maint_v", false))
	ginMaintDataMatches(run, []int64{1, 4, 5, 6}, []int64{1, 5})
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/smoke-copy")
	if err != nil {
		t.Fatal(err)
	}
	target := sql.OpenDB(connector)
	defer target.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, step) }
	if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	other(ginMaintCleanIndex("gin_maint_v", true))
	other(ginMaintCleanIndex("gin_maint_v", false))
	ginMaintDataMatches(other, []int64{1, 4, 5, 6}, []int64{1, 5})
	other(fixture.Exec("INSERT INTO gin_maint VALUES(7,'{2}','cat:1 rat:2')", 1))
	other(ginMaintCleanIndex("gin_maint_v", true))
	other(ginMaintCleanIndex("gin_maint_v", false))
	ginMaintDataMatches(other, []int64{1, 4, 5, 6, 7}, []int64{1, 5, 7})
}
