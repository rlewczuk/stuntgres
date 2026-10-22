package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterProviderLimits(t *testing.T) {
	for _, family := range copyFilterProviderFamilies() {
		t.Run(family.name, func(t *testing.T) {
			if family.name == "aclitem" {
				fixture.RoleReferenceCleanup(t, []string{"copy_provider_owner"})
			}
			copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
				copyFilterProviderSetup(t, c, family)
				for _, format := range []string{"text", "csv"} {
					for _, failure := range family.failures {
						t.Run(format+"/"+failure.name, func(t *testing.T) {
							*notices = (*notices)[:0]
							table := "copy_provider_limit_" + family.name
							c.exec(t, "CREATE TABLE "+table+"(id int,v "+family.typ+")")
							defer c.exec(t, "DROP TABLE "+table)
							c.exec(t, "INSERT INTO "+table+" VALUES(99,"+fixture.RegtypeLiteral(family.first)+")")
							input := copyFilterAliasRow(format, 1, family.first) + copyFilterAliasRow(format, 2, failure.bad) + copyFilterAliasRow(format, 3, family.last)
							query := "COPY " + table + " FROM STDIN WITH(FORMAT " + format + ",ON_ERROR ignore,REJECT_LIMIT 1,LOG_VERBOSITY verbose)"
							copyFilterImport(t, c, query, input, 2)
							copyFilterProviderRows(t, c, family, table, true)
							copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"`+failure.shown()+`"`, 2), copyFilterSummary(1))
							*notices = (*notices)[:0]
							c.exec(t, "DELETE FROM "+table+" WHERE id<>99")
							// Two real valid rows have staged before the second rejected row;
							// the failure returns no result/prefix and no successful summary.
							over := input + copyFilterAliasRow(format, 4, failure.bad) + copyFilterAliasRow(format, 5, family.first)
							copyFilterFailure(t, c, query, over, copyFilterProviderLimitDiagnostic(table, failure, 4))
							copyFilterProviderRows(t, c, family, table, false)
							copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"`+failure.shown()+`"`, 2), copyFilterRowNotice(table, "v", `"`+failure.shown()+`"`, 4))
							*notices = (*notices)[:0]
							// A later public operation on the same owner remains usable;
							// absence of a limit is unlimited and silent means no notices.
							copyFilterImport(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,LOG_VERBOSITY silent)", input, 2)
							copyFilterProviderRows(t, c, family, table, true)
							copyFilterNotices(t, *notices)
						})
					}
				}
			})
		})
	}
}
