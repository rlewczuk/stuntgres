package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ftsArrayConfigRawStep(b fixture.Backend, s fixture.Step) fixture.Step {
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if s.Types[col] == "REGCONFIG" {
			fixture.RegtypeRaw(t, b.PG, value)
		} else {
			fixture.AssertRawValue(t, b.PG, s.Types[col], value)
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		ftsConvertMetadata(t, rows, b.PG, s.Columns[0], s.Types[0])
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
		var text sql.NullString
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		want := s.Rows[index][0]
		if text.Valid != (want != nil) || want != nil && text.String != want {
			t.Fatalf("NullString=%#v want %#v", text, want)
		}
		var bytes []byte
		if err := rows.Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		if want == nil && bytes != nil || want != nil && string(bytes) != want {
			t.Fatalf("bytes=%q want %#v", bytes, want)
		}
	}
	return s
}

func TestReferenceFTSArrayConfigMetadata(t *testing.T) {
	ftsArrayConfigScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='simple'", 0))
		for _, tc := range []struct {
			call, label, typ string
			want             any
		}{
			{"array_to_tsvector(ARRAY['b','a','a'])", "array_to_tsvector", "TSVECTOR", "'a' 'b'"},
			{"array_to_tsvector('{}'::text[])", "array_to_tsvector", "TSVECTOR", ""},
			{"array_to_tsvector(NULL::text[])", "array_to_tsvector", "TSVECTOR", nil},
			{"get_current_ts_config()", "get_current_ts_config", "REGCONFIG", "simple"},
		} {
			for _, alias := range []string{"", " AS result"} {
				label := tc.label
				if alias != "" {
					label = "result"
				}
				for _, suffix := range []string{"", " WHERE false"} {
					var rows [][]any
					if suffix == "" {
						rows = [][]any{{tc.want}}
					}
					s := fixture.Select("SELECT "+tc.call+alias+suffix, []string{label}, []string{tc.typ}, rows)
					ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigRawStep(b, s))
				}
			}
		}
		q := "SELECT oid::bigint,proname,pronamespace::bigint,prorettype::bigint,proisstrict,provolatile,proparallel,proargtypes::text,proargnames::text,prosrc,procost::double precision,proretset,prolang=12 AS internal FROM pg_catalog.pg_proc WHERE oid IN (3327,3759) ORDER BY oid"
		ftsArrayConfigBoth(t, p, b, c, fixture.Select(q,
			[]string{"oid", "proname", "pronamespace", "prorettype", "proisstrict", "provolatile", "proparallel", "proargtypes", "proargnames", "prosrc", "procost", "proretset", "internal"},
			[]string{"INT8", "NAME", "INT8", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "TEXT", "FLOAT8", "BOOL", "BOOL"},
			[][]any{
				{int64(3327), "array_to_tsvector", int64(11), int64(3614), true, "i", "s", "1009", nil, "array_to_tsvector", float64(1), false, true},
				{int64(3759), "get_current_ts_config", int64(11), int64(3734), true, "s", "s", "", nil, "get_current_ts_config", float64(1), false, true},
			}))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.array_cfg_metadata (COPY=pg_catalog.simple)", 0))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='public.array_cfg_metadata'", 0))
		s := ftsArrayConfigRawStep(b, ftsArrayConfigValue("SELECT get_current_ts_config() AS result", "REGCONFIG", "array_cfg_metadata"))
		rows, err := c.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		s.CheckMetadata(t, rows)
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		// pq streams; drain before DDL on its physical owner. Native eager rows
		// remain a detached value snapshot while later calls change catalog state.
		if b.PG {
			fixture.RangeConsumeHeld(t, rows, s)
		}
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("SET default_text_search_config='english'", 0))
		ftsArrayConfigRun(t, p, b, c, fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.array_cfg_metadata", 0))
		if !b.PG {
			fixture.RangeConsumeHeld(t, rows, s)
		}
		if len(metadata) != 1 || metadata[0].Name() != "result" || metadata[0].DatabaseTypeName() != "REGCONFIG" {
			t.Fatal("retained metadata changed", metadata)
		}
		ftsArrayConfigBoth(t, p, b, c, ftsArrayConfigRawStep(b, ftsArrayConfigValue(s.Query, "REGCONFIG", "english")))
	})
}
