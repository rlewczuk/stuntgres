package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserResolution(t *testing.T) {
	for _, name := range []string{"default", "DEFAULT", `"default"`, "pg_catalog.default", `"pg_catalog"."default"`, "  pg_catalog . default  "} {
		t.Run(name, func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				ftsParserSelect(t,p,b,"SELECT * FROM ts_parse($1::text,'Word')",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}},name)
				ftsParserSelect(t,p,b,"SELECT * FROM ts_token_type($1::text) ORDER BY tokid",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors(),name)
			})
		})
	}
	t.Run("current_database",func(t *testing.T) {
		ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
			var database string
			if err := b.DB.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database); err != nil { t.Fatal(err) }
			ftsParserSelect(t,p,b,"SELECT * FROM ts_parse($1::text,'Word')",[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}},`"`+strings.ReplaceAll(database,`"`,`""`)+`".pg_catalog.default`)
		})
	})
	for _, tc := range []struct { name, call string; columns, types []string }{
		{"null_text_parser","ts_parse(NULL::text,'Word')",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"null_oid_parser","ts_parse(NULL::oid,'Word')",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"null_document","ts_parse('default',NULL::text)",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"missing_null_document","ts_parse('missing',NULL::text)",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"invalid_oid_null_document","ts_parse(0::oid,NULL::text)",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"empty_document","ts_parse('default','')",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"empty_oid_document","ts_parse(3722::oid,'')",[]string{"tokid","token"},[]string{"INT4","TEXT"}},
		{"null_text_descriptors","ts_token_type(NULL::text)",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"}},
		{"null_oid_descriptors","ts_token_type(NULL::oid)",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"}},
	} {
		t.Run(tc.name,func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) { ftsParserSelect(t,p,b,"SELECT * FROM "+tc.call,tc.columns,tc.types,nil) })
		})
	}
}
