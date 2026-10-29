package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Normal display was observed independently on pinned PostgreSQL18.6. Quoted
// display applies the existing identifier-quote contract to the same analyzed
// identities; its selected oracle assertions independently exercise that profile.
const ftsDebugExplicitBody = `SELECT tt.alias,
     tt.description,
     parse.token,
     ARRAY( SELECT (m.mapdict)::regdictionary AS mapdict
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY m.mapseqno) AS dictionaries,
     ( SELECT (m.mapdict)::regdictionary AS mapdict
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY (ts_lexize((m.mapdict)::regdictionary, parse.token) IS NULL), m.mapseqno
          LIMIT 1) AS dictionary,
     ( SELECT ts_lexize((m.mapdict)::regdictionary, parse.token) AS ts_lexize
            FROM pg_ts_config_map m
           WHERE ((m.mapcfg = (ts_debug.config)::oid) AND (m.maptokentype = parse.tokid))
           ORDER BY (ts_lexize((m.mapdict)::regdictionary, parse.token) IS NULL), m.mapseqno
          LIMIT 1) AS lexemes
    FROM ts_parse(( SELECT pg_ts_config.cfgparser
            FROM pg_ts_config
           WHERE (pg_ts_config.oid = (ts_debug.config)::oid)), ts_debug.document) parse(tokid, token),
     ts_token_type(( SELECT pg_ts_config.cfgparser
            FROM pg_ts_config
           WHERE (pg_ts_config.oid = (ts_debug.config)::oid))) tt(tokid, alias, description)
   WHERE (tt.tokid = parse.tokid)`
const ftsDebugDefaultBody = `SELECT ts_debug.alias,
     ts_debug.description,
     ts_debug.token,
     ts_debug.dictionaries,
     ts_debug.dictionary,
     ts_debug.lexemes
    FROM ts_debug(get_current_ts_config(), ts_debug.document) ts_debug(alias, description, token, dictionaries, dictionary, lexemes)`
const ftsDebugExplicitQuotedBody = `SELECT "tt"."alias",
     "tt"."description",
     "parse"."token",
     ARRAY( SELECT ("m"."mapdict")::"regdictionary" AS "mapdict"
            FROM "pg_ts_config_map" "m"
           WHERE (("m"."mapcfg" = ("ts_debug"."config")::"oid") AND ("m"."maptokentype" = "parse"."tokid"))
           ORDER BY "m"."mapseqno") AS "dictionaries",
     ( SELECT ("m"."mapdict")::"regdictionary" AS "mapdict"
            FROM "pg_ts_config_map" "m"
           WHERE (("m"."mapcfg" = ("ts_debug"."config")::"oid") AND ("m"."maptokentype" = "parse"."tokid"))
           ORDER BY ("ts_lexize"(("m"."mapdict")::"regdictionary", "parse"."token") IS NULL), "m"."mapseqno"
          LIMIT 1) AS "dictionary",
     ( SELECT "ts_lexize"(("m"."mapdict")::"regdictionary", "parse"."token") AS "ts_lexize"
            FROM "pg_ts_config_map" "m"
           WHERE (("m"."mapcfg" = ("ts_debug"."config")::"oid") AND ("m"."maptokentype" = "parse"."tokid"))
           ORDER BY ("ts_lexize"(("m"."mapdict")::"regdictionary", "parse"."token") IS NULL), "m"."mapseqno"
          LIMIT 1) AS "lexemes"
    FROM "ts_parse"(( SELECT "pg_ts_config"."cfgparser"
            FROM "pg_ts_config"
           WHERE ("pg_ts_config"."oid" = ("ts_debug"."config")::"oid")), "ts_debug"."document") "parse"("tokid", "token"),
     "ts_token_type"(( SELECT "pg_ts_config"."cfgparser"
            FROM "pg_ts_config"
           WHERE ("pg_ts_config"."oid" = ("ts_debug"."config")::"oid"))) "tt"("tokid", "alias", "description")
   WHERE ("tt"."tokid" = "parse"."tokid")`
const ftsDebugDefaultQuotedBody = `SELECT "ts_debug"."alias",
     "ts_debug"."description",
     "ts_debug"."token",
     "ts_debug"."dictionaries",
     "ts_debug"."dictionary",
     "ts_debug"."lexemes"
    FROM "ts_debug"("get_current_ts_config"(), "ts_debug"."document") "ts_debug"("alias", "description", "token", "dictionaries", "dictionary", "lexemes")`

func TestReferenceFTSDebugDeclarationBodies(t *testing.T) {
	for _, tc := range []struct{ name, signature, formals, quotedFormals, body, quotedBody string }{
		{"explicit", "regconfig,text", "config regconfig, document text", `"config" "regconfig", "document" "text"`, ftsDebugExplicitBody, ftsDebugExplicitQuotedBody},
		{"default", "text", "document text", `"document" "text"`, ftsDebugDefaultBody, ftsDebugDefaultQuotedBody},
	} {
		for _, quoted := range []bool{false, true} {
			setting := "off"
			if quoted {
				setting = "on"
			}
			t.Run(tc.name+"/"+setting, func(t *testing.T) {
				p := fixture.NewScenario(t)
				backends := p.Backends()
				for i := len(backends) - 1; i >= 0; i-- {
					b := backends[i]
					t.Run(b.Name, func(t *testing.T) {
						c := ftsInspectionConnection(t, p, b)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+setting, 0))
						defer c.ExecContext(p.Ctx, "RESET quote_all_identifiers")
						name, formals, outputs, result, language, body := "pg_catalog.ts_debug", tc.formals, "OUT alias text, OUT description text, OUT token text, OUT dictionaries regdictionary[], OUT dictionary regdictionary, OUT lexemes text[]", "record", "sql", tc.body
						if quoted {
							name, formals, outputs, result, language, body = `"pg_catalog"."ts_debug"`, tc.quotedFormals, `OUT "alias" "text", OUT "description" "text", OUT "token" "text", OUT "dictionaries" "regdictionary"[], OUT "dictionary" "regdictionary", OUT "lexemes" "text"[]`, `"record"`, `"sql"`, tc.quotedBody
						}
						want := "CREATE OR REPLACE FUNCTION " + name + "(" + formals + ", " + outputs + ")\n RETURNS SETOF " + result + "\n LANGUAGE " + language + "\n STABLE PARALLEL SAFE STRICT\nBEGIN ATOMIC\n " + body + ";\nEND\n"
						query := "SELECT pg_get_functiondef('pg_catalog.ts_debug(" + tc.signature + ")'::regprocedure) AS definition"
						s := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{want}})
						ftsInspectionBoth(t, p, b, c, s)
						stmt, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
						for range 2 {
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
					})
				}
			})
		}
	}
}
