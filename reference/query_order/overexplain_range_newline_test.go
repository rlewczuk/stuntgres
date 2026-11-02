package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainRangeNewlineIdentifiers(t *testing.T) {
	overexplainRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		overexplainRangeSeed(t, p, b, c)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		cases := []struct {
			name, child, rangeName, textIdentifier string
			columns                                []string
			pgAlias, pgEref                        string
			valuesQuery                            string
		}{
			{
				name: "unused-column-alias",
				// The SQL sent to each backend contains a literal LF, not \\n.
				// Non-VERBOSE SELECT 1 does not otherwise expose this unused name.
				child:     "SELECT 1 FROM range_src AS a(\"first\nline\",v)",
				rangeName: "a", textIdentifier: "\"first\nline\"",
				columns: []string{"first\nline", "v"},
				pgAlias: "a (\"first\nline\", v)", pgEref: "a (\"first\nline\", v)",
				valuesQuery: "SELECT a.\"first\nline\",a.v FROM range_src AS a(\"first\nline\",v) ORDER BY a.\"first\nline\"",
			},
			{
				name:      "range-alias",
				child:     "SELECT 1 FROM range_src AS \"range\nalias\"",
				rangeName: "range\nalias", textIdentifier: "\"range\nalias\"",
				columns: []string{"id", "v"},
				pgAlias: "\"range\nalias\" ()", pgEref: "\"range\nalias\" (id, v)",
				valuesQuery: "SELECT \"range\nalias\".id,\"range\nalias\".v FROM range_src AS \"range\nalias\" ORDER BY \"range\nalias\".id",
			},
			{
				name:      "consecutive-column-newlines",
				child:     "SELECT 1 FROM range_src AS a(\"first\n\nline\",v)",
				rangeName: "a", textIdentifier: "\"first\n\nline\"",
				columns: []string{"first\n\nline", "v"},
				pgAlias: "a (\"first\n\nline\", v)", pgEref: "a (\"first\n\nline\", v)",
				valuesQuery: "SELECT a.\"first\n\nline\",a.v FROM range_src AS a(\"first\n\nline\",v) ORDER BY a.\"first\n\nline\"",
			},
		}
		for _, tc := range cases {
			for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
				t.Run(tc.name+"/"+format, func(t *testing.T) {
					if tc.name == "range-alias" {
						// Ordinary rendering owns the same physical-line seam,
						// independently of RANGE_TABLE and VERBOSE activation.
						for _, options := range []string{"COSTS FALSE, VERBOSE FALSE", "RANGE_TABLE FALSE, COSTS FALSE, VERBOSE"} {
							for _, prepared := range []bool{false, true} {
								r := overexplainQuery(t, p, c, overexplainSQL(format, options, tc.child), format, prepared)
								overexplainRange(t, b, r, format, false)
								if format == "TEXT" {
									if !strings.Contains(r.text, tc.textIdentifier) {
										t.Fatal("ordinary TEXT changed newline-bearing range alias", r.text)
									}
								} else {
									found := false
									for _, node := range explainJSONNodes(r.document) {
										if node["Alias"] == tc.rangeName {
											found = true
										}
									}
									if !found {
										t.Fatal("ordinary structured plan lost exact accepted alias", r.document)
									}
								}
							}
						}
					}
					if tc.name == "unused-column-alias" {
						// This distinguishes the new range list from unrelated
						// VERBOSE expression rendering or an ordinary scan label.
						for _, prepared := range []bool{false, true} {
							q := overexplainSQL(format, "RANGE_TABLE FALSE, COSTS FALSE, VERBOSE FALSE", tc.child)
							r := overexplainQuery(t, p, c, q, format, prepared)
							overexplainRange(t, b, r, format, false)
							if format == "TEXT" && strings.Contains(r.text, tc.textIdentifier) {
								t.Fatal("unused column alias escaped disabled range projection", r.text)
							}
						}
					}
					q := overexplainSQL(format, "RANGE_TABLE, COSTS FALSE, VERBOSE FALSE", tc.child)
					check := func(r overexplainResult) {
						entries := overexplainRange(t, b, r, format, true)
						if format == "TEXT" {
							// overexplainRows already requires one physical line
							// per non-NULL SQL row. Joining those rows must restore
							// the exact newline-bearing identifier, even empty lines.
							if !strings.Contains(r.text, tc.textIdentifier) {
								t.Fatal("TEXT deleted, escaped or normalized identifier line boundaries", tc.textIdentifier, r.text)
							}
							return
						}
						e := overexplainRangeNamed(t, b, entries, "relation", tc.rangeName)
						if b.PG {
							// Released PG owns its real flat alias/RTE strings;
							// no native Columns or Scope IDs are expected here.
							if e["Alias"] != tc.pgAlias || e["Eref"] != tc.pgEref || e["Relation"] != "range_src" || overexplainInteger(t, e["Permission Info Index"], format == "XML") <= 0 {
								t.Fatal("PG lost exact accepted newline alias or actual relation/permission reference", e)
							}
						} else {
							overexplainRangeRelation(t, p, c, e, "range_src", format)
							if e["Range Name"] != tc.rangeName || !reflect.DeepEqual(overexplainRangeStrings(t, e["Columns"]), tc.columns) {
								t.Fatal("native structured projection changed raw identifier values", e)
							}
						}
					}
					for _, prepared := range []bool{false, true} {
						check(overexplainQuery(t, p, c, q, format, prepared))
					}
					held, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					rows, err := held.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(overexplainRows(t, rows, format))
				})
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(tc.valuesQuery, tc.columns, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		overexplainRangeValues(t, p, b, c)
	})
}
