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

func jsonObjectUniqueAggregateControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE jou_control_source(id int,k text,v int)", 0),
		fixture.Exec("INSERT INTO jou_control_source VALUES(1,'b',1),(2,'a',2),(3,'c',NULL)", 3),
		fixture.Exec("CREATE VIEW jou_control_view AS SELECT json_object_agg_unique(k,v ORDER BY id) FILTER(WHERE id>0) AS j,jsonb_object_agg_unique(DISTINCT k,v ORDER BY k,v) AS b,json_object_agg_unique_strict(k,v ORDER BY id) AS s,jsonb_object_agg_unique_strict(k,v ORDER BY id) AS t FROM jou_control_source", 0),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func jsonObjectUniqueAggregateControls(t *testing.T, emitted *string) {
	t.Helper()
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const q = "SELECT j,b,s,t FROM jou_control_view"
		names := []string{"j", "b", "s", "t"}
		types := []string{"JSON", "JSONB", "JSON", "JSONB"}
		want := [][]any{{`{ "b" : 1, "a" : 2, "c" : null }`, `{"a": 2, "b": 1, "c": null}`, `{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`}}
		s := fixture.Select(q, names, types, want)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayAggregateViewMetadata(t, r, b.PG, names, types, nil) }
		if b.PG {
			if *emitted == "" {
				t.Fatal("native dump missing: PostgreSQL must replay actual emitted SQL")
			}
			t.Run("actual-native-emitted-replay", func(t *testing.T) {
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
		jsonObjectUniqueAggregateControlSetup(t, p, b)
		jsonArrayAggregateRoutes(t, p, b, s)
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		heldStmt := fixture.HeldStatement{b.DB, stmt, q}
		held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder))
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE jou_unrelated(v int)", 0))
		fixture.RunStep(t, p.Ctx, heldStmt, b.Recorder, s)
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		*emitted = dump.String()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO jou_control_source VALUES(4,'d',9)", 1))
		changed := s
		changed.Rows = [][]any{{`{ "b" : 1, "a" : 2, "c" : null, "d" : 9 }`, `{"a": 2, "b": 1, "c": null, "d": 9}`, `{ "b" : 1, "a" : 2, "d" : 9 }`, `{"a": 2, "b": 1, "d": 9}`}}
		fixture.RunStep(t, p.Ctx, heldStmt, b.Recorder, changed)
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
				t.Fatal("replacement changed dump")
			}
			fixture.RunStep(t, p.Ctx, heldStmt, b.Recorder, s)
		}
		assertRestored()
		// Both root flags must survive remapping: omitted c still reserves its key.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO jou_control_source VALUES(5,'c',7)", 1))
		for _, f := range jsonObjectUniqueAggregateFamilies {
			jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,v ORDER BY id) FROM jou_control_source", sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"c"`), false)
		}
		failure := fixture.ErrorDiagnostic(q, sqlJSONObjectAggregateDuplicate(false, `"c"`))
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, heldStmt, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM jou_control_source WHERE id=5", 1))
		assertRestored()
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(*emitted+"\nCREATE TABLE jou_invalid(v missing_jou_type);")); err == nil {
			t.Fatal("invalid replacement succeeded")
		}
		assertRestored()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(ctx, p.Native, strings.NewReader(*emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		assertRestored()
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("json-object-unique-fresh/" + t.Name())
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
		jsonArrayAggregateViewMetadata(t, held, false, names, types, nil)
		after, err := held.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("retained metadata=%v/%v", after, err)
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
			t.Fatalf("retained=%#v/%v", raw, held.Err())
		}
	})
}

func TestReferenceJSONObjectUniqueAggregateControls(t *testing.T) {
	var emitted string
	jsonObjectUniqueAggregateControls(t, &emitted)
}

func TestReferenceJSONObjectUniqueAggregateFeatureSmoke(t *testing.T) {
	var emitted string
	t.Run("controls", func(t *testing.T) { jsonObjectUniqueAggregateControls(t, &emitted) })
	t.Run("behavior", func(t *testing.T) {
		jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			jsonObjectUniqueAggregateSetup(t, p, b)
			jsonObjectUniqueAggregateCore(t, p, b)
			jsonObjectUniqueAggregateHeld(t, p, b)
			jsonObjectUniqueAggregateModifierSmoke(t, p, b)
			jsonObjectUniqueAggregateConsumers(t, p, b)
			for _, f := range jsonObjectUniqueAggregateFamilies {
				d := sqlJSONObjectAggregateDuplicate(false, `"a"`)
				if f.typ == "JSONB" {
					d = jsonObjectAggregateNullKey(f.typ)
				}
				jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(k,v ORDER BY id) FROM(VALUES(1,'a',1),(2,'a',2),(3,NULL,NULL)) x(id,k,v)", d, false)
			}
		})
	})
}
