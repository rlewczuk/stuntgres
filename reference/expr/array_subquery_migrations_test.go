package reference_test

import (
	"database/sql"
	_ "embed"
	"testing"

	"stuntgres/reference/fixture"
)

// Verbatim copies of grind/coder/coder/coderd/database/migrations/
// 000595_remove_task_permissions.up.sql and
// 000616_remove_chat_automation_permissions.up.sql (coder base
// b798061108ed37e97f6be43ff7877e89c5dcbbb7).
//
//go:embed testdata/000595_remove_task_permissions.up.sql
var exprArrayMigration595 string

//go:embed testdata/000616_remove_chat_automation_permissions.up.sql
var exprArrayMigration616 string

// TestReferenceExprArraySubqueryMigrations applies both coder migrations as
// single whole-file transactions, exactly as golang-migrate's pgTxnDriver
// sends them. Only objects referenced by these migrations are created, and the
// final rows/enums are the pinned PostgreSQL 18.6 outcomes from
// .cswd/tmp/GRIND-EXPR-ARRAY-SUBQUERY/oracle-matrix.md.
func TestReferenceExprArraySubqueryMigrations(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := arraySubqueryRun(t, p, b, c)
		apply := func(name, script string) {
			t.Helper()
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatalf("%s begin: %v", name, err)
			}
			defer func() {
				_ = tx.Rollback()
			}()
			if _, err := tx.ExecContext(p.Ctx, script); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("%s commit: %v", name, err)
			}
		}

		for _, statement := range []string{
			"CREATE TYPE api_key_scope AS ENUM ('coder:all','task:read','workspace:read','chat_automation:read')",
			"CREATE TABLE custom_roles (name text PRIMARY KEY, site_permissions jsonb NOT NULL DEFAULT '[]', org_permissions jsonb NOT NULL DEFAULT '[]', user_permissions jsonb NOT NULL DEFAULT '[]', member_permissions jsonb NOT NULL DEFAULT '[]')",
			"CREATE TABLE api_keys (id text PRIMARY KEY, scopes api_key_scope[] NOT NULL, allow_list text[] NOT NULL)",
			"CREATE TABLE oauth2_provider_apps (id text PRIMARY KEY, scope text NOT NULL DEFAULT '')",
			"CREATE TABLE oauth2_provider_app_codes (id text PRIMARY KEY, scope text NOT NULL DEFAULT '')",
			"CREATE TABLE oauth2_provider_app_tokens (id text PRIMARY KEY, scope text NOT NULL DEFAULT '')",
			"CREATE TABLE site_configs (key varchar(256) NOT NULL UNIQUE, value varchar(8192) NOT NULL)",
		} {
			run(fixture.Exec(statement, 0))
		}
		run(fixture.Exec(`INSERT INTO custom_roles(name,site_permissions) VALUES ('r1','[{"resource_type":"task"},{"resource_type":"workspace"}]')`, 1))
		run(fixture.Exec(`INSERT INTO api_keys(id,scopes,allow_list) VALUES ('k1',ARRAY['task:read','workspace:read']::api_key_scope[],ARRAY['task:foo','other:bar']), ('k2',ARRAY['task:read']::api_key_scope[],ARRAY['task:foo'])`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_app_codes(id,scope) VALUES ('c1','task:read other:write'),('c2','task:read')`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_app_tokens(id,scope) VALUES ('t1','task:read other:write'),('t2','task:read')`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_apps(id,scope) VALUES ('a1','task:read other:write'),('a2','task:read')`, 2))

		apply("000595_remove_task_permissions", exprArrayMigration595)
		run(fixture.ArrayReferenceSelect("SELECT id, scopes::text[], allow_list FROM api_keys ORDER BY id", []string{"id", "scopes", "allow_list"}, []string{"TEXT", "_TEXT", "_TEXT"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{"k1", "{workspace:read}", "{other:bar}"}}, b.PG))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_app_codes ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"c1", "other:write"}}))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_app_tokens ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"t1", "other:write"}}))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_apps ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"a1", "other:write"}, {"a2", " "}}))
		run(fixture.Select("SELECT name, site_permissions::text AS site_permissions FROM custom_roles ORDER BY name", []string{"name", "site_permissions"}, []string{"TEXT", "TEXT"}, [][]any{{"r1", `[{"resource_type": "workspace"}]`}}))
		run(fixture.Select("SELECT count(*) FROM pg_enum WHERE enumtypid='api_key_scope'::regtype::oid AND enumlabel LIKE 'task:%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname='api_key_scope_old'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT 'coder:all'::api_key_scope::text AS label", []string{"label"}, []string{"TEXT"}, [][]any{{"coder:all"}}))

		run(fixture.Exec(`INSERT INTO custom_roles(name,site_permissions) VALUES ('r2','[{"resource_type":"chat_automation"},{"resource_type":"workspace"}]')`, 1))
		run(fixture.Exec(`INSERT INTO api_keys(id,scopes,allow_list) VALUES ('k3',ARRAY['workspace:read']::api_key_scope[],ARRAY['chat_automation:foo','other:bar']), ('k4',ARRAY['workspace:read']::api_key_scope[],ARRAY['chat_automation:foo'])`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_app_codes(id,scope) VALUES ('c3','chat_automation:read other:write'),('c4','chat_automation:read')`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_app_tokens(id,scope) VALUES ('t3','chat_automation:read other:write'),('t4','chat_automation:read')`, 2))
		run(fixture.Exec(`INSERT INTO oauth2_provider_apps(id,scope) VALUES ('a3','chat_automation:read other:write'),('a4','chat_automation:read')`, 2))
		run(fixture.Exec(`INSERT INTO site_configs(key,value) VALUES ('experiment_rule:chat-automations','x')`, 1))

		apply("000616_remove_chat_automation_permissions", exprArrayMigration616)
		run(fixture.ArrayReferenceSelect("SELECT id, scopes::text[], allow_list FROM api_keys ORDER BY id", []string{"id", "scopes", "allow_list"}, []string{"TEXT", "_TEXT", "_TEXT"}, []string{"TEXT", "_TEXT", "_TEXT"}, [][]any{{"k1", "{workspace:read}", "{other:bar}"}, {"k3", "{workspace:read}", "{other:bar}"}}, b.PG))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_app_codes ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"c1", "other:write"}, {"c3", "other:write"}}))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_app_tokens ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"t1", "other:write"}, {"t3", "other:write"}}))
		run(fixture.Select("SELECT id, scope FROM oauth2_provider_apps ORDER BY id", []string{"id", "scope"}, []string{"TEXT", "TEXT"}, [][]any{{"a1", "other:write"}, {"a2", " "}, {"a3", "other:write"}, {"a4", " "}}))
		run(fixture.Select("SELECT name, site_permissions::text AS site_permissions FROM custom_roles ORDER BY name", []string{"name", "site_permissions"}, []string{"TEXT", "TEXT"}, [][]any{{"r1", `[{"resource_type": "workspace"}]`}, {"r2", `[{"resource_type": "workspace"}]`}}))
		run(fixture.Select("SELECT count(*) FROM site_configs", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_enum WHERE enumtypid='api_key_scope'::regtype::oid AND enumlabel LIKE 'task:%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_enum WHERE enumtypid='api_key_scope'::regtype::oid AND enumlabel LIKE 'chat_automation:%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname='api_key_scope_old'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT 'coder:all'::api_key_scope::text AS label", []string{"label"}, []string{"TEXT"}, [][]any{{"coder:all"}}))
	})
}
