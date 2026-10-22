package reference_test

import (
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCopyFilterAliasHardErrors(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"copy_alias_actor"})
	fixture.SchemaReferenceCleanup(t, "copy_alias_private")
	copyFilterScenario(t, func(t *testing.T, _ *fixture.Pair, _ fixture.Backend, c copyTextConnection, notices *[]fixture.Diagnostic) {
		for _, format := range []string{"text", "csv"} {
			for _, tc := range []struct {
				name, typ, bad, code, message, position, prefix string
			}{
				{"type_grammar", "regtype", "int4(", "42601", "syntax error at end of input", "6", `invalid type name "int4("`},
				{"type_float_grammar", "regtype", "float(0)", "22023", "precision for type float must be at least 1 bit", "7", `invalid type name "float(0)"`},
				{"type_typmod", "regtype", "varchar(0)", "22023", "length for type varchar must be at least 1", "", ""},
				{"type_typmod_input", "regtype", "numeric('bad')", "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
				{"procedure_typmod", "regprocedure", "abs(numeric(0))", "22023", "NUMERIC precision 0 must be between 1 and 1000", "", ""},
				{"operator_typmod", "regoperator", "+(numeric(-1),integer)", "22023", "NUMERIC precision -1 must be between 1 and 1000", "", ""},
				{"class_cross_database", "regclass", "copy_alias_foreign.public.x", "0A000", `cross-database references are not implemented: "copy_alias_foreign.public.x"`, "", ""},
				{"class_qualified_shape", "regclass", "a.b.c.d", "42601", "improper relation name (too many dotted names): a.b.c.d", "", ""},
			} {
				t.Run(format+"/"+tc.name, func(t *testing.T) {
					*notices = (*notices)[:0]
					table := "copy_alias_hard_" + tc.name
					c.exec(t, "CREATE TABLE "+table+"(id int,v "+tc.typ+")")
					defer c.exec(t, "DROP TABLE "+table)
					c.exec(t, "INSERT INTO "+table+" VALUES(99,'0')")
					want := copyFilterAliasDiagnostic(table, tc.bad, 2, tc.code, tc.message)
					want.Position = tc.position
					if tc.prefix != "" {
						want.Context = tc.prefix + "\n" + want.Context
					}
					input := copyFilterAliasRow(format, 1, "0") + copyFilterAliasRow(format, 2, tc.bad)
					copyFilterFailure(t, c, "COPY "+table+" FROM STDIN WITH(FORMAT "+format+",ON_ERROR ignore,LOG_VERBOSITY verbose)", input, want)
					zero := "-"
					if tc.typ == "regoperator" {
						zero = "0"
					}
					c.output(t, "COPY (SELECT id,v FROM "+table+" ORDER BY id) TO STDOUT", "99\t"+zero+"\n", 1)
					copyFilterNotices(t, *notices)
				})
			}
		}
		c.exec(t, "CREATE TABLE copy_alias_target(id int)")
		t.Run("where_expression", func(t *testing.T) {
			*notices = (*notices)[:0]
			c.exec(t, "CREATE TABLE copy_alias_where(id int,v text)")
			c.exec(t, "INSERT INTO copy_alias_where VALUES(99,'sentinel')")
			copyFilterFailure(t, c, "COPY copy_alias_where FROM STDIN WITH(ON_ERROR ignore,LOG_VERBOSITY verbose) WHERE v::regclass IS NOT NULL", "1\tcopy_alias_target\n2\tcopy_alias_absent\n", fixture.Diagnostic{Code: "42P01", Message: `relation "copy_alias_absent" does not exist`, Context: "COPY copy_alias_where, line 2: \"2\tcopy_alias_absent\""})
			c.selectRows(t, "SELECT id,v FROM copy_alias_where ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
			copyFilterNotices(t, *notices)
		})
		t.Run("domain_check_expression", func(t *testing.T) {
			*notices = (*notices)[:0]
			c.exec(t, "CREATE DOMAIN copy_alias_domain AS text CHECK(VALUE::regclass IS NOT NULL)")
			c.exec(t, "CREATE TABLE copy_alias_domain_rows(id int,v copy_alias_domain)")
			result, err := c.conn.ExecContext(c.ctx, "INSERT INTO copy_alias_domain_rows VALUES(99,'copy_alias_target')")
			if !c.pg {
				// Existing ordinary domain evaluation has no captured regclass
				// input authority. This is not a COPY recovery capability.
				var state interface{ SQLState() string }
				if result != nil || err == nil || err.Error() != "regclass input lacks catalog authority" || errors.As(err, &state) {
					t.Fatalf("domain alias authority boundary: result=%v err=%v", result, err)
				}
				c.selectRows(t, "SELECT id,v FROM copy_alias_domain_rows ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{})
				copyFilterNotices(t, *notices)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			copyFilterFailure(t, c, "COPY copy_alias_domain_rows FROM STDIN WITH(ON_ERROR ignore,LOG_VERBOSITY verbose)", "1\tcopy_alias_target\n2\tcopy_alias_absent\n", copyFilterAliasDiagnostic("copy_alias_domain_rows", "copy_alias_absent", 2, "42P01", `relation "copy_alias_absent" does not exist`))
			c.selectRows(t, "SELECT id,v FROM copy_alias_domain_rows ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "copy_alias_target"}})
			copyFilterNotices(t, *notices)
		})
		t.Run("schema_acl", func(t *testing.T) {
			*notices = (*notices)[:0]
			c.exec(t, "CREATE ROLE copy_alias_actor")
			c.exec(t, "CREATE SCHEMA copy_alias_private")
			c.exec(t, "REVOKE ALL ON SCHEMA copy_alias_private FROM PUBLIC")
			c.exec(t, "CREATE TABLE copy_alias_private.secret(id int)")
			c.exec(t, "CREATE TABLE copy_alias_acl(id int,v regclass)")
			c.exec(t, "INSERT INTO copy_alias_acl VALUES(99,'0')")
			c.exec(t, "GRANT USAGE ON SCHEMA public TO copy_alias_actor")
			c.exec(t, "GRANT INSERT ON copy_alias_acl TO copy_alias_actor")
			c.exec(t, "SET ROLE copy_alias_actor")
			defer c.exec(t, "RESET ROLE")
			copyFilterFailure(t, c, "COPY copy_alias_acl FROM STDIN WITH(ON_ERROR ignore,LOG_VERBOSITY verbose)", "1\t0\n2\tcopy_alias_private.secret\n", copyFilterAliasDiagnostic("copy_alias_acl", "copy_alias_private.secret", 2, "42501", "permission denied for schema copy_alias_private"))
			c.exec(t, "RESET ROLE")
			c.output(t, "COPY (SELECT id,v FROM copy_alias_acl ORDER BY id) TO STDOUT", "99\t-\n", 1)
			copyFilterNotices(t, *notices)
		})
		t.Run("regconfig_existing_capability", func(t *testing.T) {
			*notices = (*notices)[:0]
			c.exec(t, "CREATE TABLE copy_alias_config(id int,v regconfig)")
			c.exec(t, "INSERT INTO copy_alias_config VALUES(99,'0')")
			r, err := c.from("COPY copy_alias_config FROM STDIN WITH(ON_ERROR ignore,LOG_VERBOSITY verbose)", strings.NewReader("1\t0\n2\tcopy_alias_missing_config\n"))
			if c.pg {
				copyTextSuccess(t, r, err, 1)
				c.output(t, "COPY (SELECT id,v FROM copy_alias_config ORDER BY id) TO STDOUT", "1\t-\n99\t-\n", 2)
				copyFilterNotices(t, *notices, copyFilterRowNotice("copy_alias_config", "v", `"copy_alias_missing_config"`, 2), copyFilterSummary(1))
			} else {
				copyTextDiagnostic(t, r, err, copyFilterAliasDiagnostic("copy_alias_config", "copy_alias_missing_config", 2, "0A000", "text search configuration name lookup is not supported"))
				c.output(t, "COPY (SELECT id,v FROM copy_alias_config ORDER BY id) TO STDOUT", "99\t-\n", 1)
				copyFilterNotices(t, *notices)
			}
		})
	})
}
