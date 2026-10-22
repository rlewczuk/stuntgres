package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Quote every CSV alias explicitly, including empty strings. An empty decoded
// type name is not CSV NULL. Text escapes retain the same decoded value.
func copyFilterAliasRow(format string, id int, value string) string {
	if format == "csv" {
		return fmt.Sprintf("%d,\"%s\"\n", id, strings.ReplaceAll(value, "\"", "\"\""))
	}
	value = strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\n", "\\n", "\r", "\\r").Replace(value)
	return fmt.Sprintf("%d\t%s\n", id, value)
}

func copyFilterAliasDiagnostic(table, value string, line int, code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: code, Message: message, Context: fmt.Sprintf("COPY %s, line %d, column v: \"%s\"", table, line, value)}
}

func TestReferenceCopyFilterAliasInputs(t *testing.T) {
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, format := range []string{"text", "csv"} {
			for _, tc := range []struct {
				name, typ, bad, code, message, hint, zero string
			}{
				{"class_missing", "regclass", "copy_alias_absent", "42P01", `relation "copy_alias_absent" does not exist`, "", "-"},
				{"class_missing_schema", "regclass", "copy_alias_no_schema.t", "42P01", `relation "copy_alias_no_schema.t" does not exist`, "", "-"},
				{"class_name", "regclass", "a..b", "42602", "invalid name syntax", "", "-"},
				{"class_oid_overflow", "regclass", "4294967296", "22003", `value "4294967296" is out of range for type oid`, "", "-"},
				{"type_missing", "regtype", "copy_alias_missing_type", "42704", `type "copy_alias_missing_type" does not exist`, "", "-"},
				{"type_missing_schema", "regtype", "copy_alias_no_schema.t", "3F000", `schema "copy_alias_no_schema" does not exist`, "", "-"},
				{"type_empty", "regtype", "", "42601", `invalid type name ""`, "", "-"},
				{"type_setof", "regtype", "SETOF integer", "42601", `invalid type name "SETOF integer"`, "", "-"},
				{"proc_missing", "regproc", "copy_alias_missing_proc", "42883", `function "copy_alias_missing_proc" does not exist`, "", "-"},
				{"proc_ambiguous", "regproc", "abs", "42725", `more than one function named "abs"`, "", "-"},
				{"proc_name", "regproc", "a..b", "42602", "invalid name syntax", "", "-"},
				{"procedure_missing", "regprocedure", "copy_alias_missing_proc(integer)", "42883", `function "copy_alias_missing_proc(integer)" does not exist`, "", "-"},
				{"procedure_syntax", "regprocedure", "abs", "22P02", "expected a left parenthesis", "", "-"},
				{"procedure_type", "regprocedure", "abs(copy_alias_missing_type)", "42704", `type "copy_alias_missing_type" does not exist`, "", "-"},
				{"oper_missing", "regoper", "||//", "42883", "operator does not exist: ||//", "", "0"},
				{"oper_ambiguous", "regoper", "+", "42725", "more than one operator named +", "", "0"},
				{"oper_name", "regoper", "a..+", "42602", "invalid name syntax", "", "0"},
				{"operator_missing", "regoperator", "++(int4,int4)", "42883", "operator does not exist: ++(int4,int4)", "", "0"},
				{"operator_one_arg", "regoperator", "-(integer)", "42P02", "missing argument", "Use NONE to denote the missing argument of a unary operator.", "0"},
				{"operator_many_args", "regoperator", "+()", "54023", "too many arguments", "Provide two argument types for operator.", "0"},
				{"operator_type", "regoperator", "+(copy_alias_missing_type,integer)", "42704", `type "copy_alias_missing_type" does not exist`, "", "0"},
				{"role_missing", "regrole", "copy_alias_missing_role", "42704", `role "copy_alias_missing_role" does not exist`, "", "-"},
				{"role_name", "regrole", "a.b", "42602", "invalid name syntax", "", "-"},
				{"namespace_missing", "regnamespace", "copy_alias_missing_namespace", "3F000", `schema "copy_alias_missing_namespace" does not exist`, "", "-"},
				{"namespace_name", "regnamespace", "a.b", "42602", "invalid name syntax", "", "-"},
				{"collation_missing", "regcollation", "copy_alias_missing_collation", "42704", `collation "copy_alias_missing_collation" for encoding "UTF8" does not exist`, "", "-"},
				{"collation_name", "regcollation", "a..b", "42602", "invalid name syntax", "", "-"},
				{"dictionary_missing", "regdictionary", "copy_alias_missing_dictionary", "42704", `text search dictionary "copy_alias_missing_dictionary" does not exist`, "", "-"},
				{"dictionary_name", "regdictionary", "a..b", "42602", "invalid name syntax", "", "-"},
				{"config_name", "regconfig", "a..b", "42602", "invalid name syntax", "", "-"},
			} {
				t.Run(format+"/"+tc.name, func(t *testing.T) {
					table := "copy_alias_" + tc.name
					c.exec(t, "CREATE TABLE "+table+"(id int,v "+tc.typ+")")
					defer c.exec(t, "DROP TABLE "+table)
					c.exec(t, "INSERT INTO "+table+" VALUES(99,'0')")
					input := copyFilterAliasRow(format, 1, "0") + copyFilterAliasRow(format, 2, tc.bad) + copyFilterAliasRow(format, 3, "0")
					for _, stop := range []string{"", ",ON_ERROR stop"} {
						want := copyFilterAliasDiagnostic(table, tc.bad, 2, tc.code, tc.message)
						want.Hint = tc.hint
						copyFilterFailure(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+stop+")", input, want)
						c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", "99\t"+tc.zero+"\n", 1)
						copyFilterNotices(t, *notices)
					}
					copyFilterImport(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,LOG_VERBOSITY verbose)", input, 2)
					copyFilterIDs(t, c, table, 1, 3, 99)
					c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", "1\t"+tc.zero+"\n3\t"+tc.zero+"\n99\t"+tc.zero+"\n", 3)
					copyFilterNotices(t, *notices, copyFilterRowNotice(table, "v", "\""+tc.bad+"\"", 2), copyFilterSummary(1))
					*notices = (*notices)[:0]
				})
			}
		}
	})
}
