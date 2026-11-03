package textsearch_test

import (
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestFTSHeadlinePreparedOwnership(t *testing.T) {
	t.Run("typed_accepted_generation", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		ast, d := enginetest.PreparedDescription(t, e, s, "SELECT ts_headline('english'::regconfig,'Rats'::text,'rat'::tsquery)")
		if d.Folds == nil || d.Folds.Current().Generation != 0 || len(d.Folds.Current().Entries) != 0 {
			t.Fatal("Describe evaluated headline")
		}
		enginetest.PreparedFoldExecute(t, e, s, ast, &d)
		entry := enginetest.PreparedFoldEntry(t, d, sqltype.Text, "<b>Rats</b>")
		before := enginetest.SnapshotFoldDescription(d)
		enginetest.SessionSQL(t, s, "SET default_text_search_config='simple'")
		enginetest.PreparedFoldExecute(t, e, s, ast, &d)
		after := d.Folds.Current()
		if after.Generation != before.Accepted.Generation || &after.Entries[0] != &before.Accepted.Entries[0] || !enginetest.SamePublishedFold(entry, enginetest.PreparedFoldEntry(t, d, sqltype.Text, "<b>Rats</b>")) {
			t.Fatal("headline accepted artifact replaced")
		}
		enginetest.ConvertRejectPublication(t, e, s, ast, d, before.Accepted)
		ftsRankHeadlineRejectStale(t, e, s, ast, d, before.Accepted)
	})
	t.Run("collated_definition_metadata", func(t *testing.T) {
		for _, tc := range []struct {
			query string
			id    uint32
		}{
			{`SELECT ts_headline('english'::regconfig,'Rats'::text COLLATE "C",'rat'::tsquery)`, 950},
			{`SELECT ts_headline('english'::regconfig,'Rats'::text,'rat'::tsquery,''::text COLLATE "POSIX")`, 951},
		} {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			ast, d := enginetest.PreparedDescription(t, e, s, tc.query)
			text, ok := e.CatalogRead().LookupType(sqltype.Text)
			if !ok || len(d.Results) != 1 || d.Results[0].Usage.Type != text || d.Results[0].Usage.Typmod != -1 || d.Results[0].Usage.Collation.ID != tc.id {
				t.Fatal("collated definition lost result generation/usage", d.Results)
			}
			if d.Folds == nil || !d.Folds.Live() || d.Folds.Current().Generation != 0 || len(d.Folds.Current().Entries) != 0 {
				t.Fatal("Describe evaluated collated headline")
			}
			owner, usage := d.Folds, d.Results[0].Usage
			// Ordinary preflight retains COLLATE children, so these calls do
			// not satisfy literal-only fold admission. Preserve that policy
			// while checking real execution and the live, empty fold owner.
			for _, setting := range []string{"english", "simple", "english"} {
				enginetest.SessionSQL(t, s, "SET default_text_search_config='"+setting+"'")
				enginetest.ConvertOwnedExecute(t, e, s, ast, &d, "", "<b>Rats</b>")
				accepted := d.Folds.Current()
				if d.Folds != owner || !owner.Live() || accepted.Generation != 0 || len(accepted.Entries) != 0 || len(accepted.Dependencies) != 0 || d.Results[0].Usage != usage {
					t.Fatal("collated execution replaced metadata or invented a fold", accepted)
				}
			}
		}
	})
	t.Run("default_remains_live", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		ast, d := enginetest.PreparedDescription(t, e, s, `SELECT ts_headline($1::text COLLATE "C",'rat'::tsquery)`)
		if len(d.Results) != 1 || d.Results[0].Usage.Collation.ID != 950 {
			t.Fatal("stable headline lost collation")
		}
		for _, tc := range []struct{ setting, want string }{{"english", "<b>Rats</b>"}, {"simple", "Rats"}, {"english", "<b>Rats</b>"}} {
			enginetest.SessionSQL(t, s, "SET default_text_search_config='"+tc.setting+"'")
			enginetest.ConvertOwnedExecute(t, e, s, ast, &d, "Rats", tc.want)
			for _, entry := range d.Folds.Current().Entries {
				if entry.Result.Type.ID() == sqltype.Text {
					text, err := entry.Value.Text()
					if err != nil {
						t.Fatal(err)
					}
					if text == "Rats" || text == "<b>Rats</b>" {
						t.Fatal("default headline result folded")
					}
				}
			}
			if d.Regtypes != nil && len(d.Regtypes.Constants) != 0 {
				t.Fatal("default headline captured regconfig")
			}
		}
	})
	t.Run("binding_graphs_and_frames", func(t *testing.T) {
		ftsRankHeadlinePortals(t, "SELECT ts_headline('english'::regconfig,'Cat'::text,'cat'::tsquery),ts_headline('english'::regconfig,$1::text,'rat'::tsquery)", "Rats", "Dogs",
			[][]byte{[]byte("<b>Cat</b>"), []byte("<b>Rats</b>")}, [][]byte{[]byte("<b>Cat</b>"), []byte("Dogs")}, false, 100, true)
		for _, tc := range []struct {
			name string
			id   uint32
		}{{"C", 950}, {"POSIX", 951}} {
			query := `SELECT ts_headline('english'::regconfig,'Cat'::text COLLATE "` + tc.name + `",'cat'::tsquery),ts_headline('english'::regconfig,$1::text COLLATE "` + tc.name + `",'rat'::tsquery)`
			ftsRankHeadlinePortals(t, query, "Rats", "Dogs",
				[][]byte{[]byte("<b>Cat</b>"), []byte("<b>Rats</b>")}, [][]byte{[]byte("<b>Cat</b>"), []byte("Dogs")}, false, tc.id, false)
		}
	})
}
