package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterDiagnostics(t *testing.T) {
	cases := []struct {
		name, query, code, message, positionToken string
		last                                      bool
	}{
		{"missing-rename", "ALTER DATABASE da_missing RENAME TO da_new", "3D000", "database \"da_missing\" does not exist", "", false},
		{"missing-flags", "ALTER DATABASE da_missing IS_TEMPLATE true", "3D000", "database \"da_missing\" does not exist", "", false},
		{"missing-empty", "ALTER DATABASE da_missing", "3D000", "database \"da_missing\" does not exist", "", false},
		{"owner-resolution", "ALTER DATABASE da_missing OWNER TO da_missing_role", "42704", "role \"da_missing_role\" does not exist", "", false},
		{"missing-owner", "ALTER DATABASE da_missing OWNER TO da_next", "3D000", "database \"da_missing\" does not exist", "", false},
		{"duplicate", "ALTER DATABASE da_source IS_TEMPLATE true IS_TEMPLATE true", "42601", "conflicting or redundant options", "IS_TEMPLATE", true},
		{"duplicate-default", "ALTER DATABASE da_source ALLOW_CONNECTIONS DEFAULT ALLOW_CONNECTIONS DEFAULT", "42601", "conflicting or redundant options", "ALLOW_CONNECTIONS", true},
		{"duplicate-limit", "ALTER DATABASE da_source CONNECTION LIMIT 1 CONNECTION LIMIT 1", "42601", "conflicting or redundant options", "CONNECTION", true},
		{"unknown", "ALTER DATABASE da_source bogus 1 IS_TEMPLATE yes", "42601", "option \"bogus\" not recognized", "bogus", false},
		{"quoted-case", "ALTER DATABASE da_source \"IS_TEMPLATE\" true", "42601", "option \"IS_TEMPLATE\" not recognized", "\"IS_TEMPLATE\"", false},
		{"generic-owner", "ALTER DATABASE da_source WITH OWNER da_next", "42601", "option \"owner\" not recognized", "OWNER", false},
		{"generic-template", "ALTER DATABASE da_source TEMPLATE template1", "42601", "option \"template\" not recognized", "TEMPLATE", false},
		{"generic-encoding", "ALTER DATABASE da_source ENCODING UTF8", "42601", "option \"encoding\" not recognized", "ENCODING", false},
		{"fixed-order", "ALTER DATABASE da_source ALLOW_CONNECTIONS yes IS_TEMPLATE no", "42601", "is_template requires a Boolean value", "", false},
		{"values-before-lookup", "ALTER DATABASE da_missing CONNECTION LIMIT -2", "22023", "invalid connection limit: -2", "", false},
		{"mixed-tablespace-first", "ALTER DATABASE da_source TABLESPACE pg_default IS_TEMPLATE true", "0A000", "option \"tablespace\" cannot be specified with other options", "TABLESPACE", false},
		{"mixed-tablespace-last", "ALTER DATABASE da_source IS_TEMPLATE true TABLESPACE pg_default", "0A000", "option \"tablespace\" cannot be specified with other options", "TABLESPACE", false},
		{"duplicate-tablespace", "ALTER DATABASE da_source TABLESPACE pg_default TABLESPACE pg_default", "42601", "conflicting or redundant options", "TABLESPACE", true},
		{"unicode-position", "ALTER DATABASE \"数据库\" ALLOW_CONNECTIONS true ALLOW_CONNECTIONS true", "42601", "conflicting or redundant options", "ALLOW_CONNECTIONS", true},
		{"full-input", "ALTER DATABASE da_source IS_TEMPLATE true; @", "42601", "syntax error at or near \"@\"", "@", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"direct", "prepared", "held"} {
				t.Run(mode, func(t *testing.T) {
					databaseAlterScenario(t, func(c *databaseCreateCase) {
						c.create("da_source", "OWNER da_owner", false)
						oid := databaseAlterSeed(c, "da_source")
						want := databaseCreateDiagnostic(tc.code, tc.message)
						if tc.positionToken != "" {
							want.Position = databaseCreatePosition(tc.query, tc.positionToken, tc.last)
						}
						s := fixture.ErrorDiagnostic(tc.query, want)
						switch mode {
						case "direct":
							c.run(s)
						case "prepared":
							c.run(fixture.Prepared(s))
						case "held":
							// Syntax errors necessarily fail Prepare; semantic errors remain execute-time.
							if tc.name == "full-input" {
								s.ErrorStage = "prepare"
								c.run(fixture.Prepared(s))
								break
							}
							databaseAlterHeld(c, c.conn, tc.query, func(h fixture.HeldStatement) {
								databaseAlterFlags(c, "da_source", false, true, -1)
								databaseAlterRun(c, h, c.backend.Recorder, s)
								databaseAlterRun(c, h, c.backend.Recorder, s)
							})
						}
						databaseAlterFlags(c, "da_source", false, true, -1)
						databaseAlterData(c, "da_source", oid, 42)
						c.absent("da_new")
					})
				})
			}
		})
	}
	databaseAlterScenario(t, func(c *databaseCreateCase) {
		c.create("da_source", "OWNER da_owner", false)
		c.remember("da_new")
		for _, q := range []string{"ALTER DATABASE da_source RENAME TO da_new", "ALTER DATABASE da_source IS_TEMPLATE true", "ALTER DATABASE da_source OWNER TO da_next", "ALTER DATABASE da_source SET TABLESPACE pg_default", "ALTER DATABASE da_source REFRESH COLLATION VERSION"} {
			databaseAlterHeld(c, c.conn, q, func(h fixture.HeldStatement) {
				c.run(fixture.Exec("SET ROLE da_plain", 0))
				databaseAlterRun(c, h, c.backend.Recorder, fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("42501", "must be owner of database da_source")))
				c.run(fixture.Exec("RESET ROLE", 0))
			})
		}
		q := "ALTER DATABASE da_source IS_TEMPLATE true"
		databaseAlterHeld(c, c.conn, q, func(h fixture.HeldStatement) {
			denied := fixture.ErrorDiagnostic(q, databaseCreateDiagnostic("42501", "must be owner of database da_source"))
			c.run(fixture.Exec("SET ROLE da_plain", 0))
			databaseAlterRun(c, h, c.backend.Recorder, denied)
			c.run(fixture.Exec("RESET ROLE", 0))
			c.run(fixture.Exec("GRANT da_owner TO da_plain WITH INHERIT TRUE, SET FALSE", 0))
			c.run(fixture.Exec("SET ROLE da_plain", 0))
			databaseAlterRun(c, h, c.backend.Recorder, fixture.Exec(q, 0))
			c.run(fixture.Exec("RESET ROLE", 0))
			c.run(fixture.Exec("REVOKE da_owner FROM da_plain", 0))
			c.run(fixture.Exec("SET ROLE da_plain", 0))
			databaseAlterRun(c, h, c.backend.Recorder, denied)
			c.run(fixture.Exec("RESET ROLE", 0))
		})
		c.run(fixture.Exec("ALTER DATABASE da_source IS_TEMPLATE false", 0))
		c.run(fixture.Exec("ALTER ROLE da_owner NOCREATEDB", 0))
		c.run(fixture.Exec("SET ROLE da_owner", 0))
		c.error("ALTER DATABASE da_source RENAME TO da_source", "42501", "permission denied to rename database")
		c.error("ALTER DATABASE da_source OWNER TO da_next", "42501", "must be able to SET ROLE \"da_next\"")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("GRANT da_next TO da_owner WITH INHERIT FALSE, SET TRUE", 0))
		c.run(fixture.Exec("SET ROLE da_owner", 0))
		c.error("ALTER DATABASE da_source OWNER TO da_next", "42501", "permission denied to change owner of database")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("GRANT da_owner TO da_inherit WITH INHERIT TRUE, SET FALSE", 0))
		c.run(fixture.Exec("SET ROLE da_inherit", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT 2", 0))
		c.error("ALTER DATABASE da_source RENAME TO da_new", "42501", "permission denied to rename database")
		c.run(fixture.Exec("RESET ROLE", 0))
		c.run(fixture.Exec("ALTER DATABASE da_source CONNECTION LIMIT -1", 0))
		for _, q := range []string{"ALTER DATABASE da_source RENAME TO da_new", "ALTER DATABASE da_source IS_TEMPLATE true", "ALTER DATABASE da_source OWNER TO da_next", "ALTER DATABASE da_source REFRESH COLLATION VERSION"} {
			c.run(fixture.Exec("BEGIN READ ONLY", 0))
			c.error(q, "25006", "cannot execute ALTER DATABASE in a read-only transaction")
			c.run(fixture.Exec("ROLLBACK", 0))
		}
		c.run(fixture.Exec("BEGIN", 0))
		c.error("ALTER DATABASE da_missing IS_TEMPLATE true", "3D000", "database \"da_missing\" does not exist")
		c.error("ALTER DATABASE da_source IS_TEMPLATE true", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
		c.run(fixture.Exec("ROLLBACK", 0))
		databaseAlterFlags(c, "da_source", false, true, -1)
		c.absent("da_new")
	})
}
