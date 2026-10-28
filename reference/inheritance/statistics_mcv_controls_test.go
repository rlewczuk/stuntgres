package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func mcvStaleControl(t *testing.T, s ndistinctScenario) {
	t.Helper()
	// Five-row facts intentionally disagree with the four live rows in the dump.
	s.run(fixture.Exec("DELETE FROM mcv_t WHERE a=2", 2))
	s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
	mcvAssert(t, s, "mcv_s", mcvFive...)
	var saved bytes.Buffer
	if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
		t.Fatal(err)
	}
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name() + "-mcv-destination")
	if err != nil {
		t.Fatal(err)
	}
	target := sql.OpenDB(connector)
	defer target.Close()
	if _, err := target.ExecContext(s.pair.Ctx, "CREATE TABLE collision(a INT,b INT); CREATE STATISTICS collision_s ON a,b FROM collision"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(s.pair.Ctx, target, bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	conn, err := target.Conn(s.pair.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	dest := s
	dest.conn = conn
	mcvAssert(t, dest, "mcv_s", mcvFive...)
	var count int
	if err := conn.QueryRowContext(s.pair.Ctx, "SELECT count(*) FROM mcv_t").Scan(&count); err != nil || count != 4 {
		t.Fatal("restored live population", count, err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(s.pair.Ctx, target, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved.Bytes(), again.Bytes()) {
		t.Fatal("stale MCV redump changed")
	}
}
func TestReferenceStatisticsMCVControls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		mcvRefresh(t, s)
		for _, q := range []string{"CREATE STATISTICS mcv_default ON a,b FROM mcv_t", "CREATE STATISTICS mcv_expr(mcv) ON (a+0),(b+0) FROM mcv_t", "CREATE TABLE mcv_empty(a INT,b INT)", "CREATE STATISTICS mcv_absent(mcv) ON a,b FROM mcv_empty", "ANALYZE mcv_t", "ALTER STATISTICS mcv_s SET STATISTICS 0"} {
			s.run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"CREATE TABLE mcv_control_p(a INT,b INT)", "CREATE TABLE mcv_control_c() INHERITS(mcv_control_p)", "CREATE STATISTICS mcv_control_ps(mcv) ON a,b FROM mcv_control_p"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO mcv_control_p VALUES(1,1),(1,1)", 2))
		s.run(fixture.Exec("INSERT INTO mcv_control_c VALUES(2,2),(2,2),(2,2)", 3))
		s.run(fixture.Exec("ANALYZE mcv_control_p", 0))
		s.run(fixture.Exec("DELETE FROM mcv_control_c", 3))
		if !s.pg {
			mcvStaleControl(t, s)
		} else {
			s.run(fixture.Exec("DELETE FROM mcv_t WHERE a=2", 2))
			s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		}
		mcvAssert(t, s, "mcv_s", mcvFive...)
		mcvAssert(t, s, "mcv_default", mcvFive...)
		mcvAssert(t, s, "mcv_expr", mcvFive...)
		s.run(fixture.Exec("ALTER STATISTICS mcv_default SET STATISTICS 1", 0))
		for _, q := range []string{"CREATE TEMP TABLE mcv_temp(a INT,b INT)", "CREATE STATISTICS pg_temp.mcv_temp_s(mcv) ON a,b FROM mcv_temp"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO mcv_temp VALUES(1,1),(1,1),(2,2),(NULL,NULL)", 4))
		s.run(fixture.Exec("ANALYZE mcv_temp", 0))
		s.run(fixture.Exec("DELETE FROM mcv_temp WHERE a=2", 1))
		mcvAssert(t, s, "mcv_temp_s", mcvFour...)
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			s.run(fixture.Exec("CREATE TABLE mcv_destination_sentinel(v INT)", 0))
			s.run(fixture.Exec("INSERT INTO mcv_destination_sentinel VALUES(77)", 1))
			held, err := s.conn.QueryContext(s.pair.Ctx, "SELECT stxdmcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_s'")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			meta, err := held.ColumnTypes()
			if err != nil || meta[0].DatabaseTypeName() != "PG_MCV_LIST" {
				t.Fatal(meta, err)
			}
			mcvCorruptControls(t, s, saved.String())
			mcvControlIO(t, s)
			canceled, cancel := context.WithCancel(s.pair.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, s.pair.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			mcvAssert(t, s, "mcv_s", mcvFive...)
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			mcvAssert(t, s, "mcv_temp_s", mcvFour...)
			s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='mcv_s'", []string{"stxname"}, []string{"NAME"}, nil))
			if !held.Next() {
				t.Fatal(held.Err())
			}
			var raw string
			if err := held.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			physical, err := hex.DecodeString(strings.TrimPrefix(raw, `\x`))
			if err != nil {
				t.Fatal(err)
			}
			assertIntMCV(t, decodeMCV(t, physical), mcvFive...)
			held.Close()
			if meta[0].DatabaseTypeName() != "PG_MCV_LIST" {
				t.Fatal("held metadata changed")
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			mcvAssert(t, s, "mcv_s", mcvFive...)
			mcvAssert(t, s, "mcv_default", mcvFive...)
			mcvAssert(t, s, "mcv_expr", mcvFive...)
			mcvAssert(t, s, "mcv_temp_s", mcvFour...)
		}
		_, inherited := readMCV(t, s, "mcv_control_ps", true)
		assertIntMCV(t, inherited, mcvExpected{"2,2", .6, .36}, mcvExpected{"1,1", .4, .16})
		mcvAssert(t, s, "mcv_control_ps", mcvExpected{"1,1", 1, 1})
		s.run(fixture.Select("SELECT d.stxdmcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='mcv_absent'", []string{"stxdmcv"}, []string{statisticsDatabaseTypeName(s.pg, "pg_mcv_list")}, nil))
		s.run(fixture.Exec("ANALYZE mcv_temp", 0))
		mcvAssert(t, s, "mcv_temp_s", mcvExpected{"1,1", 2.0 / 3, 4.0 / 9}, mcvExpected{"NULL,NULL", 1.0 / 3, 1.0 / 9})
	})
}
func mcvCorruptControls(t *testing.T, s ndistinctScenario, dump string) {
	t.Helper()
	const prefix = "-- stuntgres-ndistinct-v1 "
	line := ""
	for _, candidate := range strings.Split(dump, "\n") {
		if strings.HasPrefix(candidate, prefix) {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(candidate, prefix))
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				t.Fatal(err)
			}
			if object["mcv"] != nil && object["name"] == "mcv_s" {
				line = candidate
				break
			}
		}
	}
	if line == "" {
		t.Fatal("missing MCV construction state")
	}
	reject := func(stream string) {
		t.Helper()
		if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, strings.NewReader(stream)); err == nil {
			t.Fatal("malformed MCV state accepted")
		}
		mcvAssert(t, s, "mcv_s", mcvFive...)
		mcvAssert(t, s, "mcv_temp_s", mcvFour...)
		mcvOrdered(s, false)
		s.run(fixture.Select("SELECT v FROM mcv_destination_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
	}
	reject(dump + line + "\n")
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["rows"] = float64(0) },
		func(m map[string]any) { m["items"] = nil },
		func(m map[string]any) { m["unknown"] = true },
		func(m map[string]any) {
			items, ok := m["items"].([]any)
			if !ok || len(items) == 0 {
				t.Fatal("missing construction items")
			}
			item := items[0].(map[string]any)
			item["count"] = float64(0)
		},
		func(m map[string]any) {
			item := m["items"].([]any)[0].(map[string]any)
			item["nulls"].([]any)[0] = "false"
		},
		func(m map[string]any) {
			item := m["items"].([]any)[0].(map[string]any)
			item["indexes"].([]any)[0] = float64(9999)
		},
		func(m map[string]any) {
			item := m["items"].([]any)[0].(map[string]any)
			item["marginal_counts"].([]any)[0] = float64(0)
		},
		func(m map[string]any) { dictionary := m["dictionaries"].([]any)[0].([]any); dictionary[0] = nil },
		func(m map[string]any) {
			dictionary := m["dictionaries"].([]any)[0].([]any)
			if len(dictionary) < 2 {
				t.Fatal("need two real dictionary values")
			}
			dictionary[1] = dictionary[0]
		},
		func(m map[string]any) {
			dictionary := m["dictionaries"].([]any)[0].([]any)
			constructor := dictionary[0].(map[string]any)
			constructor["unknown"] = true
		},
	} {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, prefix))
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		m, ok := object["mcv"].(map[string]any)
		if !ok {
			t.Fatal("MCV shape")
		}
		mutate(m)
		encoded, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		bad := prefix + base64.StdEncoding.EncodeToString(encoded)
		reject(strings.Replace(dump, line, bad, 1))
	}
}
