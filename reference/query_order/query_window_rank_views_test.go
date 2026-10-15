package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW rank_named AS SELECT id,"+queryWindowRankProjection("ordered")+" FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k)", 0))
	// The ORDER key is hidden from the view output. Renaming it must preserve
	// captured expressions, their peer equality and all four stored types.
	queryWindowRun(t, p, b, fixture.Exec("ALTER TABLE t RENAME COLUMN k TO hidden_key", 0))
	s := queryWindowRankResult("SELECT * FROM rank_named ORDER BY id", queryWindowRankRows())
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 5 {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			if c.Name() != s.Columns[i] || c.DatabaseTypeName() != s.Types[i] {
				t.Fatal("view metadata", i, c)
			}
			if i > 0 {
				nullable, known := c.Nullable()
				if nullable != !b.PG || known != !b.PG {
					t.Fatal("stored view nullability", i, nullable, known)
				}
			}
		}
	}
	queryWindowRoutes(t, p, b, s)
	steps := queryWindowReviewReplay(t, p, b, "rank_named", s)
	var definition string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('rank_named'::regclass,true)").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW rank_getdef AS "+definition, 0))
	s.Query = "SELECT * FROM rank_getdef ORDER BY id"
	queryWindowRoutes(t, p, b, s)
	steps = append(steps, s)
	if !b.PG {
		queryWindowValueControls(t, p, steps)
	}
}

func TestReferenceQueryWindowRankViews(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		queryWindowRankViews(t, p, b)
	})
}

func TestReferenceQueryWindowRankFeatureSmoke(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		t.Run("ties-gaps-ratios-named", func(t *testing.T) { queryWindowRankBasic(t, p, b) })
		t.Run("singleton-empty", func(t *testing.T) { queryWindowRankSingleton(t, p, b) })
		t.Run("metadata", func(t *testing.T) { queryWindowRankMetadata(t, p, b) })
		t.Run("frame-independence", func(t *testing.T) { queryWindowRankExcluded(t, p, b) })
		t.Run("full-diagnostics", func(t *testing.T) { queryWindowRankBasicDiagnostics(t, p, b) })
		t.Run("export-replay-pristine-restore", func(t *testing.T) { queryWindowRankViews(t, p, b) })
	})
}
