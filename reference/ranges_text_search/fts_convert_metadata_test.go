package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSConvertSignatures(t *testing.T) {
	p := fixture.NewScenario(t)
	rows := [][]any{
		{int64(3745), "to_tsvector", int64(3614), true, "i", "s", "3734 25", nil, "to_tsvector_byid", float64(100), false},
		{int64(3746), "to_tsquery", int64(3615), true, "i", "s", "3734 25", nil, "to_tsquery_byid", float64(100), false},
		{int64(3747), "plainto_tsquery", int64(3615), true, "i", "s", "3734 25", nil, "plainto_tsquery_byid", float64(100), false},
		{int64(3749), "to_tsvector", int64(3614), true, "s", "s", "25", nil, "to_tsvector", float64(100), false},
		{int64(3750), "to_tsquery", int64(3615), true, "s", "s", "25", nil, "to_tsquery", float64(100), false},
		{int64(3751), "plainto_tsquery", int64(3615), true, "s", "s", "25", nil, "plainto_tsquery", float64(100), false},
		{int64(5001), "phraseto_tsquery", int64(3615), true, "s", "s", "25", nil, "phraseto_tsquery", float64(100), false},
		{int64(5006), "phraseto_tsquery", int64(3615), true, "i", "s", "3734 25", nil, "phraseto_tsquery_byid", float64(100), false},
		{int64(5007), "websearch_to_tsquery", int64(3615), true, "i", "s", "3734 25", nil, "websearch_to_tsquery_byid", float64(100), false},
		{int64(5009), "websearch_to_tsquery", int64(3615), true, "s", "s", "25", nil, "websearch_to_tsquery", float64(100), false},
	}
	s := fixture.Select("SELECT oid::bigint,proname,prorettype::bigint,proisstrict,provolatile,proparallel,proargtypes::text,proargnames::text,prosrc,procost::double precision,proretset FROM pg_proc WHERE oid IN (3745,3746,3747,3749,3750,3751,5001,5006,5007,5009) ORDER BY oid",
		[]string{"oid", "proname", "prorettype", "proisstrict", "provolatile", "proparallel", "proargtypes", "proargnames", "prosrc", "procost", "proretset"},
		[]string{"INT8", "NAME", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "TEXT", "FLOAT8", "BOOL"}, rows)
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}

func ftsConvertMetadata(t *testing.T, rows *sql.Rows, pg bool, name, typ string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].Name() != name || columns[0].DatabaseTypeName() != typ {
		t.Fatal("constructor metadata", columns)
	}
	column := columns[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if column.ScanType() != scan {
		t.Fatal("ScanType", column.ScanType(), scan)
	}
	if n, known := column.Nullable(); n || known {
		t.Fatal("Nullable", n, known)
	}
	if n, known := column.Length(); n != 0 || known {
		t.Fatal("Length", n, known)
	}
	if p, s, known := column.DecimalSize(); p != 0 || s != 0 || known {
		t.Fatal("DecimalSize", p, s, known)
	}
}
func TestReferenceFTSConvertMetadata(t *testing.T) {
	for _, name := range ftsConvertNames {
		t.Run(name, func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				for _, explicit := range []bool{false, true} {
					prefix := ""
					if explicit {
						prefix = "'english'::regconfig,"
					}
					for _, alias := range []string{"", " AS normalized"} {
						label := name
						if alias != "" {
							label = "normalized"
						}
						for _, tc := range []struct {
							argument, suffix string
							want             any
							zero             bool
						}{
							{"'Cat'::text", "", "'cat'", false}, {"''::text", "", "", false}, {"NULL::text", "", nil, false}, {"'Cat'::text", " WHERE false", nil, true},
						} {
							want := tc.want
							if want == "'cat'" && name == "to_tsvector" {
								want = "'cat':1"
							}
							var expected [][]any
							if !tc.zero {
								expected = [][]any{{want}}
							}
							query := "SELECT " + name + "(" + prefix + tc.argument + ")" + alias + tc.suffix
							for _, prepared := range []bool{false, true} {
								s := fixture.Select(query, []string{label}, []string{ftsConvertType(name)}, expected)
								s.Prepared = prepared
								s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, label, ftsConvertType(name)) }
								fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							}
						}
					}
					query := "SELECT " + name + "(" + prefix + "$1::text)"
					stmt, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					for _, input := range []any{"Cat", []byte("Rats"), "", nil} {
						rows, err := stmt.QueryContext(p.Ctx, input)
						if err != nil {
							stmt.Close()
							t.Fatal(err)
						}
						ftsConvertMetadata(t, rows, b.PG, name, ftsConvertType(name))
						if !rows.Next() {
							rows.Close()
							stmt.Close()
							t.Fatal("missing typed result")
						}
						var raw any
						if err := rows.Scan(&raw); err != nil {
							rows.Close()
							stmt.Close()
							t.Fatal(err)
						}
						var expected any
						if input != nil {
							text := ""
							switch v := input.(type) {
							case string:
								text = v
							case []byte:
								text = string(v)
							}
							switch text {
							case "Cat":
								expected = "'cat'"
							case "Rats":
								expected = "'rat'"
							default:
								expected = ""
							}
							if name == "to_tsvector" && text != "" {
								expected = expected.(string) + ":1"
							}
						}
						fixture.AssertRawValue(t, b.PG, ftsConvertType(name), raw)
						if !reflect.DeepEqual(fixture.Normalize(raw), expected) {
							rows.Close()
							stmt.Close()
							t.Fatal("raw conversion", raw, expected)
						}
						if rows.Next() || rows.Err() != nil {
							rows.Close()
							stmt.Close()
							t.Fatal("trailing rows", rows.Err())
						}
						rows.Close()
					}
					stmt.Close()
				}
			})
		})
	}
}
