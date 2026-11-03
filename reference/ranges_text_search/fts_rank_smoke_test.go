package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		s := fixture.Select("SELECT ts_rank('cat:1'::tsvector,'cat'::tsquery) AS frequency, ts_rank_cd('cat:1 rat:3'::tsvector,'cat & rat'::tsquery) AS density, ts_rank_cd(strip('cat:1 rat:3'::tsvector),'cat & rat'::tsquery) AS stripped", []string{"frequency", "density", "stripped"}, []string{"FLOAT4", "FLOAT4", "FLOAT4"}, [][]any{{float64(float32(.0607927106320858)), float64(float32(.05)), float64(0)}})
		// The public pq value is parsed from the release's shortest decimal;
		// native returns an exact binary32 widening. Keep the complete
		// three-column smoke and independently assert recovered bits on both.
		if b.PG {
			s.Rows = [][]any{{float64(.06079271), float64(.05), float64(0)}}
		}
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(columns) != 3 {
				t.Fatal("smoke column count", len(columns))
			}
			for i, label := range []string{"frequency", "density", "stripped"} {
				ftsRankColumn(t, columns[i], b.PG, label, "FLOAT4", false)
			}
		}
		s.CheckRaw = func(t *testing.T, column, row int, raw any) bool {
			value, ok := raw.(float64)
			if !ok {
				t.Fatalf("smoke REAL column %d = %T(%v)", column, raw, raw)
			}
			if !b.PG && value != float64(float32(value)) {
				t.Fatalf("smoke REAL is not binary32: %.18g", value)
			}
			expected := [3]uint32{0x3d7901c7, 0x3d4ccccd, 0}
			if bits := math.Float32bits(float32(value)); bits != expected[column] {
				t.Fatalf("smoke REAL column %d bits=%08x want=%08x", column, bits, expected[column])
			}
			return true
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		ftsRankRoutes(t, p, b, c, "SELECT ts_rank(ARRAY[0.1,0.2,0.4,1]::real[],'cat:1'::tsvector,'cat'::tsquery,32) AS weighted", "weighted", "FLOAT4", ftsRankExact(.057308755815029144))
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,'The fat cat ate the rat.','cat'::tsquery) AS excerpt", "excerpt", "TEXT", "The fat <b>cat</b> ate the rat.")
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('english'::regconfig,'Rats & cats','rat'::tsquery,'HighlightAll=true,StartSel=<<,StopSel=>>') AS marked", "marked", "TEXT", "<<Rats>> & cats")
		defaultSQL := "SELECT ts_headline($1::text,$2::tsquery)"
		defaultStatement, err := c.PrepareContext(p.Ctx, defaultSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer defaultStatement.Close()
		for _, setting := range []string{"english", "simple", "english"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET default_text_search_config='"+setting+"'", 0))
			want := "<b>Rats</b>"
			if setting == "simple" {
				want = "Rats"
			}
			rows, err := defaultStatement.QueryContext(p.Ctx, "Rats", "rat")
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", want, false)
		}
		rankSQL := "SELECT ts_rank($1::real[],$2::tsvector,$3::tsquery,$4::integer) AS score"
		rankStatement, err := c.PrepareContext(p.Ctx, rankSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer rankStatement.Close()
		for _, tc := range []struct {
			args []any
			want any
		}{
			{[]any{"{0.1,0.2,0.4,1}", "cat:1", "cat", 0}, ftsRankExact(.0607927106320858)},
			{[]any{"{0.1,0.2,0.4,1}", "cat:1", "cat", 32}, ftsRankExact(.057308755815029144)},
			{[]any{"{1}", nil, "cat", 0}, nil},
		} {
			rows, err := rankStatement.QueryContext(p.Ctx, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			ftsRankRead(t, p, b, rows, "score", "FLOAT4", tc.want, false)
		}
		for range 2 {
			s := fixture.ErrorDiagnostic(rankSQL, fixture.ControlsReferenceDiagnostic("2202E", "array of weight is too short"), "{1}", "cat:1", "cat", 0)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: rankStatement, SQL: rankSQL}, b.Recorder, s)
		}
		rows, err := rankStatement.QueryContext(p.Ctx, "{0.1,0.2,0.4,1}", "cat:1", "cat", 0)
		if err != nil {
			t.Fatal(err)
		}
		ftsRankRead(t, p, b, rows, "score", "FLOAT4", ftsRankExact(.0607927106320858), false)
		headlineSQL := "SELECT ts_headline('english'::regconfig,$1::text,$2::tsquery,$3::text) AS excerpt"
		headlineStatement, err := c.PrepareContext(p.Ctx, headlineSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer headlineStatement.Close()
		rows, err = headlineStatement.QueryContext(p.Ctx, "Cat", "cat", "StartSel=[,StopSel=]")
		if err != nil {
			t.Fatal(err)
		}
		ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "[Cat]", false)
		for range 2 {
			s := fixture.ErrorDiagnostic(headlineSQL, fixture.ControlsReferenceDiagnostic("42601", `invalid parameter list format: "StartSel="`), "Cat", "cat", "StartSel=")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: headlineStatement, SQL: headlineSQL}, b.Recorder, s)
		}
		rows, err = headlineStatement.QueryContext(p.Ctx, "Cat", "cat", "HighlightAll=true")
		if err != nil {
			t.Fatal(err)
		}
		ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Cat</b>", false)
		for _, expression := range []struct{ sql, typ string }{
			{"ts_rank(NULL::tsvector,'cat'::tsquery)", "FLOAT4"},
			{"ts_rank_cd(NULL::tsvector,'cat'::tsquery)", "FLOAT4"},
			{"ts_headline(NULL::text,'cat'::tsquery)", "TEXT"},
		} {
			ftsRankRoutes(t, p, b, c, "SELECT "+expression.sql+" AS absent", "absent", expression.typ, nil)
		}
		publication := fixture.Select("SELECT oid::bigint AS oid FROM pg_catalog.pg_proc WHERE oid IN (3703,3704,3705,3706,3707,3708,3709,3710,3743,3744,3754,3755) ORDER BY oid", []string{"oid"}, []string{"INT8"}, [][]any{{int64(3703)}, {int64(3704)}, {int64(3705)}, {int64(3706)}, {int64(3707)}, {int64(3708)}, {int64(3709)}, {int64(3710)}, {int64(3743)}, {int64(3744)}, {int64(3754)}, {int64(3755)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, publication)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(publication))
	})
}
