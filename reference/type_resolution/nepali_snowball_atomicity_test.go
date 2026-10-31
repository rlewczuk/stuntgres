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
func nepaliDefinitionSnapshot(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, config string) func() {
	t.Helper()
	queries := []fixture.Step{
		fixture.Select("SELECT oid::bigint,dictowner::bigint,dictnamespace::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary", []string{"oid", "dictowner", "dictnamespace", "dicttemplate", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, nil, dictionary),
		fixture.Select("SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=$1::regconfig", []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, nil, config),
		fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg=$1::regconfig ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, nil, config),
		fixture.Select("SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE (classid=3600 AND objid=$1::regdictionary::oid) OR (classid=3602 AND objid=$2::regconfig::oid) ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, nil, dictionary, config),
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
		if len(s.Rows) == 0 {
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
			nepaliSmokeBoth(t, p, b, c, s)
		}
		if !b.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed change altered complete native state", err)
			}
		}
	}
}
func TestReferenceNepaliSnowballAtomicity(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY nepali_atomic (TEMPLATE=snowball,Language=nepali,StopWords=nepali)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION nepali_atomic_config (COPY=nepali)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION nepali_atomic_config ALTER MAPPING FOR asciiword,word WITH nepali_atomic", 0)
		q := "SELECT ts_lexize('nepali_atomic',$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		preserved := nepaliDefinitionSnapshot(t, p, b, c, "nepali_atomic", "nepali_atomic_config")
		for _, options := range []string{"language=english,accept=true", "stopwords='',accept=true", "stopwords,language=nepali_bogus"} {
			code, message := "22023", `unrecognized Snowball parameter: "accept"`
			if options == "stopwords,language=nepali_bogus" {
				code, message = "42704", `no Snowball stemmer available for language "nepali_bogus" and encoding "UTF8"`
			}
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY nepali_atomic ("+options+")", code, message)
			preserved()
			for _, tc := range [][2]string{{"घरहरू", "{घर}"}, {"सबै", "{}"}, {"RATS", "{rats}"}} {
				dictionaryResult(t, p, b, held, q, tc[1], tc[0])
			}
		}
		nepaliMissingStopwords(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY nepali_atomic_absent (TEMPLATE=snowball,Language=nepali,StopWords=nepali_missing)")
		preserved()
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='nepali_atomic_absent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}
