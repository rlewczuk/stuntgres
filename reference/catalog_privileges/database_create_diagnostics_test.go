package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseCreateDiagnostics(t *testing.T) {
	databaseCreateScenario(t, func(c *databaseCreateCase) {
		t := c.t
		c.run(fixture.Exec("CREATE ROLE dc_actor CREATEDB", 0))
		c.run(fixture.Exec("CREATE ROLE dc_owner", 0))
		c.run(fixture.Exec("CREATE ROLE dc_denied", 0))
		c.create("dc_duplicate", "", false)
		cases := []struct{ suffix, role, code, message, position, hint string }{
			{"", "", "42P04", "database \"dc_duplicate\" already exists", "", ""},
			{"ENCODING DEFAULT ENCODING 'UTF8'", "", "42601", "conflicting or redundant options", "ENCODING", ""},
			{"OWNER DEFAULT OWNER DEFAULT", "", "42601", "conflicting or redundant options", "OWNER", ""},
			{"TEMPLATE DEFAULT TEMPLATE template1", "", "42601", "conflicting or redundant options", "TEMPLATE", ""},
			{"LC_COLLATE 'C' LC_COLLATE DEFAULT", "", "42601", "conflicting or redundant options", "LC_COLLATE", ""},
			{"LC_CTYPE DEFAULT LC_CTYPE 'C'", "", "42601", "conflicting or redundant options", "LC_CTYPE", ""},
			{"bogus 'x'", "", "42601", "option \"bogus\" not recognized", "bogus", ""},
			{"ENCODING 'NO_SUCH_ENCODING'", "dc_denied", "42704", "NO_SUCH_ENCODING is not a valid encoding name", "ENCODING ", ""},
			{"ENCODING 999", "dc_denied", "42704", "999 is not a valid encoding code", "ENCODING", ""},
			{"OWNER dc_missing_role", "dc_denied", "42704", "role \"dc_missing_role\" does not exist", "", ""},
			{"", "dc_denied", "42501", "permission denied to create database", "", ""},
			{"OWNER dc_owner TEMPLATE dc_missing_template", "dc_actor", "42501", "must be able to SET ROLE \"dc_owner\"", "", ""},
			{"TEMPLATE dc_missing_template", "", "3D000", "template database \"dc_missing_template\" does not exist", "", ""},
			{"ENCODING 'LATIN1'", "", "22023", "new encoding (LATIN1) is incompatible with the encoding of the template database (UTF8)", "", "Use the same encoding as in the template database, or use template0 as template."},
			{"LC_COLLATE 'POSIX'", "", "42P04", "database \"dc_duplicate\" already exists", "", ""},
		}
		original := c.scalar("SELECT current_database()")
		for i, tc := range cases {
			t.Run(fmt.Sprintf("case_%02d", i), func(t *testing.T) {
				child := *c
				child.t = t
				c := &child
				query := "CREATE DATABASE /*é*/ dc_duplicate " + tc.suffix
				d := databaseCreateDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				if tc.position != "" {
					d.Position = databaseCreatePosition(query, tc.position, true)
				}
				if tc.role != "" {
					c.run(fixture.Exec("SET ROLE "+tc.role, 0))
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(query, d)
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "execute"
					}
					fixture.RunStep(t, c.p.Ctx, c.conn, c.backend.Recorder, s)
				}
				if tc.role != "" {
					c.run(fixture.Exec("RESET ROLE", 0))
				}
				if got := c.scalar("SELECT current_database()"); got != original {
					t.Fatalf("database changed to %q", got)
				}
				c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname='dc_duplicate'", []string{"datname"}, []string{"NAME"}, [][]any{{"dc_duplicate"}}))
			})
		}
		for _, tc := range []struct{ begin, code, message string }{
			{"BEGIN", "25001", "CREATE DATABASE cannot run inside a transaction block"},
			{"BEGIN READ ONLY", "25006", "cannot execute CREATE DATABASE in a read-only transaction"},
		} {
			c.run(fixture.Exec(tc.begin, 0))
			c.error("CREATE DATABASE dc_block OWNER dc_missing_role ENCODING 'NO_SUCH_ENCODING'", tc.code, tc.message)
			c.error("CREATE DATABASE dc_block", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
			c.run(fixture.Exec("ROLLBACK", 0))
			c.absent("dc_block")
		}
		for _, query := range []string{"CREATE DATABASE dc_batch; SELECT 1", "SELECT 1; CREATE DATABASE dc_batch"} {
			c.error(query, "25001", "CREATE DATABASE cannot run inside a transaction block")
			c.absent("dc_batch")
		}
		c.create("dc_committed", "", false)
		c.error("SELECT 1/0", "22012", "division by zero")
		c.run(fixture.Select("SELECT datname FROM pg_database WHERE datname='dc_committed'", []string{"datname"}, []string{"NAME"}, [][]any{{"dc_committed"}}))
	})
}
