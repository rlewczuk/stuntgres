package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePGStatAllTablesSnapshotsAndLifetime(t *testing.T) {
	tableStatSnapshotsAndLifetime(t, tableStatFamily{})
}

func tableStatSnapshotsAndLifetime(t *testing.T, f tableStatFamily) {
	aSchema, bSchema, movedSchema := "sat_cache_a", "sat_cache_b", "sat_moved"
	if f.system {
		aSchema, bSchema, movedSchema = "information_schema", "information_schema", "public"
	} else {
		fixture.SchemaReferenceCleanup(t, aSchema)
		fixture.SchemaReferenceCleanup(t, bSchema)
		fixture.SchemaReferenceCleanup(t, movedSchema)
	}
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !f.system {
			for _, schema := range []string{aSchema, bSchema, movedSchema} {
				fixture.StatisticsExec(t, p, b, c, "CREATE SCHEMA "+schema, 0)
			}
		}
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		for _, mode := range []string{"none", "cache", "snapshot"} {
			for _, key := range []string{"relid", "schemaname", "relname"} {
				t.Run(mode+"/"+key, func(t *testing.T) {
					nameA, nameB := "sat_"+mode+"_"+key+"_a", "sat_"+mode+"_"+key+"_b"
					a, bname := aSchema+"."+nameA, bSchema+"."+nameB
					for _, name := range []string{a, bname} {
						f.create(t, p, b, c, name)
						w, retire := f.reporting(t, p, b)
						fixture.StatisticsExec(t, p, b, w, "INSERT INTO "+name+" VALUES(1,'a'),(2,NULL),(3,'c')", 3)
						retire()
					}
					for _, name := range []string{nameA, nameB} {
						f.await(t, p, b, name, "seed3", func(r tableStatRow) bool { return r.Inserts == 3 })
					}
					f.build(t, p, b, nameA, 1, 3, "CREATE INDEX "+nameA+"_idx ON "+a+" USING hash(n)")
					f.build(t, p, b, nameB, 1, 3, "CREATE INDEX "+nameB+"_idx ON "+bname+" USING hash(n)")
					reader := f.connection(t, p, b)
					fixture.StatisticsExec(t, p, b, reader, "SET stats_fetch_consistency="+mode, 0)
					fixture.StatisticsExec(t, p, b, reader, "BEGIN", 0)
					defer reader.ExecContext(context.Background(), "ROLLBACK")
					predicate, arg := "relname=$1", any(nameA)
					if key == "schemaname" {
						predicate, arg = "schemaname=$1", aSchema
					}
					if key == "relid" {
						predicate, arg = "relid=$1", f.get(t, p, c, nameA).ID
					}
					// The public SYS fixtures share the supported system namespace.
					// Identity is first because Keep only pushes the first-evaluated
					// conjunct. Composed Reader coverage proves pure schema rejection.
					q := "SELECT * FROM " + f.view() + " WHERE " + predicate
					args := []any{arg}
					if f.system && key == "schemaname" {
						q = "SELECT * FROM " + f.view() + " WHERE relid=$1 AND schemaname=$2"
						args = []any{f.get(t, p, c, nameA).ID, aSchema}
					}
					rows, err := reader.QueryContext(p.Ctx, q, args...)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for rows.Next() {
						var r tableStatRow
						if err := rows.Scan(r.destinations()...); err != nil {
							t.Fatal(err)
						}
						if r.Name == nameA {
							found = true
							if r.Seq != 1 || r.Read != 3 || !r.Index.Valid || r.Index.Int64 != 0 {
								t.Fatal("initial post-build reader", r)
							}
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if !found {
						t.Fatal("A row not captured")
					}
					w, retire := f.reporting(t, p, b)
					for _, name := range []string{a, bname} {
						tableStatSeqPrefer(t, p, b, w)
						fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM "+name+" WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
						fixture.StatisticsExec(t, p, b, w, "SET enable_indexscan=on", 0)
						indexStatPrefer(t, p, b, w)
						if !indexStatSimple(t, b, indexStatPlan(t, p, w, "SELECT payload FROM "+name+" WHERE n=2")) {
							t.Fatal("cache writer not indexed")
						}
						fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM "+name+" WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
					}
					retire()
					for _, name := range []string{nameA, nameB} {
						f.await(t, p, b, name, "heap+index publication", func(r tableStatRow) bool { return r.Seq == 2 && r.Index.Valid && r.Index.Int64 == 1 })
					}
					wantA, wantB := int64(0), int64(1)
					if mode == "none" {
						wantA = 1
					}
					if mode == "snapshot" {
						wantB = 0
					}
					for _, tc := range []struct {
						name string
						want int64
					}{{nameA, wantA}, {nameB, wantB}} {
						r := f.get(t, p, reader, tc.name)
						if r.Seq != 1+tc.want || r.Read != 3+3*tc.want || !r.Index.Valid || r.Index.Int64 != tc.want || !r.Fetch.Valid || r.Fetch.Int64 != tc.want {
							t.Fatal("predicate-before-reader cache semantics", mode, key, tc, r)
						}
					}
					fixture.StatisticsClearSnapshot(t, p, b, reader)
					for _, name := range []string{nameA, nameB} {
						r := f.get(t, p, reader, name)
						if r.Seq != 2 || r.Read != 6 {
							t.Fatal("clear reader cut", r)
						}
						tableStatIndexWant(t, r, 1, 1)
					}
					fixture.StatisticsExec(t, p, b, reader, "COMMIT", 0)
				})
			}
		}
		if f.system {
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, schema := range []string{"information_schema", "public"} {
					if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+schema+".sat_renamed CASCADE"); err != nil {
						t.Error("renamed SYS cleanup", err)
					}
				}
			})
		}
		f.create(t, p, b, c, "sat_lifetime")
		f.seed(t, p, b, "sat_lifetime")
		old := f.get(t, p, c, "sat_lifetime")
		w, retire := f.reporting(t, p, b)
		for _, q := range []string{"SET track_counts=off", "SET track_activities=off", "SET track_functions=none", "SET track_io_timing=off"} {
			fixture.StatisticsExec(t, p, b, w, q, 0)
		}
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM sat_lifetime WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
		fixture.StatisticsExec(t, p, b, w, "ANALYZE sat_lifetime", 0)
		fixture.StatisticsExec(t, p, b, w, "SET track_counts=on", 0)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM sat_lifetime WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
		retire()
		positive := f.await(t, p, b, "sat_lifetime", "future-only counts enable", func(r tableStatRow) bool { return r.Seq == 1 })
		if positive.Read != 3 || positive.Analyze != 0 {
			t.Fatal("counts policy or unrelated settings gate", positive)
		}
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE sat_lifetime", 0)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		if got := f.get(t, p, c, "sat_lifetime"); !got.equal(positive) {
			t.Fatal("rolled-back drop changed history", got)
		}
		heldConn := f.connection(t, p, b)
		held, err := heldConn.QueryContext(p.Ctx, f.query(), "sat_lifetime")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldColumns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		tableStatMetadata(t, heldColumns, b.PG)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE sat_lifetime RENAME TO sat_renamed", 0)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE sat_renamed SET SCHEMA "+movedSchema, 0)
		moved := tableStatGet(t, p, c, "sat_renamed")
		if moved.ID != positive.ID || moved.Schema != movedSchema || moved.Seq != 1 || moved.Inserts != 3 {
			t.Fatal("qualified identity reminted", moved)
		}
		dropSchema := movedSchema
		if f.system {
			if _, err := f.read(p.Ctx, c, "sat_renamed"); err != sql.ErrNoRows {
				t.Fatal("moved out of SYS", err)
			}
			fixture.StatisticsExec(t, p, b, c, "ALTER TABLE public.sat_renamed SET SCHEMA information_schema", 0)
			if got := f.get(t, p, c, "sat_renamed"); got.ID != positive.ID || got.Inserts != 3 || got.Seq != 1 {
				t.Fatal("moved back into SYS lost history", got)
			}
			dropSchema = "information_schema"
		}
		fixture.StatisticsExec(t, p, b, c, "DROP TABLE "+dropSchema+".sat_renamed", 0)
		if !held.Next() {
			t.Fatal("retained named row missing", held.Err())
		}
		var retainedNamed tableStatRow
		if err := held.Scan(retainedNamed.destinations()...); err != nil {
			t.Fatal(err)
		}
		held.Close()
		if !retainedNamed.equal(positive) {
			t.Fatal("retained result changed through rename/move/drop", retainedNamed, positive)
		}
		tableStatMetadata(t, heldColumns, b.PG)
		if _, err := f.read(p.Ctx, c, "sat_renamed"); err != sql.ErrNoRows {
			t.Fatal("dropped descriptor resurrected", err)
		}
		f.create(t, p, b, c, "sat_lifetime")
		fresh := f.get(t, p, c, "sat_lifetime")
		if fresh.ID == old.ID || fresh.Inserts != 0 || fresh.Seq != 0 || fresh.LastSeq.Valid {
			t.Fatal("same-name incarnation inherited history", fresh, old)
		}
		if old.Seq != 0 || old.Inserts != 3 || positive.Seq != 1 || positive.Schema != old.Schema {
			t.Fatal("retained rows mutated", old, positive)
		}
	})
}

func TestReferencePGStatAllTablesNativeControls(t *testing.T) {
	tableStatNativeControls(t, tableStatFamily{})
}

func tableStatNativeControls(t *testing.T, f tableStatFamily) {
	var emitted []byte
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			if f.system {
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					if _, err := b.DB.ExecContext(ctx, "DROP TABLE IF EXISTS information_schema.sat_control_rows CASCADE"); err != nil {
						t.Error("replayed SYS cleanup", err)
					}
				})
			}
			if len(emitted) == 0 {
				t.Fatal("missing actual native dump")
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("real PostgreSQL dump replay", err)
			}
			f.setup(t, p, b, c)
			fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
			initial := f.get(t, p, c, "sat_control_rows")
			if initial.Seq != 0 || initial.LastSeq.Valid {
				t.Fatal("dump serialized runtime history", initial)
			}
			w, retire := f.reporting(t, p, b)
			tableStatSeqPrefer(t, p, b, w)
			fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
			retire()
			f.await(t, p, b, "sat_control_rows", "fresh oracle execution", func(r tableStatRow) bool { return r.Seq == 1 && r.Read == 3 })
			return
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("table-stat-source")
		if err != nil {
			t.Fatal(err)
		}
		source := sql.OpenDB(connector)
		defer source.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		sourceBackend := b
		sourceBackend.DB, sourceBackend.Recorder = recorded, recorder
		sourceConn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer sourceConn.Close()
		f.setup(t, p, sourceBackend, sourceConn)
		tableStatCreate(t, p, sourceBackend, sourceConn, "sat_control_rows")
		f.seed(t, p, sourceBackend, "sat_control_rows")
		sourceWriter, retireSource := f.reporting(t, p, sourceBackend)
		fixture.RunStep(t, p.Ctx, sourceWriter, recorder, fixture.Select("SELECT payload FROM sat_control_rows WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
		retireSource()
		f.await(t, p, sourceBackend, "sat_control_rows", "source positive history", func(r tableStatRow) bool { return r.Seq == 1 && r.Read == 3 })
		fixture.StatisticsExec(t, p, sourceBackend, sourceConn, "CREATE TEMP TABLE sat_source_temp(n integer)", 0)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, source, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if bytes.Contains(emitted, []byte("seq_scan")) || bytes.Contains(emitted, []byte("last_seq_scan")) {
			t.Fatal("runtime monitoring serialized")
		}
		f.create(t, p, b, c, "sat_control_rows")
		f.seed(t, p, b, "sat_control_rows")
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		w, retire := f.reporting(t, p, b)
		fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT payload FROM sat_control_rows WHERE n=2", []string{"payload"}, []string{"TEXT"}, [][]any{{nil}}))
		retire()
		old := f.await(t, p, b, "sat_control_rows", "destination positive history", func(r tableStatRow) bool { return r.Seq == 1 && r.Read == 3 })
		keeper := fixture.IdentityConnection(t, p.Ctx, b.DB)
		for _, step := range []fixture.Step{fixture.Exec("CREATE TEMP TABLE sat_control_temp(n integer,payload text)", 0), fixture.Exec("INSERT INTO sat_control_temp VALUES(1,NULL),(2,'b')", 2), fixture.Exec("CREATE INDEX sat_temp_index ON sat_control_temp USING hash(n)", 0), fixture.Select("SELECT n FROM sat_control_temp ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})} {
			fixture.RunStep(t, p.Ctx, keeper, b.Recorder, step)
		}
		ctx, cancel := context.WithTimeout(p.Ctx, fixture.Liveness)
		defer cancel()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		var temp tableStatRow
		for {
			var one int
			if err := keeper.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
				t.Fatal(err)
			}
			temp, err = tableStatRead(ctx, c, "sat_control_temp")
			if err != nil {
				t.Fatal(err)
			}
			if temp.Seq == 2 && temp.Inserts == 2 {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("TEMP publication", temp, ctx.Err())
			case <-tick.C:
			}
		}
		if temp.Read != 4 {
			t.Fatal("real TEMP build and query fetches", temp)
		}
		heldConn := fixture.IdentityConnection(t, p.Ctx, b.DB)
		var heldSys *sql.Rows
		var heldSysColumns []*sql.ColumnType
		if f.system {
			heldSys, err = heldConn.QueryContext(p.Ctx, f.query(), "sat_control_rows")
			if err != nil {
				t.Fatal(err)
			}
			defer heldSys.Close()
			heldSysColumns, err = heldSys.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			tableStatMetadata(t, heldSysColumns, false)
		}
		held, err := heldConn.QueryContext(p.Ctx, tableStatSelect, "sat_control_temp")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldColumns, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.StatisticsExec(t, p, b, keeper, "SET track_counts=off", 0)
		unchanged := func() {
			t.Helper()
			if got := f.get(t, p, c, "sat_control_rows"); !got.equal(old) {
				t.Fatal("failed Restore changed persistent identity/history", got, old)
			}
			if got := tableStatGet(t, p, c, "sat_control_temp"); !got.equal(temp) {
				t.Fatal("failed Restore changed TEMP identity/history", got, temp)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
			fixture.RunStep(t, p.Ctx, keeper, b.Recorder, fixture.Select("SELECT n,payload FROM sat_control_temp ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), nil}, {int64(2), "b"}}))
		}
		bad := append(bytes.Clone(emitted), []byte("\nCREATE TABLE sat_bad(n no_such_type);")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
			t.Fatal("late invalid Restore accepted")
		}
		unchanged()
		cancelled, stop := context.WithCancel(p.Ctx)
		stop()
		if err := stuntgres.Restore(cancelled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		unchanged()
		during, cancelInput := context.WithCancel(p.Ctx)
		input := &indexStatCancelReader{Reader: bytes.NewReader(emitted), cancel: cancelInput}
		err = stuntgres.Restore(during, p.Native, input)
		cancelInput()
		if !errors.Is(err, context.Canceled) || !input.entered {
			t.Fatal("admitted external input cancellation", input.entered, err)
		}
		unchanged()
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fresh := f.get(t, p, c, "sat_control_rows")
		if fresh.Inserts != 0 || fresh.Seq != 0 || fresh.Read != 0 || fresh.LastSeq.Valid {
			t.Fatal("persistent history crossed replacement", fresh)
		}
		if got := tableStatGet(t, p, c, "sat_control_temp"); !got.equal(temp) {
			t.Fatal("surviving TEMP did not transplant", got, temp)
		}
		if _, err := tableStatRead(p.Ctx, c, "sat_source_temp"); err != sql.ErrNoRows {
			t.Fatal("foreign source TEMP imported", err)
		}
		if !held.Next() {
			t.Fatal("retained TEMP row missing", held.Err())
		}
		var retained tableStatRow
		if err := held.Scan(retained.destinations()...); err != nil {
			t.Fatal(err)
		}
		held.Close()
		if !retained.equal(temp) {
			t.Fatal("retained cursor changed through replacement", retained, temp)
		}
		tableStatMetadata(t, heldColumns, false)
		if f.system {
			if !heldSys.Next() {
				t.Fatal("held SYS row lost", heldSys.Err())
			}
			var retainedSys tableStatRow
			if err := heldSys.Scan(retainedSys.destinations()...); err != nil {
				t.Fatal(err)
			}
			heldSys.Close()
			if !retainedSys.equal(old) {
				t.Fatal("held SYS changed through replacement", retainedSys, old)
			}
			tableStatMetadata(t, heldSysColumns, false)
		}
		fixture.StatisticsExec(t, p, b, c, "SET track_counts=off", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT n,payload FROM sat_control_rows ORDER BY n", []string{"n", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}}))
		var resetHeld *sql.Rows
		var resetColumns []*sql.ColumnType
		if f.system {
			resetConn := f.connection(t, p, b)
			resetHeld, err = resetConn.QueryContext(p.Ctx, f.query(), "sat_control_rows")
			if err != nil {
				t.Fatal(err)
			}
			defer resetHeld.Close()
			resetColumns, err = resetHeld.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			tableStatMetadata(t, resetColumns, false)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if _, err := f.read(p.Ctx, c, "sat_control_rows"); err != sql.ErrNoRows {
			t.Fatal("Reset retained persistent relation", err)
		}
		if got := tableStatGet(t, p, c, "sat_control_temp"); !got.equal(temp) {
			t.Fatal("Reset lost attached TEMP history", got, temp)
		}
		if f.system {
			if !resetHeld.Next() {
				t.Fatal("held SYS lost through Reset", resetHeld.Err())
			}
			var retainedReset tableStatRow
			if err := resetHeld.Scan(retainedReset.destinations()...); err != nil {
				t.Fatal(err)
			}
			resetHeld.Close()
			if !retainedReset.equal(fresh) {
				t.Fatal("Reset changed retained SYS row", retainedReset, fresh)
			}
			tableStatMetadata(t, resetColumns, false)
		}
		if len(emitted) == 0 {
			t.Fatal("empty definitions/data dump")
		}
	})
}
