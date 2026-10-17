package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func materializedViewSnapshot(schema, name string, populated bool, rows string) string {
	data, err := json.Marshal(struct {
		Schema    string `json:"schema"`
		Name      string `json:"name"`
		Populated bool   `json:"populated"`
		Rows      string `json:"rows"`
	}{schema, name, populated, rows})
	if err != nil {
		panic(err)
	}
	return "-- stuntgres-matview-v1 " + base64.StdEncoding.EncodeToString(data) + "\n"
}

func materializedViewRetainedBag(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	for _, want := range [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}} {
		if !rows.Next() {
			t.Fatal("retained cursor ended early", rows.Err())
		}
		var i int64
		var s *string
		if err := rows.Scan(&i, &s); err != nil {
			t.Fatal(err)
		}
		if i != want[0] || (s == nil) != (want[1] == nil) || s != nil && *s != want[1] {
			t.Fatalf("retained row=%d/%v want=%v", i, s, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cursor tail", rows.Err())
	}
}

func TestReferenceMaterializedViewControls(t *testing.T)     { materializedViewControls(t) }
func TestReferenceMaterializedViewFeatureSmoke(t *testing.T) { materializedViewControls(t) }

func materializedViewControls(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	var dump []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_control AS SELECT i,s FROM mv_base", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_control_no AS SELECT 1/0 AS x WITH NO DATA", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_control_empty AS SELECT i,s FROM mv_base WHERE false", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_control_zero AS SELECT FROM mv_base", 3))
			run(fixture.Exec(`CREATE MATERIALIZED VIEW mv_control_typed AS SELECT 'abcd'::varchar(4) AS v,12.34::numeric(5,2) AS n`, 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_control_refs AS SELECT 'mv_base'::regclass AS r,'integer'::regtype AS t", 1))
			refs := fixture.Select("SELECT r::text AS r,t::text AS t FROM mv_control_refs", []string{"r", "t"}, []string{"TEXT", "TEXT"}, [][]any{{"mv_base", "integer"}})
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			q := "SELECT i,s FROM mv_control"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(fixture.Exec("INSERT INTO mv_base VALUES(3,'b')", 1))
			check := func() {
				run(materializedViewBag("mv_control"))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, materializedViewBag("mv_control"))
				run(fixture.Select("SELECT count(*) AS n FROM mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				run(fixture.MaterializedViewUnpopulated("mv_control_no"))
				fixture.MaterializedViewPopulation(run, "mv_control", true)
				fixture.MaterializedViewPopulation(run, "mv_control_empty", true)
				fixture.MaterializedViewPopulation(run, "mv_control_no", false)
				run(fixture.Select("SELECT * FROM mv_control_empty", []string{"i", "s"}, []string{"INT4", "TEXT"}, nil))
				run(fixture.Select("SELECT * FROM mv_control_zero", []string{}, []string{}, [][]any{{}, {}, {}}))
				run(fixture.Select("SELECT * FROM mv_control_typed", []string{"v", "n"}, []string{"VARCHAR", "NUMERIC"}, [][]any{{"abcd", "12.34"}}))
				run(refs)
				run(fixture.Select("SELECT matviewowner FROM pg_matviews WHERE matviewname='mv_control'", []string{"matviewowner"}, []string{"NAME"}, [][]any{{owner}}))
				run(fixture.Select("SELECT pg_get_viewdef('mv_control'::regclass,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{" SELECT i,\n    s\n   FROM mv_base;"}}))
				run(fixture.Select("SELECT matviewname,ispopulated FROM pg_matviews WHERE matviewname='mv_control_no'", []string{"matviewname", "ispopulated"}, []string{"NAME", "BOOL"}, [][]any{{"mv_control_no", false}}))
			}
			check()
			run(fixture.ImplicitError("INSERT INTO mv_control VALUES(9,'x')", fixture.Diagnostic{Code: "42809", Message: `cannot change materialized view "mv_control"`}))
			if !b.PG {
				var saved bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
					t.Fatal(err)
				}
				dump = bytes.Clone(saved.Bytes())
				retained, err := conn.QueryContext(p.Ctx, "SELECT i,s FROM mv_control ORDER BY i")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.QueryTableCreateAbsent(t, run, "mv_control")
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				materializedViewRetainedBag(t, retained)
				check()
				// A fresh driver remints identities, but must not recompute the stale bag.
				fresh := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, materializedViewBag("mv_control"))
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.MaterializedViewUnpopulated("mv_control_no"))
				fixture.RunStep(t, p.Ctx, fresh.Memory, fresh.MemoryRecord, refs)
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, fresh.Native, &again); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(again.Bytes())); err != nil {
					t.Fatal(err)
				}
				check()
			} else if dump != nil {
				// PG consumes actual emitted schema SQL, ignores native comment snapshots.
				run(fixture.Exec("DROP MATERIALIZED VIEW mv_control,mv_control_no,mv_control_empty,mv_control_zero,mv_control_typed,mv_control_refs", 0))
				run(fixture.Exec("DROP TABLE mv_base CASCADE", 0))
				if _, err := conn.ExecContext(p.Ctx, string(dump)); err != nil {
					t.Fatal("PG rejected native schema replay", err)
				}
				for _, name := range []string{"mv_control", "mv_control_no", "mv_control_empty", "mv_control_zero", "mv_control_typed", "mv_control_refs"} {
					fixture.MaterializedViewPopulation(run, name, false)
					run(fixture.MaterializedViewUnpopulated(name))
				}
				run(fixture.Select("SELECT count(*) AS n FROM mv_base", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			}
			run(fixture.Exec("DROP MATERIALIZED VIEW mv_control,mv_control_no,mv_control_empty,mv_control_zero,mv_control_typed,mv_control_refs", 0))
			run(fixture.Exec("DROP TABLE mv_base", 0))
		})
	}
}

func TestReferenceMaterializedViewRestoreBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_boundary AS SELECT i,s FROM mv_base", 3))
			comment := materializedViewSnapshot("public", "mv_boundary", true, "INSERT INTO public.mv_boundary VALUES(9,'injected');")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(comment+"INSERT INTO mv_boundary VALUES(9,'injected')", fixture.Diagnostic{Code: "42809", Message: `cannot change materialized view "mv_boundary"`})
				s.Prepared = prepared
				run(s)
				run(materializedViewBag("mv_boundary"))
			}
			unchanged := materializedViewBag("mv_boundary")
			unchanged.Query = comment + unchanged.Query
			run(unchanged)
			if !b.PG {
				// Comment-looking text in a literal or block comment is never an import record.
				for _, script := range []string{
					"CREATE MATERIALIZED VIEW mv_literal AS SELECT '-- stuntgres-matview-v1 invalid' AS s WITH NO DATA;",
					"/*\n-- stuntgres-matview-v1 invalid\n*/ CREATE MATERIALIZED VIEW mv_literal AS SELECT 1 AS s WITH NO DATA;",
				} {
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
						t.Fatal(err)
					}
					run(fixture.MaterializedViewUnpopulated("mv_literal"))
				}
			}
		})
	}
}

func TestReferenceMaterializedViewRestoreAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			materializedViewSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_sentinel AS SELECT i,s FROM mv_base", 3))
			run(materializedViewBag("mv_sentinel"))
			if b.PG {
				return
			} // Native control API has no PostgreSQL equivalent.
			retained, err := conn.QueryContext(p.Ctx, "SELECT i,s FROM mv_sentinel ORDER BY i")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			prefix := "CREATE TABLE mv_import_source(i INT); CREATE MATERIALIZED VIEW mv_import AS SELECT i FROM mv_import_source WITH NO DATA;\n"
			valid := materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES(7);")
			bad := []string{
				prefix + "-- stuntgres-matview-v1 not-base64\n",
				prefix + "-- stuntgres-matview-v2 e30=\n",
				prefix + "-- stuntgres-matview-v1 " + base64.StdEncoding.EncodeToString([]byte(`{"schema":"public","name":"mv_import","rows":""}`)) + "\n",
				prefix + "-- stuntgres-matview-v1 " + base64.StdEncoding.EncodeToString([]byte(`{"schema":"public","name":"mv_import","populated":true,"rows":"","extra":1}`)) + "\n",
				prefix + valid + valid,
				prefix + materializedViewSnapshot("public", "missing", true, ""),
				prefix + materializedViewSnapshot("public", "mv_import_source", true, ""),
				prefix + materializedViewSnapshot("public", "mv_import", false, "INSERT INTO public.mv_import VALUES(7);"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES(1,2);"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES('bad');"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import_source VALUES(7);"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import SELECT 7;"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES($1);"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import DEFAULT VALUES;"),
				prefix + materializedViewSnapshot("public", "mv_import", true, "INSERT INTO public.mv_import VALUES(nextval('missing'));"),
				prefix + valid + "CREATE MATERIALIZED VIEW mv_late AS SELECT 1/0 AS i;",
				"CREATE MATERIALIZED VIEW mv_import AS SELECT i FROM missing WITH NO DATA;",
				"SET SESSION AUTHORIZATION missing; " + prefix + valid,
			}
			check := func() {
				run(materializedViewBag("mv_sentinel"))
				run(materializedViewBag("mv_base"))
				fixture.MaterializedViewPopulation(run, "mv_sentinel", true)
				fixture.QueryTableCreateAbsent(t, run, "mv_import")
			}
			for i, script := range bad {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
					t.Fatalf("Restore accepted invalid snapshot case%d", i)
				}
				check()
			}
			failure := errors.New("materialized view import read failed")
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			check()
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(prefix+valid)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			materializedViewRetainedBag(t, retained)
		})
	}
}

// Mutate the actual native record, leaving all genuine schema/type/owner and
// other snapshot records intact. This tests staged replacement, not a parallel
// hand-built serialization format.
func materializedViewReplaceSnapshotRows(t *testing.T, dump []byte, name, rows string) []byte {
	t.Helper()
	lines := strings.Split(string(dump), "\n")
	found := false
	const marker = "-- stuntgres-matview-v1 "
	for i, line := range lines {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Schema    string `json:"schema"`
			Name      string `json:"name"`
			Populated bool   `json:"populated"`
			Rows      string `json:"rows"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Schema != "public" || record.Name != name {
			continue
		}
		if found {
			t.Fatal("duplicate genuine snapshot", name)
		}
		found = true
		record.Rows = rows
		data, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = marker + base64.StdEncoding.EncodeToString(data)
	}
	if !found {
		t.Fatal("genuine snapshot absent", name)
	}
	return []byte(strings.Join(lines, "\n"))
}

func TestReferenceMaterializedViewRecursiveSnapshots(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			for _, query := range []string{
				"CREATE DOMAIN mv_codec_domain_type AS NUMERIC(5,2)",
				"CREATE TYPE mv_codec_enum_type AS ENUM ('ok','later')",
				"CREATE TYPE mv_codec_record AS (n NUMERIC(5,2),t VARCHAR(3),tm TIMESTAMP(3))",
				"CREATE TABLE mv_codec_source(k INT,d mv_codec_domain_type,e mv_codec_enum_type,a NUMERIC(5,2)[],c mv_codec_record)",
			} {
				run(fixture.Exec(query, 0))
			}
			const record = `(12.34,abc,"2024-01-02 12:34:56.123")`
			const array = "[0:1]={12.34,NULL}"
			row := "(1,12.34,'ok','" + array + "','" + record + "')"
			run(fixture.Exec("INSERT INTO mv_codec_source VALUES "+row+","+row+",(2,NULL,NULL,NULL,NULL)", 3))
			for _, column := range []struct{ name, field string }{
				{"mv_codec_domain", "d"}, {"mv_codec_enum", "e"},
				{"mv_codec_array", "a"}, {"mv_codec_composite", "c"},
			} {
				run(fixture.Exec("CREATE MATERIALIZED VIEW "+column.name+" AS SELECT k,"+column.field+" FROM mv_codec_source", 3))
			}
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_codec_bag AS SELECT k,d,e,a,c FROM mv_codec_source", 3))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_codec_no AS SELECT d FROM mv_codec_source WITH NO DATA", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_codec_empty AS SELECT d FROM mv_codec_source WHERE false", 0))
			run(fixture.Exec("INSERT INTO mv_codec_source VALUES(3,56.78,'later','{56.78}','(56.78,xyz,\"2024-01-02 12:34:56.456\")')", 1))
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			check := func(executor fixture.Executor, backend fixture.Backend) {
				execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					domain := fixture.Select("SELECT d AS v FROM mv_codec_domain ORDER BY k", []string{"v"}, []string{"NUMERIC"}, [][]any{{"12.34"}, {"12.34"}, {nil}})
					domain.Prepared = prepared
					execute(domain)
					enum := fixture.Select("SELECT e AS v FROM mv_codec_enum ORDER BY k", []string{"v"}, []string{""}, [][]any{{"ok"}, {"ok"}, {nil}})
					enum.Prepared = prepared
					enum.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, true)
					fixture.EnumReferenceRunStep(t, p.Ctx, executor, backend.Recorder, enum)
					arrayStep := fixture.ArrayReferenceSelect("SELECT a AS v FROM mv_codec_array ORDER BY k", []string{"v"}, []string{"_NUMERIC"}, []string{"_NUMERIC"}, [][]any{{array}, {array}, {nil}}, backend.PG)
					arrayStep.Prepared = prepared
					execute(arrayStep)
					fixture.CompositeReferenceRows(t, p, backend, executor, "SELECT c AS v FROM mv_codec_composite ORDER BY k", [][]any{{record}, {record}, {nil}}, prepared)
				}
				bag := fixture.Select("SELECT k FROM mv_codec_bag", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
				bag.Bag = true
				execute(bag)
				complete := fixture.ArrayReferenceSelect("SELECT k,d,e,c,a FROM mv_codec_bag ORDER BY k", []string{"k", "d", "e", "c", "a"}, []string{"INT4", "NUMERIC", "", "", "_NUMERIC"}, []string{"INT4", "NUMERIC", "", "", "_NUMERIC"}, [][]any{{int64(1), "12.34", "ok", record, array}, {int64(1), "12.34", "ok", record, array}, {int64(2), nil, nil, nil, nil}}, backend.PG)
				fixture.EnumReferenceRunStep(t, p.Ctx, executor, backend.Recorder, complete)
				for _, name := range []string{"mv_codec_bag", "mv_codec_domain", "mv_codec_enum", "mv_codec_array", "mv_codec_composite", "mv_codec_empty"} {
					fixture.MaterializedViewPopulation(execute, name, true)
				}
				fixture.MaterializedViewPopulation(execute, "mv_codec_no", false)
				execute(fixture.MaterializedViewUnpopulated("mv_codec_no"))
				execute(fixture.Select("SELECT d AS v FROM mv_codec_empty", []string{"v"}, []string{"NUMERIC"}, nil))
				execute(fixture.Select("SELECT count(*) AS n FROM mv_codec_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
				for _, column := range []struct{ name, field string }{
					{"mv_codec_domain", "d"}, {"mv_codec_enum", "e"}, {"mv_codec_array", "a"}, {"mv_codec_composite", "c"},
				} {
					execute(fixture.Select("SELECT matviewowner,definition FROM pg_matviews WHERE matviewname='"+column.name+"'", []string{"matviewowner", "definition"}, []string{"NAME", "TEXT"}, [][]any{{owner, " SELECT k,\n    " + column.field + "\n   FROM mv_codec_source;"}}))
				}
			}
			check(conn, b)
			if b.PG {
				return
			} // Native controls have no PostgreSQL equivalent.
			var genuine bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &genuine); err != nil {
				t.Fatal(err)
			}
			retained, err := conn.QueryContext(p.Ctx, "SELECT d,e,a,c FROM mv_codec_bag WHERE k=1")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(genuine.Bytes())); err != nil {
				t.Fatal("genuine recursive MV snapshot", err)
			}
			check(conn, b)
			// A fresh Driver remints all user-defined descriptors and their fields.
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(genuine.Bytes())); err != nil {
				t.Fatal("fresh recursive snapshot", err)
			}
			check(fresh.Memory, fixture.Backend{Recorder: fresh.MemoryRecord})
			var before bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []struct{ name, expression string }{
				{"mv_codec_domain", "12.345"},
				{"mv_codec_domain", "CAST(CAST(12.345 AS NUMERIC(5,2)) AS mv_codec_domain_type)"},
				{"mv_codec_array", "CAST('[0:1]={12.345,NULL}' AS NUMERIC(5,2)[])"},
				{"mv_codec_array", "ARRAY[CAST(12.345 AS NUMERIC(5,2)),NULL]"},
				{"mv_codec_composite", `CAST('(12.345,abc,"2024-01-02 12:34:56.123")' AS mv_codec_record)`},
				{"mv_codec_composite", `CAST('(12.34,abc,"2024-01-02 12:34:56.123789")' AS mv_codec_record)`},
			} {
				payload := materializedViewReplaceSnapshotRows(t, genuine.Bytes(), bad.name, "INSERT INTO public."+bad.name+" VALUES(1,"+bad.expression+");\n")
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(payload)); err == nil {
					t.Fatal("accepted recursive datum loss", bad.name, bad.expression)
				}
				check(conn, b)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("recursive rejection changed entire sentinel catalog/data/population/definition")
				}
			}
			for range 2 {
				if !retained.Next() {
					t.Fatal("retained recursive row missing", retained.Err())
				}
				var d, e, a, c string
				if err := retained.Scan(&d, &e, &a, &c); err != nil {
					t.Fatal(err)
				}
				if d != "12.34" || e != "ok" || a != array || c != record {
					t.Fatal("retained recursive row changed", d, e, a, c)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained recursive cursor tail", retained.Err())
			}
		})
	}
}

func TestReferenceMaterializedViewTemporalSnapshots(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			run(fixture.Exec("CREATE TABLE mv_codec_temporal_source(k INT,d DATE,s TIMESTAMP(3),z TIMESTAMPTZ(3),q TIMETZ(3))", 0))
			const first = "(1,'infinity','infinity','-infinity',timezone(INTERVAL '2147483647 seconds',TIMETZ '12:00+00'))"
			const second = "(2,'-infinity','-infinity','infinity',timezone(INTERVAL '2147483648 seconds',TIMETZ '12:00+00'))"
			run(fixture.Exec("INSERT INTO mv_codec_temporal_source VALUES "+first+","+first+","+second+",(3,NULL,NULL,NULL,NULL)", 4))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_codec_temporal AS SELECT k,d,s,z,q FROM mv_codec_temporal_source", 4))
			run(fixture.Exec("CREATE MATERIALIZED VIEW mv_codec_temporal_no AS SELECT q FROM mv_codec_temporal_source WITH NO DATA", 0))
			run(fixture.Exec("INSERT INTO mv_codec_temporal_source VALUES(4,'2000-01-01','epoch','epoch',NULL)", 1))
			query := "SELECT k,d::TEXT AS d,s::TEXT AS s,z::TEXT AS z,q::TEXT AS q FROM mv_codec_temporal ORDER BY k"
			want := [][]any{
				{int64(1), "infinity", "infinity", "-infinity", "15:14:07+596523:14:07"},
				{int64(1), "infinity", "infinity", "-infinity", "15:14:07+596523:14:07"},
				{int64(2), "-infinity", "-infinity", "infinity", "08:45:52+4294370773:4294967282:4294967288"},
				{int64(3), nil, nil, nil, nil},
			}
			var owner string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			check := func(executor fixture.Executor, backend fixture.Backend) {
				execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, backend.Recorder, s) }
				s := fixture.Select(query, []string{"k", "d", "s", "z", "q"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, want)
				execute(s)
				execute(fixture.Prepared(s))
				// Raw declared metadata is checked separately from presentation so
				// pq's invalid-Location TIMETZ endpoint cannot be mistaken for time.Time.
				execute(fixture.Select("SELECT d,s,z,q FROM mv_codec_temporal WHERE false", []string{"d", "s", "z", "q"}, []string{"DATE", "TIMESTAMP", "TIMESTAMPTZ", "TIMETZ"}, nil))
				bag := fixture.Select("SELECT k FROM mv_codec_temporal", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(3)}})
				bag.Bag = true
				execute(bag)
				fixture.MaterializedViewPopulation(execute, "mv_codec_temporal", true)
				fixture.MaterializedViewPopulation(execute, "mv_codec_temporal_no", false)
				execute(fixture.MaterializedViewUnpopulated("mv_codec_temporal_no"))
				execute(fixture.Select("SELECT count(*) AS n FROM mv_codec_temporal_source", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
				execute(fixture.Select("SELECT matviewowner,definition FROM pg_matviews WHERE matviewname='mv_codec_temporal'", []string{"matviewowner", "definition"}, []string{"NAME", "TEXT"}, [][]any{{owner, " SELECT k,\n    d,\n    s,\n    z,\n    q\n   FROM mv_codec_temporal_source;"}}))
			}
			check(conn, b)
			if b.PG {
				return
			}
			var genuine bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &genuine); err != nil {
				t.Fatal(err)
			}
			retained, err := conn.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(genuine.Bytes())); err != nil {
				t.Fatal("genuine infinity/TIMETZ endpoint MV snapshot", err)
			}
			check(conn, b)
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(p.Ctx, fresh.Native, bytes.NewReader(genuine.Bytes())); err != nil {
				t.Fatal("fresh temporal snapshot", err)
			}
			check(fresh.Memory, fixture.Backend{Recorder: fresh.MemoryRecord})
			for _, row := range want {
				if !retained.Next() {
					t.Fatal("retained temporal row missing", retained.Err())
				}
				var k int64
				var d, s, z, q *string
				if err := retained.Scan(&k, &d, &s, &z, &q); err != nil {
					t.Fatal(err)
				}
				if k != row[0] {
					t.Fatal("retained temporal key changed", k, row)
				}
				for i, cell := range []*string{d, s, z, q} {
					if (cell == nil) != (row[i+1] == nil) || cell != nil && *cell != row[i+1] {
						t.Fatal("retained temporal datum changed", row)
					}
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained temporal cursor tail", retained.Err())
			}
		})
	}
}
