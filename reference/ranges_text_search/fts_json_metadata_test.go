package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSJSONSignatures(t *testing.T) {
	p := fixture.NewScenario(t)
	rows := [][]any{
		{int64(4209), "to_tsvector", int64(3614), true, "s", "s", "3802", nil, "jsonb_string_to_tsvector", float64(100), false, int64(0), int64(0)},
		{int64(4210), "to_tsvector", int64(3614), true, "s", "s", "114", nil, "json_string_to_tsvector", float64(100), false, int64(0), int64(0)},
		{int64(4211), "to_tsvector", int64(3614), true, "i", "s", "3734 3802", nil, "jsonb_string_to_tsvector_byid", float64(100), false, int64(0), int64(0)},
		{int64(4212), "to_tsvector", int64(3614), true, "i", "s", "3734 114", nil, "json_string_to_tsvector_byid", float64(100), false, int64(0), int64(0)},
		{int64(4213), "jsonb_to_tsvector", int64(3614), true, "s", "s", "3802 3802", nil, "jsonb_to_tsvector", float64(100), false, int64(0), int64(0)},
		{int64(4214), "jsonb_to_tsvector", int64(3614), true, "i", "s", "3734 3802 3802", nil, "jsonb_to_tsvector_byid", float64(100), false, int64(0), int64(0)},
		{int64(4215), "json_to_tsvector", int64(3614), true, "s", "s", "114 3802", nil, "json_to_tsvector", float64(100), false, int64(0), int64(0)},
		{int64(4216), "json_to_tsvector", int64(3614), true, "i", "s", "3734 114 3802", nil, "json_to_tsvector_byid", float64(100), false, int64(0), int64(0)},
	}
	s := fixture.Select("SELECT oid::bigint,proname,prorettype::bigint,proisstrict,provolatile,proparallel,proargtypes::text,proargnames::text,prosrc,procost::double precision,proretset,pronargdefaults::bigint,provariadic::bigint FROM pg_proc WHERE oid IN (4209,4210,4211,4212,4213,4214,4215,4216) ORDER BY oid",
		[]string{"oid", "proname", "prorettype", "proisstrict", "provolatile", "proparallel", "proargtypes", "proargnames", "prosrc", "procost", "proretset", "pronargdefaults", "provariadic"},
		[]string{"INT8", "NAME", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "TEXT", "FLOAT8", "BOOL", "INT8", "INT8"}, rows)
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	// Unknown literals must retain the preferred TEXT overload.
	s = ftsJSONValue("SELECT to_tsvector('simple','Cat') AS normalized", "'cat':1")
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}

func TestReferenceFTSJSONMetadata(t *testing.T) {
	for _, form := range ftsJSONForms() {
		t.Run(form.name+"_"+form.typ+"_"+map[bool]string{true: "explicit", false: "default"}[form.explicit], func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				for _, alias := range []bool{false, true} {
					label := form.name
					if alias {
						label = "normalized"
					}
					for _, tc := range []struct {
						document any
						want     any
						zero     bool
					}{
						{`"Cat"`, "'cat':1", false}, {`[]`, "", false}, {nil, nil, false}, {`"Cat"`, nil, true},
					} {
						query, _ := form.query(true, "english", tc.document, `"string"`)
						if !alias {
							query = strings.TrimSuffix(query, " AS normalized")
						}
						if tc.zero {
							query += " WHERE false"
						}
						var expected [][]any
						if !tc.zero {
							expected = [][]any{{tc.want}}
						}
						for _, prepared := range []bool{false, true} {
							s := fixture.Select(query, []string{label}, []string{"TSVECTOR"}, expected)
							s.Prepared = prepared
							s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, label, "TSVECTOR") }
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						}
					}
				}
				query, _ := form.query(false, "english", `"Cat"`, `"string"`)
				query = strings.TrimSuffix(query, " AS normalized")
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, tc := range []struct{ document, want any }{{`"Cat"`, "'cat':1"}, {[]byte(`"Rats"`), "'rat':1"}, {`[]`, ""}, {nil, nil}} {
					_, args := form.query(false, "english", tc.document, `"string"`)
					rows, err := stmt.QueryContext(p.Ctx, args...)
					if err != nil {
						t.Fatal(err)
					}
					ftsConvertMetadata(t, rows, b.PG, form.name, "TSVECTOR")
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing typed result", rows.Err())
					}
					var raw any
					if err := rows.Scan(&raw); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					fixture.AssertRawValue(t, b.PG, "TSVECTOR", raw)
					if !reflect.DeepEqual(fixture.Normalize(raw), tc.want) {
						rows.Close()
						t.Fatal("raw JSON vector", raw, tc.want)
					}
					if rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal("trailing result", rows.Err())
					}
					rows.Close()
				}
			})
		})
	}
}
