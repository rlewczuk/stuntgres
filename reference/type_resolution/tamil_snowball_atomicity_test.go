package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Snapshot only allocator-owned identities and accepted ordered definitions.
// Provider outputs and vectors are independently specified at each caller.
func tamilDefinitionSnapshot(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, config string) func() {
	t.Helper()
	queries := []fixture.Step{
		fixture.Select("SELECT oid::bigint,dictowner::bigint,dictnamespace::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary", []string{"oid", "dictowner", "dictnamespace", "dicttemplate", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, nil, dictionary),
		fixture.Select("SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=$1::regconfig", []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, nil, config),
		fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg=$1::regconfig ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, nil, config),
		fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE (classid=3600 AND objid=$1::regdictionary) OR (classid=3602 AND objid=$2::regconfig) ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, nil, dictionary, config),
	}
	for i := range queries {
		s := &queries[i]
		rows, err := c.QueryContext(p.Ctx, s.Query, s.Args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(s.Columns))
			dest := make([]any, len(values))
			for j := range values {
				dest[j] = &values[j]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for j := range values {
				values[j] = fixture.Normalize(values[j])
			}
			s.Rows = append(s.Rows, values)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if len(s.Rows) == 0 && i < 3 {
			t.Fatal("missing accepted definition", s.Query)
		}
	}
	var before bytes.Buffer
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		for _, s := range queries {
			tamilSmokeBoth(t, p, b, c, s)
		}
		if !b.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed change altered complete native state", err)
			}
		}
	}
}
func TestReferenceTamilSnowballAtomicity(t *testing.T) {
	t.Run("ownership", tamilOwnership)
	t.Run("failed-publication", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY tamil_atomic (TEMPLATE=snowball,Language=tamil,StopWords=english)", 0)
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION tamil_atomic_config (COPY=tamil)", 0)
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tamil_atomic_config ALTER MAPPING FOR asciiword,word WITH tamil_atomic", 0)
			preserved := tamilDefinitionSnapshot(t, p, b, c, "tamil_atomic", "tamil_atomic_config")
			tamilMissingStopwords(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY tamil_atomic (language=english,stopwords=tamil)")
			preserved()
			tamilMissingStopwords(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY tamil_unpublished (TEMPLATE=snowball,language=tamil,stopwords=tamil)")
			preserved()
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='tamil_unpublished'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('tamil_atomic',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{தமிழ்}"}}, "தமிழில்"))
			tamilSmokeBoth(t, p, b, c, fixture.Select("SELECT to_tsvector('tamil_atomic_config','ONLY தமிழில்') AS normalized", []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{"'தமிழ்':2"}}))
		})
	})
}
