package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalSchemaUtilityBoundaries(t *testing.T) {
	type boundaryCase struct {
		name, query, tag string
		setup            []string
		// Ordinary utilities are legal PostgreSQL transactions. The independently
		// retained native admission contract is not a PostgreSQL prohibition.
		nativeExcluded bool
		prohibited     string
		parserMessage  string
		probe          string
	}
	cases := []boundaryCase{
		{name: "CreateTable", query: "CREATE TABLE tx_bound_new(a INT)", tag: "CREATE TABLE", probe: "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'"},
		{name: "CreateSchema", query: "CREATE SCHEMA tx_bound_schema", tag: "CREATE SCHEMA", probe: "SELECT count(*) FROM pg_namespace WHERE nspname='tx_bound_schema'"},
		{name: "RenameTable", query: "ALTER TABLE tx_bound RENAME TO tx_bound_new", tag: "ALTER TABLE", probe: "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'"},
		{name: "Default", query: "ALTER TABLE tx_bound ALTER a SET DEFAULT 11", tag: "ALTER TABLE", probe: "SELECT count(*) FROM pg_attrdef WHERE adrelid='tx_bound'::regclass"},
		{name: "SetSchema", setup: []string{"CREATE SCHEMA tx_bound_dest"}, query: "ALTER TABLE tx_bound SET SCHEMA tx_bound_dest", tag: "ALTER TABLE", probe: "SELECT count(*) FROM pg_class WHERE relnamespace='tx_bound_dest'::regnamespace AND relname='tx_bound'"},
		{name: "TableOwner", query: "ALTER TABLE tx_bound OWNER TO CURRENT_USER", tag: "ALTER TABLE", probe: "SELECT count(*) FROM pg_class WHERE oid='tx_bound'::regclass AND relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)"},
		{name: "Database", query: "CREATE DATABASE tx_bound_database", tag: "CREATE DATABASE", prohibited: "CREATE DATABASE"},
		{name: "Tablespace", query: "CREATE TABLESPACE tx_bound_space LOCATION '/tmp/tx_bound_absent'", tag: "CREATE TABLESPACE", prohibited: "CREATE TABLESPACE"},
		{name: "ConcurrentIndex", query: "CREATE INDEX CONCURRENTLY tx_bound_new ON tx_bound(a)", tag: "CREATE INDEX", prohibited: "CREATE INDEX CONCURRENTLY"},
		{name: "ConcurrentDetach", setup: []string{"CREATE TABLE tx_bound_parent(a INT) PARTITION BY RANGE(a)", "CREATE TABLE tx_bound_child PARTITION OF tx_bound_parent FOR VALUES FROM(0) TO(10)"}, query: "ALTER TABLE tx_bound_parent DETACH PARTITION tx_bound_child CONCURRENTLY", tag: "ALTER TABLE", prohibited: "ALTER TABLE ... DETACH CONCURRENTLY", parserMessage: "DETACH PARTITION CONCURRENTLY is unsupported"},
		// Parsed concurrent DROP is forbidden in a transaction even though its
		// standalone executor retains the feature-owned unsupported boundary.
		{name: "ConcurrentDropIndex", setup: []string{"CREATE INDEX tx_bound_index ON tx_bound(a)"}, query: "DROP INDEX CONCURRENTLY tx_bound_index", tag: "DROP INDEX", prohibited: "DROP INDEX CONCURRENTLY"},
		{name: "Index", query: "CREATE INDEX tx_bound_new ON tx_bound(a)", tag: "CREATE INDEX", probe: "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'"},
		{name: "View", query: "CREATE VIEW tx_bound_new AS SELECT a FROM tx_bound", tag: "CREATE VIEW", nativeExcluded: true, probe: "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'"},
		{name: "Composite", query: "CREATE TYPE tx_bound_new AS(a INT)", tag: "CREATE TYPE", nativeExcluded: true, probe: "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tx_bound_new'"},
		{name: "Domain", query: "CREATE DOMAIN tx_bound_new AS INT", tag: "CREATE DOMAIN", nativeExcluded: true, probe: "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tx_bound_new'"},
		{name: "Routine", query: "CREATE FUNCTION tx_bound_new() RETURNS INT LANGUAGE SQL RETURN 11", tag: "CREATE FUNCTION", nativeExcluded: true, probe: "SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='tx_bound_new'"},
		{name: "Role", query: "CREATE ROLE tx_bound_role NOLOGIN", tag: "CREATE ROLE", nativeExcluded: true, probe: "SELECT count(*) FROM pg_roles WHERE rolname='tx_bound_role'"},
		{name: "Conversion", query: "CREATE CONVERSION tx_bound_new FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", tag: "CREATE CONVERSION", nativeExcluded: true, probe: "SELECT count(*) FROM pg_conversion WHERE connamespace='public'::regnamespace AND conname='tx_bound_new'"},
		{name: "Grant", query: "GRANT SELECT ON tx_bound TO PUBLIC", tag: "GRANT", nativeExcluded: true, probe: "SELECT count(*) FROM pg_class WHERE oid='tx_bound'::regclass AND relacl IS NOT NULL"},
		{name: "Comment", query: "COMMENT ON TABLE tx_bound IS 'private comment'", tag: "COMMENT", probe: "SELECT count(*) FROM pg_description WHERE classoid='pg_class'::regclass AND objoid='tx_bound'::regclass AND objsubid=0"},
		{name: "MaterializedForm", query: "CREATE MATERIALIZED VIEW tx_bound_new AS SELECT a FROM tx_bound WITH NO DATA", tag: "CREATE MATERIALIZED VIEW", nativeExcluded: true, probe: "SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'"},
		{name: "Refresh", setup: []string{"CREATE MATERIALIZED VIEW tx_bound_mat AS SELECT a FROM tx_bound WITH NO DATA"}, query: "REFRESH MATERIALIZED VIEW tx_bound_mat", tag: "REFRESH MATERIALIZED VIEW", nativeExcluded: true, probe: "SELECT count(*) FROM pg_class WHERE oid='tx_bound_mat'::regclass AND relispopulated"},
		{name: "ViewOwner", setup: []string{"CREATE VIEW tx_bound_view AS SELECT a FROM tx_bound"}, query: "ALTER VIEW tx_bound_view OWNER TO CURRENT_USER", tag: "ALTER VIEW", nativeExcluded: true},
		{name: "TypeOwner", setup: []string{"CREATE TYPE tx_bound_type AS(a INT)"}, query: "ALTER TYPE tx_bound_type OWNER TO CURRENT_USER", tag: "ALTER TYPE", nativeExcluded: true},
		{name: "DomainOwner", setup: []string{"CREATE DOMAIN tx_bound_domain AS INT"}, query: "ALTER DOMAIN tx_bound_domain OWNER TO CURRENT_USER", tag: "ALTER DOMAIN", nativeExcluded: true},
		{name: "RoutineOwner", setup: []string{"CREATE FUNCTION tx_bound_function() RETURNS INT LANGUAGE SQL RETURN 11"}, query: "ALTER FUNCTION tx_bound_function() OWNER TO CURRENT_USER", tag: "ALTER FUNCTION", nativeExcluded: true},
	}
	for _, tc := range cases {
		for _, held := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/held=%v/goTx=%v", tc.name, held, goTx), func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"tx_bound_role"})
					fixture.SchemaReferenceCleanup(t, "tx_bound_schema")
					fixture.SchemaReferenceCleanup(t, "tx_bound_dest")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							base(fixture.Exec("CREATE TABLE tx_bound(a INT)", 0))
							base(fixture.Exec("INSERT INTO tx_bound VALUES(7)", 1))
							for _, q := range tc.setup {
								base(fixture.Exec(q, 0))
							}
							// Prepare outside BEGIN for excluded ordinary utilities, so
							// this is genuine held Execute admission rather than Describe.
							var prepared fixture.HeldStatement
							if held && (b.PG || tc.parserMessage == "") {
								stmt, err := c.PrepareContext(p.Ctx, tc.query)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								prepared = fixture.HeldStatement{Statement: stmt, SQL: tc.query}
							}
							var e fixture.Executor = c
							rollback := func() { fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0) }
							if goTx {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								// The held statement belongs to this same pinned Conn.
								// Execute it unchanged; Tx.StmtContext would reprepare a
								// Conn-owned definition and test Describe instead.
								rollback = func() {
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
								}
							} else {
								base(fixture.Exec("BEGIN", 0))
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							statement := e
							if prepared.Statement != nil {
								prepared.Executor = e
								statement = prepared
							}
							s := fixture.Exec(tc.query, 0)
							failed := false
							if tc.prohibited != "" {
								failed = true
								s = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "25001", Message: tc.prohibited + " cannot run inside a transaction block"})
							}
							if !b.PG && tc.nativeExcluded {
								failed = true
								s = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
							}
							if !b.PG && tc.parserMessage != "" {
								s = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: tc.parserMessage, Position: fixture.Position(strings.Index(tc.query, "CONCURRENTLY") + 1)})
								// Unsupported syntax cannot yield a held statement. Assert
								// its actual prepare-stage boundary instead of faking one.
								s.Prepared = held
								if held {
									s.ErrorStage = "prepare"
								}
							}
							if failed && s.ErrorStage == "" {
								s.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, statement, b.Recorder, s)
							if failed {
								run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
							} else if tc.probe != "" {
								run(fixture.Select(tc.probe, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
							}
							rollback()
							base(fixture.Select("SELECT a FROM tx_bound", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tx_bound_new'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							if tc.probe != "" && tc.name != "TableOwner" {
								base(fixture.Select(tc.probe, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							}
							// Independently retain read-only-first execution for admitted
							// and excluded parsed utilities. Parser-only negatives above
							// have no executable AST and keep their syntax boundary.
							if tc.parserMessage == "" {
								ro := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
								ro.ErrorStage = "execute"
								var x fixture.Executor = c
								if held {
									stmt, err := c.PrepareContext(p.Ctx, tc.query)
									if err != nil {
										t.Fatal("read-only Prepare", err)
									}
									defer stmt.Close()
									x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.query}
								}
								base(fixture.Exec("BEGIN READ ONLY", 0))
								defer c.ExecContext(p.Ctx, "ROLLBACK")
								fixture.RunStep(t, p.Ctx, x, b.Recorder, ro)
								base(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
								base(fixture.Exec("ROLLBACK", 0))
								base(fixture.Select("SELECT a FROM tx_bound", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
							}
						})
					}
				})
			}
		}
	}
}
