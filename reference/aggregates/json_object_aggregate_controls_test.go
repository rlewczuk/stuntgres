package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectAggregateControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE joa_control_source(id int,k text,v int)", 0),
		fixture.Exec("INSERT INTO joa_control_source VALUES(1,'b',1),(2,'a',2),(3,'a',NULL)", 3),
		fixture.Exec("CREATE VIEW joa_control_view AS SELECT json_object_agg(k,v ORDER BY id) FILTER(WHERE id>0) AS j,jsonb_object_agg(k,v ORDER BY id) AS b,json_object_agg_strict(k,v ORDER BY id) AS s,jsonb_object_agg_strict(k,v ORDER BY id) AS t FROM joa_control_source", 0),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func jsonObjectAggregateControls(t *testing.T, emitted *string) {
	t.Helper()
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const q = "SELECT j,b,s,t FROM joa_control_view"
		names := []string{"j", "b", "s", "t"}
		types := []string{"JSON", "JSONB", "JSON", "JSONB"}
		want := [][]any{{`{ "b" : 1, "a" : 2, "a" : null }`, `{"a": null, "b": 1}`, `{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`}}
		s := fixture.Select(q, names, types, want)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayAggregateViewMetadata(t, r, b.PG, names, types, nil) }
		if b.PG {
			t.Run("actual-native-emitted-replay", func(t *testing.T) {
				if *emitted == "" {
					t.Fatal("native dump missing: PostgreSQL must replay actual emitted SQL")
				}
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				if _, err = conn.ExecContext(p.Ctx, *emitted); err != nil {
					t.Fatal(err)
				}
				for _, prepared := range []bool{false, true} {
					replay := s
					replay.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, replay)
				}
			})
			return
		}
		jsonObjectAggregateControlSetup(t, p, b)
		jsonArrayAggregateRoutes(t, p, b, s)
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := fixture.HeldStatement{b.DB, stmt, q}
		held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE joa_unrelated(v int)", 0))
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		*emitted = dump.String()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO joa_control_source VALUES(4,'c',9)", 1))
		changed := s
		changed.Rows = [][]any{{`{ "b" : 1, "a" : 2, "a" : null, "c" : 9 }`, `{"a": null, "b": 1, "c": 9}`, `{ "b" : 1, "a" : 2, "c" : 9 }`, `{"a": 2, "b": 1, "c": 9}`}}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, changed)
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(*emitted)); err != nil {
			t.Fatal(err)
		}
		assertRestored := func() {
			t.Helper()
			var redump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if redump.String() != *emitted {
				t.Fatal("replacement changed native dump")
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		}
		assertRestored()
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(*emitted+"\nCREATE TABLE joa_invalid(v missing_joa_type);")); err == nil {
			t.Fatal("invalid late replacement succeeded")
		}
		assertRestored()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(*emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		assertRestored()
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("json-object-aggregate-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(*emitted)); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		jsonArrayAggregateViewMetadata(t, rows, false, names, types, nil)
		fixture.ScalarRows(t, false, rows, names, types, want, false)
		// This cursor predates source mutation AND actual Reset/Restore. Its
		// captured data/schema must not rebind along with the held statement.
		jsonArrayAggregateViewMetadata(t, held, false, names, types, nil)
		after, err := held.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("retained metadata changed: %v", err)
		}
		if !held.Next() {
			t.Fatal("missing retained result", held.Err())
		}
		raw := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err = held.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range raw {
			fixture.AssertRawValue(t, false, types[i], value)
			raw[i] = fixture.Normalize(value)
		}
		if !reflect.DeepEqual(raw, want[0]) || held.Next() || held.Err() != nil {
			t.Fatalf("retained=%#v err=%v", raw, held.Err())
		}
	})
}

func TestReferenceJSONObjectAggregateControls(t *testing.T) {
	var emitted string
	jsonObjectAggregateControls(t, &emitted)
}

func TestReferenceJSONObjectAggregateFeatureSmoke(t *testing.T) {
	var emitted string
	t.Run("controls", func(t *testing.T) { jsonObjectAggregateControls(t, &emitted) })
	t.Run("behavior", func(t *testing.T) {
		jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			jsonObjectAggregateSetup(t, p, b)
			jsonObjectAggregateCore(t, p, b)
			jsonObjectAggregateConvertedNulls(t, p, b)
			jsonObjectAggregateHeld(t, p, b)
			jsonObjectAggregateModifierSmoke(t, p, b)
			jsonObjectAggregateConsumers(t, p, b)
		})
	})
}
