package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDictXsynDiagnostics(t *testing.T) {
	type failure struct {
		options, code, message, resource string
		createOnly                       bool
	}
	cases := []failure{
		{options: "unknown=1", code: "22023", message: `unrecognized xsyn parameter: "unknown"`},
		{options: `"MATCHORIG"=true`, code: "22023", message: `unrecognized xsyn parameter: "MATCHORIG"`},
		{options: "rules='../xsyn_sample'", code: "22023", message: `invalid text search configuration file name "../xsyn_sample"`},
		{options: "rules='XSYN_SAMPLE'", code: "22023", message: `invalid text search configuration file name "XSYN_SAMPLE"`},
		{options: "rules='xsyn_sample.rules'", code: "22023", message: `invalid text search configuration file name "xsyn_sample.rules"`},
		{options: "rules=xsyn_missing", resource: "xsyn_missing"},
		{options: "rules=''", code: "F0000"},
		{options: "rules=xsyn_sample,rules=xsyn_missing", resource: "xsyn_missing"},
		{options: "rules='../bad',unknown=1", code: "22023", message: `unrecognized xsyn parameter: "unknown"`},
		{options: "matchorig='yes',unknown=1", code: "42601", message: "matchorig requires a Boolean value"},
		{options: "unknown=1,matchorig='yes'", code: "22023", message: `unrecognized xsyn parameter: "unknown"`},
		{options: "rules", code: "42601", message: "rules requires a parameter", createOnly: true},
	}
	for _, option := range []string{"matchorig", "matchsynonyms", "keeporig", "keepsynonyms"} {
		for _, value := range []string{"'1'", "'0'", "'yes'", "'no'", "'t'", "'f'", "2", "-1"} {
			cases = append(cases, failure{options: option + "=" + value, code: "42601", message: option + " requires a Boolean value"})
		}
		cases = append(cases,
			failure{options: option, code: "42601", message: option + " requires a parameter", createOnly: true},
			failure{options: option + ",rules=xsyn_missing", resource: "xsyn_missing", createOnly: true},
			failure{options: "rules=xsyn_missing," + option, resource: "xsyn_missing", createOnly: true})
	}
	for _, tc := range cases {
		t.Run(tc.options, func(t *testing.T) {
			xsynCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, q := range []string{
					"CREATE EXTENSION dict_xsyn",
					"CREATE TEXT SEARCH DICTIONARY xsyn_valid (TEMPLATE=xsyn_template,rules=xsyn_sample,matchsynonyms=true)",
					"COMMENT ON TEXT SEARCH DICTIONARY xsyn_valid IS 'valid baseline'",
					"CREATE TEXT SEARCH CONFIGURATION xsyn_cfg (COPY=pg_catalog.simple)",
					"ALTER TEXT SEARCH CONFIGURATION xsyn_cfg ALTER MAPPING FOR asciiword WITH xsyn_valid,pg_catalog.simple",
				} {
					xsynExec(t, p, b, c, q, 0)
				}
				h := xsynHeld(t, p, c)
				defer h.Statement.Close()
				const identity = "SELECT d.oid::bigint,d.dictnamespace::bigint,d.dictowner::bigint,d.dicttemplate::bigint,d.dictinitoption,obj_description(d.oid,'pg_ts_dict'),t.tmplinit::oid::bigint,t.tmpllexize::oid::bigint FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.oid='xsyn_valid'::regdictionary::oid"
				const edges = "SELECT classid::bigint,objid::bigint,objsubid,refclassid::bigint,refobjid::bigint,refobjsubid,deptype FROM pg_depend WHERE classid=3600 OR classid=3764 OR classid=3602 ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
				const maps = "SELECT mapcfg::bigint,maptokentype,mapseqno,mapdict::bigint FROM pg_ts_config_map WHERE mapcfg='xsyn_cfg'::regconfig::oid ORDER BY maptokentype,mapseqno"
				xsynOptions(t, p, b, c, "xsyn_valid", "rules = 'xsyn_sample', matchsynonyms = 'true'")
				before, dependencies, mappings := dictIntRows(t, p, c, identity), dictIntRows(t, p, c, edges), dictIntRows(t, p, c, maps)
				check := func() {
					dictIntUnchanged(t, p, c, before, identity)
					dictIntUnchanged(t, p, c, dependencies, edges)
					dictIntUnchanged(t, p, c, mappings, maps)
					for _, e := range []fixture.Executor{c, h} {
						xsynLexemes(t, p, b, e, xsynAll, "xsyn_valid", "sn")
					}
					xsynSelect(t, p, b, c, "SELECT to_tsvector('xsyn_cfg','supernova')::text AS vector", []string{"vector"}, []string{"TEXT"}, [][]any{{"'1987a':1 'sn':1 'sne':1 'supernova':1"}})
					xsynSelect(t, p, b, c, "SELECT dictname FROM pg_ts_dict WHERE dictname='xsyn_bad'", []string{"dictname"}, []string{"NAME"}, nil)
				}
				check()
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				pg := want
				if tc.resource != "" || tc.code == "F0000" {
					want, pg = xsynResourceDiagnostic(t, p, b, c, tc.resource)
				}
				queries := []string{"CREATE TEXT SEARCH DICTIONARY xsyn_bad (TEMPLATE=xsyn_template," + tc.options + ")"}
				if !tc.createOnly {
					queries = append(queries, "ALTER TEXT SEARCH DICTIONARY xsyn_valid ("+tc.options+")")
				}
				for _, q := range queries {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want)
						s.PostgresDiagnostic, s.Prepared, s.ErrorStage = &pg, prepared, "execute"
						xsynRun(t, p, b, c, s)
						check()
					}
				}
				if strings.HasPrefix(tc.options, "unknown=") {
					xsynExec(t, p, b, c, "BEGIN", 0)
					xsynExec(t, p, b, c, "SAVEPOINT xsyn_failure", 0)
					xsynRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TEXT SEARCH DICTIONARY xsyn_valid (unknown=1)", want))
					xsynExec(t, p, b, c, "ROLLBACK TO SAVEPOINT xsyn_failure", 0)
					check()
					xsynExec(t, p, b, c, "COMMIT", 0)
				}
			})
		})
	}
}
