package reference_test

import (
	"database/sql"
	"path"
	"testing"

	"stuntgres/reference/fixture"
)

func unaccentDictionaryMissingRulesStep(q, resource string) fixture.Step {
	native := dictionaryDiagnostic("F0000", `could not open unaccent file "`+resource+`.rules": No such file or directory`)
	pg := dictionaryDiagnostic("F0000", `could not open unaccent file "`+path.Join(fixture.TextSearchDataDirectory, resource+".rules")+`": No such file or directory`)
	s := fixture.ErrorDiagnostic(q, native)
	s.PostgresDiagnostic = &pg
	return s
}

func TestReferenceUnaccentDictionaryRules(t *testing.T) {
	for _, tc := range []struct{ options, code, message, resource string }{
		{"", "22023", "missing Rules parameter", ""},
		{"rules", "42601", "rules requires a parameter", ""},
		{"rules=unaccent,rules=unaccent", "22023", "multiple Rules parameters", ""},
		{"bogus=1", "22023", `unrecognized Unaccent parameter: "bogus"`, ""},
		{`"Rules"=unaccent`, "22023", `unrecognized Unaccent parameter: "Rules"`, ""},
		{"rules='../unaccent'", "22023", `invalid text search configuration file name "../unaccent"`, ""},
		{"rules='unaccent.rules'", "22023", `invalid text search configuration file name "unaccent.rules"`, ""},
		{"rules='UNACCENT'", "22023", `invalid text search configuration file name "UNACCENT"`, ""},
		{"rules='é'", "22023", `invalid text search configuration file name "é"`, ""},
		{"rules='a/b'", "22023", `invalid text search configuration file name "a/b"`, ""},
		{"rules=''", "F0000", "", ""},
		{"rules=unaccent_missing", "F0000", "", "unaccent_missing"},
		{"rules=unaccent_missing,bogus=1", "F0000", "", "unaccent_missing"},
		{"bogus=1,rules=unaccent_missing", "22023", `unrecognized Unaccent parameter: "bogus"`, ""},
		{"rules=unaccent_missing,rules=unaccent", "F0000", "", "unaccent_missing"},
		{"rules=unaccent,rules=unaccent_missing", "22023", "multiple Rules parameters", ""},
	} {
		t.Run(tc.options, func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
				var dependencies int64
				if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=3600").Scan(&dependencies); err != nil {
					t.Fatal(err)
				}
				q := "CREATE TEXT SEARCH DICTIONARY unaccent_rule_bad (TEMPLATE=public.unaccent"
				if tc.options != "" {
					q += "," + tc.options
				}
				q += ")"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic(tc.code, tc.message))
					if tc.code == "F0000" {
						s = unaccentDictionaryMissingRulesStep(q, tc.resource)
					}
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='unaccent_rule_bad' AND dictnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
					regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend WHERE classid=3600", []string{"n"}, []string{"INT8"}, [][]any{{dependencies}})
				}
			})
		})
	}
	t.Run("alter_atomicity_and_merged_order", func(t *testing.T) {
		dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY unaccent_rule_live (TEMPLATE=public.unaccent,RULES=unaccent)", 0)
			q := "SELECT public.unaccent('unaccent_rule_live'::regdictionary,$1::text) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var oid, owner, template, namespace, dependencies int64
			var options string
			snapshot := "SELECT d.oid::bigint AS id,d.dictowner::bigint AS owner,d.dicttemplate::bigint AS template,d.dictnamespace::bigint AS namespace,d.dictinitoption,(SELECT count(*) FROM pg_depend dep WHERE dep.classid=3600 AND dep.objid=d.oid) AS dependencies FROM pg_ts_dict d WHERE d.oid='unaccent_rule_live'::regdictionary::oid"
			if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&oid, &owner, &template, &namespace, &options, &dependencies); err != nil || options != "rules = 'unaccent'" || dependencies < 2 {
				t.Fatal(oid, owner, template, namespace, options, dependencies, err)
			}
			check := func(t *testing.T) {
				t.Helper()
				regtsSelect(t, p, b, c, snapshot, []string{"id", "owner", "template", "namespace", "dictinitoption", "dependencies"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT", "INT8"}, [][]any{{oid, owner, template, namespace, options, dependencies}})
				unaccentDictionaryText(t, p, b, held, q, "Hotel", "Hôtel")
			}
			// ALTER removes each old match and appends its replacement. Unlike
			// CREATE, duplicate replacements leave only the final value.
			// Each case and route runs serially in its own named subtest so an
			// unexpected diagnostic cannot hide any later ordering witness.
			for _, tc := range []struct{ name, options, code, message string }{
				{"remove_rules", "rules", "22023", "missing Rules parameter"},
				{"valid_rules_before_bogus", "rules=unaccent,bogus=1", "22023", `unrecognized Unaccent parameter: "bogus"`},
				{"bogus_before_valid_rules", "bogus=1,rules=unaccent", "22023", `unrecognized Unaccent parameter: "bogus"`},
				{"missing_rules", "rules=unaccent_missing", "F0000", ""},
				{"missing_rules_before_bogus", "rules=unaccent_missing,bogus=1", "F0000", ""},
				{"bogus_before_missing_rules", "bogus=1,rules=unaccent_missing", "22023", `unrecognized Unaccent parameter: "bogus"`},
				{"duplicate_valid_rules", "rules=unaccent,rules=unaccent", "", ""},
				{"missing_then_valid_rules", "rules=unaccent_missing,rules=unaccent", "", ""},
				{"valid_then_missing_rules", "rules=unaccent,rules=unaccent_missing", "F0000", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						route := "direct"
						if prepared {
							route = "prepared"
						}
						t.Run(route, func(t *testing.T) {
							defer check(t)
							q := "ALTER TEXT SEARCH DICTIONARY unaccent_rule_live (" + tc.options + ")"
							s := fixture.Exec(q, 0)
							if tc.code == "F0000" {
								s = unaccentDictionaryMissingRulesStep(q, "unaccent_missing")
							} else if tc.code != "" {
								s = fixture.ErrorDiagnostic(q, dictionaryDiagnostic(tc.code, tc.message))
							}
							s.Prepared, s.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				})
			}
			regtsExec(t, p, b, c, "BEGIN", 0)
			regtsExec(t, p, b, c, "SAVEPOINT unaccent_rules_save", 0)
			regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY unaccent_rule_rolled (TEMPLATE=public.unaccent,RULES=unaccent)", 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, unaccentDictionaryMissingRulesStep("ALTER TEXT SEARCH DICTIONARY unaccent_rule_live (rules=unaccent_missing)", "unaccent_missing"))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", dictionaryDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			regtsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT unaccent_rules_save", 0)
			check(t)
			regtsExec(t, p, b, c, "ROLLBACK", 0)
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='unaccent_rule_rolled' AND dictnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			check(t)
		})
	})
}
