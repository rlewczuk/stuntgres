package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Stored-view outputs are relation columns, not computed SELECT expressions.
func jsonpathNavigateViewMetadataStep(s fixture.Step, pg bool) fixture.Step {
	s = jsonbSetMetadataStep(s, pg) // Retain the raw, rescan and byte-ownership checks.
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal(columns, err)
		}
		column := columns[0]
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if column.Name() != "changed" || column.DatabaseTypeName() != "JSONB" || column.ScanType() != scan {
			t.Fatalf("view metadata=%q/%q/%v want changed/JSONB/%v", column.Name(), column.DatabaseTypeName(), column.ScanType(), scan)
		}
		if length, known := column.Length(); known {
			t.Fatalf("unexpected view length %d", length)
		}
		if precision, scale, known := column.DecimalSize(); known {
			t.Fatalf("unexpected view decimal %d,%d", precision, scale)
		}
		if nullable, known := column.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("view nullable=%v,%v want %v,%v", nullable, known, !pg, !pg)
		}
	}
	return s
}

func TestReferenceJSONPathNavigateFeatureSmoke(t *testing.T) {
	jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{`'{"a":[1,2,3]}','$.a[*]?(@ > $n)','{"n":1}'`, `[2, 3]`},
			{`'[{"x":1},{},{"x":3}]','strict $[*].x',silent => true`, `[1]`},
			{`'"foo"','$ > 0'`, `[null]`},
			{`'["A.B","axb"]','$[*]?(@ like_regex "a.b" flag "iq")'`, `["A.B"]`},
		} {
			jsonpathNavigateBoth(t, p, b, c, jsonbSetMetadataStep(jsonpathNavigateSelect("SELECT jsonb_path_query_array("+tc.args+") AS changed", tc.want), b.PG))
		}
		jsonpathNavigateBoth(t, p, b, c, jsonpathNavigateError(`SELECT jsonb_path_query_array('1','$v',silent=>true)`, "42704", `could not find jsonpath variable "v"`))
		q := `SELECT jsonb_path_query_array($1,$2,$3,$4) AS changed`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateError(q, "2203A", `JSON object does not contain key "x"`, `{}`, `strict $.x`, `{}`, false))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, jsonpathNavigateSelect(q, `[2, 3]`, `[1,2,3]`, `$[*]?(@ > $n)`, `{"n":1}`, false))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpn_smoke(doc jsonb)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpn_smoke VALUES('{"a":[1,2,3]}')`, 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE VIEW jpn_smoke_view AS SELECT jsonb_path_query_array(vars=>'{"n":1}',path=>'$.a[*]?(@ > $n)',target=>doc) AS changed FROM jpn_smoke`, 0))
		check := jsonpathNavigateViewMetadataStep(jsonpathNavigateSelect(`SELECT changed FROM jpn_smoke_view`, `[2, 3]`), b.PG)
		jsonpathNavigateBoth(t, p, b, c, check)
		jsonpathNavigateReplay(t, p, b, c, check)
	})
}
