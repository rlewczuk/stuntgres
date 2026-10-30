package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func hungarianCreateCarriers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TEXT SEARCH CONFIGURATION hungarian_original_config (COPY=hungarian)",
		"ALTER TEXT SEARCH CONFIGURATION hungarian_original_config ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH dictionary_right.hungarian_saved",
		"ALTER TEXT SEARCH CONFIGURATION hungarian_original_config RENAME TO hungarian_saved_config",
		"ALTER TEXT SEARCH CONFIGURATION hungarian_saved_config SET SCHEMA dictionary_right",
		"ALTER TEXT SEARCH CONFIGURATION dictionary_right.hungarian_saved_config OWNER TO dictionary_creator",
		"CREATE DOMAIN dictionary_right.hu_cfg_domain AS regconfig",
		"CREATE TYPE dictionary_right.hu_cfg_pair AS (v regconfig,d dictionary_right.hu_cfg_domain)",
		"CREATE TABLE dictionary_right.hu_cfg_rows(id int,c regconfig DEFAULT 'dictionary_right.hungarian_saved_config'::regconfig,a regconfig[] DEFAULT ARRAY['dictionary_right.hungarian_saved_config'::regconfig],d dictionary_right.hu_cfg_domain,p dictionary_right.hu_cfg_pair)",
		"CREATE VIEW dictionary_right.hu_cfg_view AS SELECT c,a,d,p FROM dictionary_right.hu_cfg_rows",
	} {
		regtsExec(t, p, b, c, q, 0)
	}
	regtsExec(t, p, b, c, "INSERT INTO dictionary_right.hu_cfg_rows(id,d,p) VALUES(1,'dictionary_right.hungarian_saved_config',ROW('dictionary_right.hungarian_saved_config'::regconfig,'dictionary_right.hungarian_saved_config'::dictionary_right.hu_cfg_domain)::dictionary_right.hu_cfg_pair)", 1)
}

func hungarianSavedCarriers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, relation := range []string{"dictionary_right.hu_cfg_rows", "dictionary_right.hu_cfg_view"} {
		regtsSelect(t, p, b, c, "SELECT c='dictionary_right.hungarian_saved_config'::regconfig AS scalar_same,a[1]='dictionary_right.hungarian_saved_config'::regconfig AS array_same,d::regconfig=c AS domain_same,(p).v=c AS pair_same,(p).d::regconfig=c AS nested_same FROM "+relation, []string{"scalar_same", "array_same", "domain_same", "pair_same", "nested_same"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}})
	}
	regtsSelect(t, p, b, c, "SELECT cfgowner='dictionary_creator'::regrole::oid AS owned,cfgnamespace='dictionary_right'::regnamespace AS moved,cfgparser::bigint FROM pg_ts_config WHERE oid='dictionary_right.hungarian_saved_config'::regconfig", []string{"owned", "moved", "cfgparser"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, true, int64(3722)}})
	regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_config_map WHERE mapcfg='dictionary_right.hungarian_saved_config'::regconfig AND mapdict='dictionary_right.hungarian_saved'::regdictionary AND maptokentype IN (1,2,10,11,16,17) AND mapseqno=1", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}})
	hungarianSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('dictionary_right.hungarian_saved_config','cikk házak') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'cik':1 'ház':2"}}))
	regtsExec(t, p, b, c, "INSERT INTO dictionary_right.hu_cfg_rows(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT c='dictionary_right.hungarian_saved_config'::regconfig AS scalar_same,a[1]=c AS array_same FROM dictionary_right.hu_cfg_rows WHERE id=2", []string{"scalar_same", "array_same"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
	regtsExec(t, p, b, c, "DELETE FROM dictionary_right.hu_cfg_rows WHERE id=2", 1)
}
