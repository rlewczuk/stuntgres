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

func mcvItemsRoundTrip(t *testing.T, s ndistinctScenario, name string, want []mcvItemsExpected) {
	t.Helper()
	var saved bytes.Buffer
	if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
		t.Fatal(err)
	}
	// An independent Driver makes allocation collisions real, not just a second
	// pool pointing at the source catalog. All dictionary values must be reminted.
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name() + "-items-destination")
	if err != nil {
		t.Fatal(err)
	}
	dst := sql.OpenDB(connector)
	defer dst.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	if _, err := dst.ExecContext(s.pair.Ctx, "CREATE TYPE collision AS ENUM('x','y'); CREATE DOMAIN collision_domain AS collision; CREATE TABLE collision_table(a INT,b INT); CREATE STATISTICS collision_s ON a,b FROM collision_table"); err != nil {
		t.Fatal(err)
	}
	// Controls stay raw; every assertion uses its actual destination's
	// recording adapter, including the independent Driver's own recorder.
	for _, target := range []struct {
		control, recorded *sql.DB
		recorder          *fixture.Recorder
	}{
		{s.pair.Native, s.pair.Memory, s.pair.MemoryRecord},
		{dst, recorded, recorder},
	} {
		if err := stuntgres.Restore(s.pair.Ctx, target.control, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		func() {
			conn, err := target.recorded.Conn(s.pair.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			}()
			dest := ndistinctScenario{conn: conn, pair: s.pair, prepared: s.prepared}
			ctx := context.WithValue(s.pair.Ctx, fixture.RecorderKey{}, target.recorder)
			items := mcvItemsRead(t, ctx, dest, name)
			mcvItemsAssert(t, items, want...)
			// The independent semantic assertion above owns expectations;
			// this separately proves recorded raw consumption and closure
			// agree with those same consumed rows, including tied indexes.
			consumed := make([][]any, len(items))
			for i, item := range items {
				consumed[i] = []any{item.index, item.rawValues, item.rawNulls, item.frequency, item.baseline}
			}
			fixture.ViewRecorded(t, target.recorder, mcvItemsQuery, mcvItemsNames, mcvItemsTypes, consumed, false)
		}()
		var again bytes.Buffer
		if err := stuntgres.Dump(s.pair.Ctx, target.control, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(saved.Bytes(), again.Bytes()) {
			t.Fatal("inspection changed deterministic semantic redump")
		}
	}
}

func mcvItemsStaleRestore(t *testing.T, s ndistinctScenario) {
	t.Helper()
	// Five-row facts survive despite a completely empty restored live table.
	s.run(fixture.Exec("DELETE FROM mcv_t", 5))
	mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
	mcvItemsRoundTrip(t, s, "mcv_s", mcvItemsFive)
	s.run(fixture.Select("SELECT count(*) FROM mcv_t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
}

func TestReferenceStatisticsMCVItemsControls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
		s.run(fixture.Exec("CREATE VIEW mcv_items_saved AS SELECT m.* FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid CROSS JOIN LATERAL pg_mcv_list_items(d.stxdmcv) m WHERE s.stxname='mcv_s' AND NOT d.stxdinherit", 0))
		if s.pg {
			s.run(fixture.Exec("DELETE FROM mcv_t", 5))
			mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
			return // The shared SQL witness is stale; native controls have no PG API.
		}
		mcvItemsStaleRestore(t, s)
		rows, err := s.conn.QueryContext(s.pair.Ctx, "SELECT * FROM mcv_items_saved ORDER BY index")
		if err != nil {
			t.Fatal(err)
		}
		mcvItemsAssert(t, mcvItemsConsume(t, rows, false), mcvItemsFive...)
		var saved bytes.Buffer
		if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
			t.Fatal(err)
		}
		s.run(fixture.Exec("CREATE TABLE mcv_items_sentinel(v INT)", 0))
		s.run(fixture.Exec("INSERT INTO mcv_items_sentinel VALUES(77)", 1))
		checkSentinel := func() {
			t.Helper()
			s.run(fixture.Select("SELECT v FROM mcv_items_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
			mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
		}
		if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, strings.NewReader(saved.String()+"\nthis is not SQL;\n")); err == nil {
			t.Fatal("malformed replacement succeeded")
		}
		checkSentinel()
		ctx, cancel := context.WithCancel(s.pair.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled restore", err)
		}
		checkSentinel()
		old, err := s.conn.QueryContext(s.pair.Ctx, mcvItemsQuery, "mcv_s")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
			t.Fatal(err)
		}
		mcvItemsCheck(t, s, "mcv_s")
		if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
		mcvItemsAssert(t, mcvItemsConsume(t, old, false), mcvItemsFive...)
		for i, column := range meta {
			if column.Name() != mcvItemsNames[i] || column.DatabaseTypeName() != mcvItemsTypes[i] {
				t.Fatal("captured metadata changed", i, column)
			}
		}
	})
	t.Run("typed-destination-remint", func(t *testing.T) {
		withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
			for _, q := range []string{"CREATE TYPE mcv_items_enum AS ENUM('z','a')", "CREATE DOMAIN mcv_items_element AS mcv_items_enum", "CREATE DOMAIN mcv_items_array AS int[]"} {
				s.run(fixture.Exec(q, 0))
			}
			if !s.pg {
				s.run(fixture.Exec("CREATE OPERATOR CLASS mcv_items_array_ops DEFAULT FOR TYPE int[] USING btree AS STORAGE int[]", 0))
			}
			for _, q := range []string{"CREATE TABLE mcv_items_remint(a mcv_items_array,b INT,c mcv_items_element)", "CREATE STATISTICS mcv_items_remint_s(mcv) ON a,b,c FROM mcv_items_remint"} {
				s.run(fixture.Exec(q, 0))
			}
			s.run(fixture.Exec("INSERT INTO mcv_items_remint VALUES('[0:2]={7,NULL,9}',1,'z'),('[0:2]={7,NULL,9}',1,'z'),('[0:2]={7,NULL,9}',2,'a')", 3))
			s.run(fixture.Exec("ANALYZE mcv_items_remint", 0))
			want := []mcvItemsExpected{{[]any{"[0:2]={7,NULL,9}", "1", "z"}, 2.0 / 3, 4.0 / 9}, {[]any{"[0:2]={7,NULL,9}", "2", "a"}, 1.0 / 3, 1.0 / 9}}
			mcvItemsCheck(t, s, "mcv_items_remint_s", want...)
			s.run(fixture.Exec("DELETE FROM mcv_items_remint", 3))
			if !s.pg {
				mcvItemsRoundTrip(t, s, "mcv_items_remint_s", want)
			}
			mcvItemsCheck(t, s, "mcv_items_remint_s", want...)
		})
	})
}
