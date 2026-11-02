package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ginControlSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE gin_controls(id integer,a integer[],v tsvector)", 0))
	run(fixture.Exec("CREATE INDEX gin_controls_i ON gin_controls USING gin(a,v)", 0))
	run(fixture.Exec("ALTER INDEX gin_controls_i SET(fastupdate=off,gin_pending_list_limit=64)", 0))
	run(fixture.Exec("INSERT INTO gin_controls VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{}','dog:1'),(3,NULL,NULL)", 3))
}

func ginControlCheck(run func(fixture.Step)) {
	for _, s := range []fixture.Step{
		ginIDs("SELECT id FROM gin_controls ORDER BY id", []int64{1, 2, 3}),
		ginIDs("SELECT id FROM gin_controls WHERE a @> '{2}'::integer[] ORDER BY id", []int64{1}),
		ginIDs("SELECT id FROM gin_controls WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{1}),
		fixture.Select("SELECT reloptions::text AS options FROM pg_class WHERE oid='gin_controls_i'::regclass", []string{"options"}, []string{"TEXT"}, [][]any{{"{fastupdate=off,gin_pending_list_limit=64}"}}),
		fixture.Select("SELECT c.relam::bigint,i.indclass::text,pg_get_indexdef(c.oid,1,true) AS a,pg_get_indexdef(c.oid,2,true) AS v FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE c.oid='gin_controls_i'::regclass", []string{"relam", "indclass", "a", "v"}, []string{"INT8", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2742), "10064 10073", "a", "v"}}),
	} {
		run(s)
		run(fixture.Prepared(s))
	}
}

func ginControlPostDML(run func(fixture.Step)) {
	run(fixture.Prepared(fixture.Exec("INSERT INTO gin_controls VALUES($1,$2::integer[],$3::tsvector)", 1, 4, "{2,2}", "cat:1 rat:2")))
	run(ginIDs("SELECT id FROM gin_controls WHERE a @> '{2}'::integer[] AND v @@ 'cat <-> rat'::tsquery ORDER BY id", []int64{1, 4}))
	run(fixture.Exec("UPDATE gin_controls SET a='{9}',v='dog:1' WHERE id=1", 1))
	run(ginIDs("SELECT id FROM gin_controls WHERE a && '{2}'::integer[] AND v @@ 'cat'::tsquery ORDER BY id", []int64{4}))
	run(fixture.Exec("DELETE FROM gin_controls WHERE id=4", 1))
	run(ginIDs("SELECT id FROM gin_controls WHERE a @> '{2}'::integer[] OR v @@ 'cat'::tsquery ORDER BY id", nil))
}

func TestReferenceGINControls(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		ginControlSeed(run)
		ginControlCheck(run)
		const query = "SELECT id FROM gin_controls WHERE a @> $1::integer[] AND v @@ $2::tsquery ORDER BY id"
		held := classHeld(t, p, conn, query)
		wantHeld := ginIDs(query, []int64{1}, "{2}", "cat <-> rat")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
		if b.PG {
			run(fixture.Exec("DROP TABLE gin_controls", 0))
			if emitted != "" {
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
				if _, err := conn.ExecContext(ctx, emitted); err != nil {
					t.Fatalf("PostgreSQL rejected actual native GIN Dump: %v\n%s", err, emitted)
				}
			} else {
				// Native red must not prevent the independently expected PG replay.
				ginControlSeed(run)
			}
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			create, alter := false, false
			for _, line := range strings.Split(emitted, "\n") {
				if strings.Contains(line, "CREATE INDEX") && strings.Contains(line, "gin_controls_i") {
					create = strings.Contains(line, "USING gin") && !strings.Contains(line, " WITH ")
				}
				if strings.Contains(line, "ALTER INDEX") && strings.Contains(line, "gin_controls_i") {
					alter = strings.Contains(line, "SET") && strings.Contains(line, "fastupdate") && strings.Contains(line, "gin_pending_list_limit")
				}
			}
			if !create || !alter {
				t.Fatalf("Dump must separate GIN creation and option replay: %s", emitted)
			}
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			old, err := conn.QueryContext(ctx, "SELECT id FROM gin_controls ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil || len(metadata) != 1 || metadata[0].Name() != "id" || metadata[0].DatabaseTypeName() != "INT4" {
				t.Fatal("retained metadata", metadata, err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('gin_controls','gin_controls_i')", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT oid::bigint,amname,amhandler::oid::bigint FROM pg_am WHERE amname='gin'", []string{"oid", "amname", "amhandler"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(2742), "gin", int64(333)}}))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.DeleteReferenceConsume(t, false, old, nil, ginIDs("SELECT id FROM gin_controls ORDER BY id", []int64{1, 2, 3}))
			if metadata[0].Name() != "id" || metadata[0].DatabaseTypeName() != "INT4" {
				t.Fatal("replacement mutated retained descriptor", metadata)
			}
			ginControlCheck(run)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)

			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/gin-independent")
			if err != nil {
				t.Fatal(err)
			}
			target := sql.OpenDB(connector)
			defer target.Close()
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			targetRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, recorded, recorder, s) }
			if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal("independent Driver Restore", err)
			}
			ginControlCheck(targetRun)
			ginControlPostDML(targetRun)
			ginControlCheck(run) // Independent destination mutations leave source intact.

			bad := dump.String() + "\nALTER INDEX public.gin_controls_i SET(gin_pending_list_limit=63);"
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			d := fixture.ControlsReferenceDiagnostic("22023", `value 63 out of bounds for option "gin_pending_list_limit"`)
			d.Detail = `Valid values are between "64" and "2147483647".`
			fixture.AssertDiagnostic(t, err, &d)
			canceled, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(canceled, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Restore cancellation", err)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(dump.Bytes(), after.Bytes()) {
				t.Fatal("failed replacement changed source Dump", err)
			}
		}
		ginControlCheck(run)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, wantHeld)
		ginControlPostDML(run)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(query, nil, "{2}", "cat <-> rat"))
	})
}
