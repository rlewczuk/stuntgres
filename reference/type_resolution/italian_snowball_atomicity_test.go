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
func italianDefinitionSnapshot(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, config string) func() {
	t.Helper()
	queries := []fixture.Step{
		fixture.Select("SELECT oid::bigint,dictowner::bigint,dictnamespace::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid=$1::regdictionary", []string{"oid", "dictowner", "dictnamespace", "dicttemplate", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT"}, nil, dictionary),
		fixture.Select("SELECT oid::bigint,cfgowner::bigint,cfgnamespace::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=$1::regconfig", []string{"oid", "cfgowner", "cfgnamespace", "cfgparser"}, []string{"INT8", "INT8", "INT8", "INT8"}, nil, config),
		fixture.Select("SELECT maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg=$1::regconfig ORDER BY maptokentype,mapseqno", []string{"maptokentype", "mapseqno", "mapdict"}, []string{"INT4", "INT4", "INT8"}, nil, config),
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
			italianSmokeBoth(t, p, b, c, s)
		}
		if !b.PG {
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed change altered complete native state", err)
			}
		}
	}
}
