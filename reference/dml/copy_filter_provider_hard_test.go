package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterProviderHardErrors(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
			for _, format := range []string{"text", "csv"} {
				// Same SQLSTATE as soft lexical input, but these released helpers
				// throw: geometry arithmetic, numeric_in and recursive int32 parsing.
				for _, tc := range []struct {
					name, typ, databaseType, good string
					failure                       copyFilterProviderFailure
				}{
					{"line_overflow", "line", "LINE", "{1,-1,0}", copyFilterProviderFailure{bad: "(1e308,1e308),(-1e308,-1e308)", code: "22003", message: "value out of range: overflow"}},
					{"line_underflow", "line", "LINE", "{1,-1,0}", copyFilterProviderFailure{bad: "(1e-308,0),(1e308,1)", code: "22003", message: "value out of range: underflow"}},
					{"jsonpath_numeric_overflow", "jsonpath", "JSONPATH", "$", copyFilterProviderFailure{bad: "1e131072", code: "22003", message: "value overflows numeric format"}},
					{"jsonpath_numeric_scale", "jsonpath", "JSONPATH", "$", copyFilterProviderFailure{bad: "1e-16384", code: "22003", message: "value overflows numeric format"}},
					{"jsonpath_recursive_overflow", "jsonpath", "JSONPATH", "$", copyFilterProviderFailure{bad: "$.**{2147483648}", code: "22003", message: `value "2147483648" is out of range for type integer`}},
				} {
					t.Run(format+"/"+tc.name, func(t *testing.T) {
						*notices = (*notices)[:0]
						table := "copy_provider_hard_" + tc.name
						c.exec(t, "CREATE TABLE "+table+"(id int,v "+tc.typ+")")
						defer c.exec(t, "DROP TABLE "+table)
						c.exec(t, "INSERT INTO "+table+" VALUES(99,"+fixture.RegtypeLiteral(tc.good)+")")
						input := copyFilterAliasRow(format, 1, tc.good) + copyFilterAliasRow(format, 2, tc.failure.bad) + copyFilterAliasRow(format, 3, tc.good)
						for _, action := range []string{"stop", "ignore"} {
							copyFilterFailure(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR "+action+",LOG_VERBOSITY verbose)", input, tc.failure.diagnostic(table, 2))
							copyFilterProviderRows(t, c, copyFilterProviderFamily{typ: tc.typ, databaseType: tc.databaseType, firstValue: tc.good}, table, false)
							copyFilterNotices(t, *notices)
						}
					})
				}
			}
		})
	})

	for _, family := range copyFilterProviderFamilies() {
		routes := []string{"default"}
		// Pure text-to-provider casts require no domain catalog capture. BIT
		// explicit casts pad/truncate; ACL domain authority is not introduced.
		switch family.name {
		case "array", "int4range", "jsonpath", "jsonb", "tsvector", "tsquery", "query_int":
			routes = append(routes, "where", "domain")
		}
		for _, route := range routes {
			// A fresh bounded Pair per family/route keeps the existing30s
			// fixture context unchanged rather than extending its deadline.
			t.Run(family.name+"_"+route, func(t *testing.T) {
				if family.name == "aclitem" {
					fixture.RoleReferenceCleanup(t, []string{"copy_provider_owner"})
				}
				copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
					copyFilterProviderSetup(t, c, family)
					for _, format := range []string{"text", "csv"} {
						for _, failure := range family.failures {
							t.Run(format+"/"+failure.name, func(t *testing.T) {
								*notices = (*notices)[:0]
								if route == "default" {
									// Real nextval exhaustion wins after the row notice;
									// no new volatile arithmetic/default recipe is used.
									table := "copy_provider_default_" + family.name
									c.exec(t, "CREATE SEQUENCE copy_provider_default_seq MINVALUE 0 START 1 MAXVALUE 1 NO CYCLE")
									defer c.exec(t, "DROP SEQUENCE copy_provider_default_seq")
									c.exec(t, "CREATE TABLE "+table+"(id int,v "+family.typ+",stamp bigint DEFAULT nextval('copy_provider_default_seq'))")
									defer c.exec(t, "DROP TABLE "+table)
									c.exec(t, "INSERT INTO "+table+" VALUES(99,"+fixture.RegtypeLiteral(family.first)+",99)")
									input := copyFilterAliasRow(format, 1, family.first) + copyFilterAliasRow(format, 2, failure.bad) + copyFilterAliasRow(format, 3, family.last)
									copyFilterFailure(t, c, "COPY "+table+"(id,v) FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,REJECT_LIMIT 1,LOG_VERBOSITY verbose)", input, fixture.Diagnostic{Code: "2200H", Message: `nextval: reached maximum value of sequence "copy_provider_default_seq" (1)`, Context: fmt.Sprintf("COPY %s, line 2, column v: \"%s\"", table, failure.shown())})
									c.selectRows(t, "SELECT id,v,stamp FROM "+table+" ORDER BY id", []string{"id", "v", "stamp"}, []string{"INT4", copyFilterProviderType(t, c, family), "INT8"}, [][]any{{int64(99), family.firstValue, int64(99)}})
									c.selectRows(t, "SELECT last_value,is_called FROM copy_provider_default_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}})
									copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"`+failure.shown()+`"`, 2))
								} else {
									table := "copy_provider_" + route + "_" + family.name + "_" + format + "_" + failure.name
									typ := "text"
									if route == "domain" {
										typ = "copy_provider_check"
										statement := "CREATE DOMAIN " + typ + " AS text CHECK(VALUE::" + family.typ + " IS NOT NULL)"
										_, err := c.conn.ExecContext(c.ctx, statement)
										if err != nil {
											t.Fatal(statement, err)
										}
										defer c.exec(t, "DROP DOMAIN "+typ)
									}
									c.exec(t, "CREATE TABLE "+table+"(id int,v "+typ+")")
									defer c.exec(t, "DROP TABLE "+table)
									// The ordinary supported sentinel INSERT precedes COPY.
									c.exec(t, "INSERT INTO "+table+" VALUES(99,"+fixture.RegtypeLiteral(family.first)+")")
									input := copyFilterAliasRow(format, 1, family.first) + copyFilterAliasRow(format, 2, failure.bad) + copyFilterAliasRow(format, 3, family.last)
									query := "COPY " + table + " FROM STDIN WITH(FORMAT " + format + ",ON_ERROR ignore,LOG_VERBOSITY verbose)"
									want := failure.diagnostic(table, 2)
									if route == "where" {
										query += " WHERE v::" + family.typ + " IS NOT NULL"
										raw := strings.TrimSuffix(copyFilterAliasRow(format, 2, failure.bad), "\n")
										// Only ASCII long words exceed this row-display cap.
										if len(raw) > 100 {
											raw = raw[:100] + "..."
										}
										want.Context = fmt.Sprintf("COPY %s, line 2: \"%s\"", table, raw)
										if failure.prefix != "" {
											want.Context = failure.prefix + "\n" + want.Context
										}
									}
									copyFilterFailure(t, c, query, input, want)
									c.selectRows(t, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), family.first}})
									c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", "99\t"+family.first+"\n", 1)
									copyFilterNotices(t, *notices)
								}
							})
						}
					}
				})
			})
		}
	}
}
