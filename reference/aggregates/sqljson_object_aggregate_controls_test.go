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

func sqlJSONObjectAggregateControls(t *testing.T, emitted *string) {
	t.Helper()
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		const q = "SELECT g,j,b,t FROM sjo_view ORDER BY g"
		names := []string{"g", "j", "b", "t"}
		types := []string{"INT4", "JSON", "JSONB", "VARCHAR"}
		want := [][]any{{int64(1), `{ "a" : 1 }`, `{"a": 1}`, `{ "a" : 1 }`}, {int64(2), `{ "a" : [2] }`, `{"a": [2]}`, `{ "a" : [2] }`}}
		s := fixture.Select(q, names, types, want)
		if b.PG {
			if *emitted == "" {
				t.Fatal("missing actual native dump")
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
				copy := s
				copy.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, copy)
			}
			portable := sqlJSONObjectPrepareReplacementHolds(t, p, b)
			portable.checkValues(t, p, b, true, `{"a": 1}`)
			return
		}
		sqlJSONObjectAggregateConsumerSetup(t, p, b)
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN sjo_binary AS jsonb", 0),
			fixture.Exec("CREATE VIEW sjo_view AS SELECT g,json_objectagg(k VALUE v FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING json) AS j,json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING sjo_binary) AS b,json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITHOUT UNIQUE KEYS RETURNING varchar(40) FORMAT JSON) AS t FROM sjo_input GROUP BY g", 0),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
		jsonArrayAggregateRoutes(t, p, b, s)
		preReplacement := sqlJSONObjectPrepareReplacementHolds(t, p, b)
		preReplacement.checkValues(t, p, b, true, `{"a": 1}`)
		held, err := b.DB.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		*emitted = dump.String()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE sjo_input SET v='[9]' WHERE id=1", 1))
		changed := s
		changed.Rows = [][]any{{int64(1), `{ "a" : [9] }`, `{"a": [9]}`, `{ "a" : [9] }`}, want[1]}
		jsonArrayAggregateRoutes(t, p, b, changed)
		preReplacement.checkValues(t, p, b, true, `{"a": [9]}`)
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(*emitted)); err != nil {
			t.Fatal(err)
		}
		preReplacement.checkValues(t, p, b, false, `{"a": 1}`)
		preReplacement.checkFenced(t, p.Ctx)
		// These catalog-typed statements belong to the new lifetime. Failed and
		// canceled replacement must leave them eligible, unlike the retired pair.
		current := sqlJSONObjectPrepareReplacementHolds(t, p, b)
		assertRestored := func() {
			t.Helper()
			jsonArrayAggregateRoutes(t, p, b, s)
			preReplacement.checkValues(t, p, b, false, `{"a": 1}`)
			preReplacement.checkFenced(t, p.Ctx)
			current.checkValues(t, p, b, true, `{"a": 1}`)
			var again bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if again.String() != *emitted {
				t.Fatal("symbolic options or remapped types changed on redump")
			}
		}
		assertRestored()
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(*emitted+"\nCREATE TABLE sjo_invalid(v missing_sjo_type);")); err == nil {
			t.Fatal("late invalid restore published")
		}
		assertRestored()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err = stuntgres.Restore(canceled, p.Native, strings.NewReader(*emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		assertRestored()
		after, err := held.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("retained metadata changed", err)
		}
		fixture.ScalarRows(t, false, held, names, types, want, false)
		// Also rebind the stored domain and typmod wrappers into a fresh engine.
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("sjo-fresh/" + t.Name())
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
		fixture.ScalarRows(t, false, rows, names, types, want, false)
	})
}

func TestReferenceSQLJSONObjectAggregateControls(t *testing.T) {
	var emitted string
	sqlJSONObjectAggregateControls(t, &emitted)
}

func TestReferenceSQLJSONObjectAggregateFeatureSmoke(t *testing.T) {
	t.Run("behavior", func(t *testing.T) {
		sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, tc := range []struct {
				q, typ string
				value  any
			}{
				{"SELECT json_objectagg('a' VALUE 1) AS v", "JSON", `{ "a" : 1 }`},
				{"SELECT json_objectagg('a':1 RETURNING jsonb) AS v", "JSONB", `{"a": 1}`},
				{"SELECT json_objectagg('a':NULL) AS v", "JSON", `{ "a" : null }`},
				{"SELECT json_objectagg('a':NULL ABSENT ON NULL) AS v", "JSON", `{  }`},
				{"SELECT json_objectagg('a':1) AS v WHERE FALSE", "JSON", nil},
				{"SELECT json_objectagg('a':'[1]'::text FORMAT JSON RETURNING bytea FORMAT JSON ENCODING UTF8) AS v", "BYTEA", `{ "a" : [1] }`},
				{"SELECT json_objectagg(k:v WITHOUT UNIQUE) AS v FROM(VALUES('a',1),('a',2)) t(k,v)", "JSON", `{ "a" : 1, "a" : 2 }`},
				{"SELECT json_objectagg(k:v WITHOUT UNIQUE RETURNING jsonb) AS v FROM(VALUES('a',1),('a',2)) t(k,v)", "JSONB", `{"a": 2}`},
			} {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(tc.q, tc.typ, tc.value))
			}
			for _, suffix := range []string{"", " RETURNING jsonb", " RETURNING jsonb FORMAT JSON"} {
				binary := suffix == " RETURNING jsonb"
				q := "SELECT json_objectagg(k:v ABSENT ON NULL WITH UNIQUE KEYS" + suffix + ") FROM(VALUES('a',NULL::int),('a',NULL::int)) t(k,v)"
				jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(binary, `"a"`), false)
				typ := "JSON"
				if binary {
					typ = "JSONB"
				}
				jsonArrayAggregateError(t, p, b, "SELECT json_objectagg(NULL:NULL ABSENT ON NULL"+suffix+")", jsonObjectAggregateNullKey(typ), false)
			}
			jsonArrayAggregateError(t, p, b, "SELECT json_objectagg('a':1 RETURNING varchar(10))", fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(10)"), false)
			malformed := "/*é😀*/ SELECT json_objectagg('a',1)"
			d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ","`)
			d.Position = jsonArrayAggregatePosition(malformed, ",")
			jsonArrayAggregateError(t, p, b, malformed, d, true)
			sqlJSONObjectAggregateConsumerSetup(t, p, b)
			sqlJSONObjectAggregateHeld(t, p, b)
			// Reset the same fixture's source after held execution for atomic consumers.
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE sjo_input SET v='1' WHERE id=1", 1))
			sqlJSONObjectAggregateConsumers(t, p, b)
		})
	})
	t.Run("stored-options", func(t *testing.T) { var emitted string; sqlJSONObjectAggregateControls(t, &emitted) })
}
