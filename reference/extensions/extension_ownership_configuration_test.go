package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ownershipConfigurationAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"eo_config_actor"})
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		// A real superuser installation followed by demotion gives the actor
		// genuine ordinary members; no unaccent ownerless-template veto masks cfg.
		for _, q := range []string{`CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`, `CREATE SCHEMA eo_other`, `CREATE ROLE eo_config_actor SUPERUSER`, `GRANT USAGE,CREATE ON SCHEMA eo_old,eo_new TO eo_config_actor`, `SET SESSION AUTHORIZATION eo_config_actor`, `CREATE EXTENSION fuzzystrmatch SCHEMA eo_old`, `RESET SESSION AUTHORIZATION`, `ALTER ROLE eo_config_actor NOSUPERUSER`, `CREATE TEXT SEARCH CONFIGURATION eo_old.item (COPY=pg_catalog.simple)`, `SET ROLE eo_config_actor`} {
			run(fixture.Exec(q, 0))
		}
		run(ownershipError(`ALTER EXTENSION fuzzystrmatch ADD TEXT SEARCH CONFIGURATION eo_old.item`, "42501", `must be owner of text search configuration eo_old.item`))
		run(ownershipError(`ALTER EXTENSION fuzzystrmatch ADD TEXT SEARCH CONFIGURATION eo_old.missing`, "42704", `text search configuration "eo_old.missing" does not exist`))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`ALTER EXTENSION fuzzystrmatch ADD TEXT SEARCH CONFIGURATION eo_old.item`, 0))
		id := ownershipID(t, p, c, `SELECT 'eo_old.item'::regconfig::bigint`)
		metadata := ownershipConfigurationSnapshot(t, p, c, id)
		state := `SELECT oid::bigint,cfgnamespace::bigint,cfgowner::bigint,cfgparser::bigint FROM pg_ts_config WHERE oid=` + ownershipNumber(id)
		before := ownershipSnapshot(t, p, c, state)
		run(fixture.Exec(`SET ROLE eo_config_actor`, 0))
		run(ownershipError(`ALTER EXTENSION fuzzystrmatch SET SCHEMA eo_new`, "42501", `must be owner of text search configuration item`))
		ownershipUnchanged(t, p, c, state, before)
		procedures := ownershipSnapshot(t, p, c, ownershipProcedures)
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`ALTER TEXT SEARCH CONFIGURATION eo_old.item OWNER TO eo_config_actor`, 0))
		run(fixture.Exec(`REVOKE CREATE ON SCHEMA eo_new FROM eo_config_actor`, 0))
		run(fixture.Exec(`SET ROLE eo_config_actor`, 0))
		run(ownershipError(`ALTER EXTENSION fuzzystrmatch SET SCHEMA eo_new`, "42501", `permission denied for schema eo_new`))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`GRANT CREATE ON SCHEMA eo_new TO eo_config_actor`, 0))
		run(fixture.Exec(`CREATE TEXT SEARCH CONFIGURATION eo_new.item (COPY=pg_catalog.simple)`, 0))
		run(fixture.Exec(`SET ROLE eo_config_actor`, 0))
		collisionState := ownershipSnapshot(t, p, c, state)
		run(ownershipError(`ALTER EXTENSION fuzzystrmatch SET SCHEMA eo_new`, "42710", `text search configuration "item" already exists in schema "eo_new"`))
		ownershipUnchanged(t, p, c, state, collisionState)
		ownershipUnchanged(t, p, c, ownershipProcedures, procedures)
		run(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch' AND extnamespace='eo_old'::regnamespace`, 1))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`DROP TEXT SEARCH CONFIGURATION eo_new.item`, 0))
		run(fixture.Exec(`SET ROLE eo_config_actor`, 0))
		run(fixture.Exec(`ALTER EXTENSION fuzzystrmatch SET SCHEMA eo_new`, 0))
		run(fixture.Exec(`RESET ROLE`, 0))
		if ownershipID(t, p, c, `SELECT 'eo_new.item'::regconfig::bigint`) != id {
			t.Fatal("authorized relocation replaced configuration")
		}
		// Owner transfer is intentional; parser and all ordered actual mapping IDs
		// must survive both the failed and successful relocation paths unchanged.
		after := ownershipConfigurationSnapshot(t, p, c, id)
		ownershipClassMetadataEqual(t, metadata[1:], after[1:])
		run(fixture.Select(`SELECT r.rolname,c.cfgparser=p.oid AS default_parser FROM pg_ts_config c JOIN pg_roles r ON r.oid=c.cfgowner JOIN pg_ts_parser p ON p.prsname='default' AND p.prsnamespace='pg_catalog'::regnamespace WHERE c.oid='eo_new.item'::regconfig`, []string{"rolname", "default_parser"}, []string{"NAME", "BOOL"}, [][]any{{"eo_config_actor", true}}))
		ownershipClassWitness(run, "configuration", "eo_new")
	})
}
