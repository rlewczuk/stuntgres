package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseAlterTablespaceOptionBlocks(t *testing.T) {
	cases := []struct {
		name, options, code, message, token string
		last                                bool
	}{
		{"mixed-first", "TABLESPACE pg_default IS_TEMPLATE yes", "0A000", `option "tablespace" cannot be specified with other options`, "TABLESPACE", false},
		{"mixed-last", "IS_TEMPLATE true TABLESPACE pg_default", "0A000", `option "tablespace" cannot be specified with other options`, "TABLESPACE", false},
		{"duplicate-before-unknown", "TABLESPACE pg_default TABLESPACE pg_default bogus 1", "42601", "conflicting or redundant options", "TABLESPACE", true},
		{"unknown-before-duplicate", "bogus 1 TABLESPACE pg_default TABLESPACE pg_default", "42601", `option "bogus" not recognized`, "bogus", false},
		{"unknown-after-tablespace", "TABLESPACE pg_default bogus 1", "42601", `option "bogus" not recognized`, "bogus", false},
		{"other-duplicate-before-mixed", "TABLESPACE pg_default IS_TEMPLATE true IS_TEMPLATE true", "42601", "conflicting or redundant options", "IS_TEMPLATE", true},
		{"valid-general", "TABLESPACE pg_default", "25001", "ALTER DATABASE SET TABLESPACE cannot run inside a transaction block", "", false},
		{"valid-dedicated", "SET TABLESPACE pg_default", "25001", "ALTER DATABASE SET TABLESPACE cannot run inside a transaction block", "", false},
	}
	for _, block := range []string{"sql", "go", "implicit"} {
		for _, mode := range []string{"direct", "prepared", "held"} {
			// PostgreSQL's extended protocol cannot prepare a statement batch.
			// The same sole utility is prepared in both explicit block routes.
			if block == "implicit" && mode != "direct" {
				continue
			}
			t.Run(block+"/"+mode, func(t *testing.T) {
				databaseAlterScenario(t, func(c *databaseCreateCase) {
					c.create("da_source", "OWNER da_owner", false)
					oid := databaseAlterSeed(c, "da_source")
					c.run(fixture.Exec("CREATE TABLE da_block_marker(v integer)", 0))
					// The fixture recreates public without ordinary-role USAGE.
					// Admit the marker DML explicitly so it cannot mask ALTER.
					c.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO da_plain", 0))
					c.run(fixture.Exec("GRANT SELECT, INSERT ON da_block_marker TO da_plain", 0))
					c.run(fixture.Exec("SET ROLE da_plain", 0))
					c.run(fixture.Select("SELECT count(*) AS n FROM da_block_marker", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					for _, tc := range cases {
						for _, target := range []string{"da_source", "da_missing"} {
							c.t.Run(tc.name+"/"+target, func(t *testing.T) {
								query := "/* 数据库 */ ALTER DATABASE " + target + " " + tc.options
								if block == "implicit" {
									query = "INSERT INTO da_block_marker VALUES (1); " + query + "; INSERT INTO da_block_marker VALUES (2)"
								}
								want := databaseCreateDiagnostic(tc.code, tc.message)
								if tc.token != "" {
									want.Position = databaseCreatePosition(query, tc.token, tc.last)
								}
								defer c.conn.ExecContext(c.p.Ctx, "ROLLBACK")
								var held *sql.Stmt
								if mode == "held" {
									var err error
									held, err = c.conn.PrepareContext(c.p.Ctx, query)
									if err != nil {
										t.Fatal("effect-free Prepare", err)
									}
									defer held.Close()
								}
								for range 2 {
									var executor fixture.Executor = c.conn
									var tx *sql.Tx
									if block == "sql" {
										c.run(fixture.Exec("BEGIN", 0))
									} else if block == "go" {
										var err error
										tx, err = c.conn.BeginTx(c.p.Ctx, nil)
										if err != nil {
											t.Fatal(err)
										}
										defer tx.Rollback()
										executor = tx
									}
									run := func(s fixture.Step) {
										fixture.RunStep(t, c.p.Ctx, executor, c.backend.Recorder, s)
									}
									if block != "implicit" {
										run(fixture.Exec("SAVEPOINT da_option_block", 0))
									}
									s := fixture.ErrorDiagnostic(query, want)
									s.ErrorStage = "execute"
									if mode == "prepared" {
										s = fixture.Prepared(s)
									} else if held != nil {
										executor = fixture.HeldStatement{Executor: executor, Statement: held, SQL: query}
									}
									run(s)
									if block != "implicit" {
										run(fixture.ErrorDiagnostic("SELECT 1", databaseCreateDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
										run(fixture.Exec("ROLLBACK TO SAVEPOINT da_option_block", 0))
									}
									run(fixture.Select("SELECT 42 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(42)}}))
									if tx != nil {
										if err := tx.Rollback(); err != nil {
											t.Fatal(err)
										}
									} else if block == "sql" {
										c.run(fixture.Exec("ROLLBACK", 0))
									}
								}
								// Read-only and already-aborted admission still precede even
								// malformed option lists, on the same retained physical owner.
								if block != "implicit" {
									c.run(fixture.Exec("BEGIN READ ONLY", 0))
									s := fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("25006", "cannot execute ALTER DATABASE in a read-only transaction"))
									s.ErrorStage = "execute"
									if mode == "prepared" {
										s = fixture.Prepared(s)
									}
									var executor fixture.Executor = c.conn
									if held != nil {
										executor = fixture.HeldStatement{Executor: c.conn, Statement: held, SQL: query}
									}
									fixture.RunStep(t, c.p.Ctx, executor, c.backend.Recorder, s)
									s = fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
									s.ErrorStage = "execute"
									// Reuse a held definition; a fresh Prepare in an aborted
									// PostgreSQL transaction is itself rejected at Parse.
									fixture.RunStep(t, c.p.Ctx, executor, c.backend.Recorder, s)
									c.run(fixture.Exec("ROLLBACK", 0))
								}
							})
						}
					}
					c.run(fixture.Exec("RESET ROLE", 0))
					databaseAlterFlags(c, "da_source", false, true, -1)
					c.run(fixture.Select("SELECT pg_get_userbyid(datdba) AS owner,datacl::text AS acl FROM pg_database WHERE datname='da_source'", []string{"owner", "acl"}, []string{"NAME", "TEXT"}, [][]any{{"da_owner", nil}}))
					c.absent("da_missing")
					c.run(fixture.Select("SELECT count(*) AS n FROM da_block_marker", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					databaseAlterData(c, "da_source", oid, 42)
				})
			})
		}
	}
}
