package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterAliasLimits(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		c.exec(t, "CREATE TABLE copy_alias_target_a(id int)")
		c.exec(t, "CREATE TABLE copy_alias_target_b(id int)")
		for _, format := range []string{"text", "csv"} {
			t.Run(format, func(t *testing.T) {
				const table = "copy_alias_limits"
				c.exec(t, "CREATE TABLE "+table+"(id int,v regclass)")
				defer c.exec(t, "DROP TABLE "+table)
				c.exec(t, "INSERT INTO "+table+" VALUES(99,'copy_alias_target_b')")
				types := []string{"INT4", "REGCLASS"}
				if c.pg {
					types[1] = "2205"
				} // Existing genuine pgx alias metadata.
				check := func(want [][]any, output string, count int64) {
					c.selectRows(t, "SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, types, want)
					c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", output, count)
				}
				before := [][]any{{int64(99), "copy_alias_target_b"}}
				beforeText := "99\tcopy_alias_target_b\n"
				input := copyFilterAliasRow(format, 1, "copy_alias_target_a") + copyFilterAliasRow(format, 2, "copy_alias_absent_one") + copyFilterAliasRow(format, 3, "copy_alias_target_b")
				for _, stop := range []string{"", ",ON_ERROR stop"} {
					copyFilterFailure(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+stop+")", input, copyFilterAliasDiagnostic(table, "copy_alias_absent_one", 2, "42P01", `relation "copy_alias_absent_one" does not exist`))
					check(before, beforeText, 1)
					copyFilterNotices(t, *notices)
				}
				query := "COPY " + table + " FROM STDIN WITH(FORMAT " + format + ",ON_ERROR ignore,REJECT_LIMIT 1,LOG_VERBOSITY verbose)"
				copyFilterImport(t, c, query, input, 2)
				check([][]any{{int64(1), "copy_alias_target_a"}, {int64(3), "copy_alias_target_b"}, {int64(99), "copy_alias_target_b"}}, "1\tcopy_alias_target_a\n3\tcopy_alias_target_b\n99\tcopy_alias_target_b\n", 3)
				copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"copy_alias_absent_one"`, 2), copyFilterSummary(1))
				*notices = (*notices)[:0]
				c.exec(t, "DELETE FROM "+table+" WHERE id<>99")
				over := input + copyFilterAliasRow(format, 4, "copy_alias_absent_two") + copyFilterAliasRow(format, 5, "copy_alias_target_a")
				copyFilterFailure(t, c, query, over, copyFilterAliasDiagnostic(table, "copy_alias_absent_two", 4, "22P02", "skipped more than REJECT_LIMIT (1) rows due to data type incompatibility"))
				check(before, beforeText, 1)
				copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", `"copy_alias_absent_one"`, 2), copyFilterRowNotice(table, "v", `"copy_alias_absent_two"`, 4))
				*notices = (*notices)[:0]
				copyFilterImport(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,LOG_VERBOSITY silent)", over, 3)
				check([][]any{{int64(1), "copy_alias_target_a"}, {int64(3), "copy_alias_target_b"}, {int64(5), "copy_alias_target_a"}, {int64(99), "copy_alias_target_b"}}, "1\tcopy_alias_target_a\n3\tcopy_alias_target_b\n5\tcopy_alias_target_a\n99\tcopy_alias_target_b\n", 4)
				copyFilterNotices(t, *notices)
			})
		}
	})
}
