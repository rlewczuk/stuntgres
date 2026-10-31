package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func porterSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
	}
}

func porterLexizeMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	ct := types[0]
	scan := reflect.TypeFor[string]()
	if b.PG {
		scan = reflect.TypeFor[any]()
	}
	length, lk := ct.Length()
	precision, scale, dk := ct.DecimalSize()
	nullable, nk := ct.Nullable()
	if ct.DatabaseTypeName() != "_TEXT" || ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
		t.Fatalf("lexize descriptors: %#v scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", ct, ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
	}
}

// Porter is an optional provider, never a stock dictionary/configuration.
func porterSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dictionary, config string, stops bool) {
	t.Helper()
	options := "Language=porter"
	if stops {
		options += ",StopWords=english"
	}
	regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+dictionary+" (TEMPLATE=pg_catalog.snowball,"+options+")", 0)
	if config != "" {
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION "+config+" (COPY=pg_catalog.simple)", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION "+config+" ALTER MAPPING FOR asciiword,word,hword_part,hword_asciipart,asciihword,hword WITH "+dictionary, 0)
	}
}
func porterAbsentStock(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='porter_stem' AND dictnamespace='pg_catalog'::regnamespace",
		"SELECT count(*) AS n FROM pg_ts_config WHERE cfgname='porter' AND cfgnamespace='pg_catalog'::regnamespace",
		"SELECT count(*) AS n FROM pg_ts_config_map m JOIN pg_ts_dict d ON d.oid=m.mapdict WHERE d.dictname='porter_stem' AND d.dictnamespace='pg_catalog'::regnamespace",
	} {
		porterSmokeBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}
