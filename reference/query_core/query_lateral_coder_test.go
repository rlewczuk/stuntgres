package reference_test

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Verbatim full upstream files from coderd/database/migrations in
// grind/coder/coder, base b798061108ed37e97f6be43ff7877e89c5dcbbb7.
// No external checkout is consulted at test runtime.
//
//go:embed testdata/coder_lateral/*.up.sql
var coderLateralMigrations embed.FS

func TestReferenceQueryLateralCoderMigrations(t *testing.T) {
	for _, schema := range []string{"coder_lateral_323", "coder_lateral_views", "coder_lateral_387"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, family := range []struct {
			name string
			body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)
		}{
			{"323", coderLateralLatestBuilds},
			{"views", coderLateralTaskViews},
			{"387", coderLateralTaskWorkspaces},
		} {
			t.Run(family.name, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				schema := "coder_lateral_" + family.name
				run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				run(fixture.Exec("SET search_path TO "+schema+", public", 0))
				t.Cleanup(func() {
					if _, err := c.ExecContext(p.Ctx, "SET search_path TO public"); err != nil {
						t.Error(err)
					}
					if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
						t.Error(err)
					}
				})
				family.body(t, p, b, c)
			})
		}
	})
}

func coderID(n int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", n) }

// The selected files contain ordinary/E strings, quoted identifiers, line and
// block comments. Preserve their bytes, framing only on unquoted semicolons;
// in particular 000400's E-string and apostrophes in comments are not delimiters.
func coderMigrationStatements(t *testing.T, text string) []string {
	t.Helper()
	var out []string
	start, block := 0, 0
	var quote byte
	line, escape := false, false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if line {
			if ch == '\n' {
				line = false
			}
			continue
		}
		if block > 0 {
			if i+1 < len(text) && text[i:i+2] == "/*" {
				block++
				i++
			} else if i+1 < len(text) && text[i:i+2] == "*/" {
				block--
				i++
			}
			continue
		}
		if quote != 0 {
			if escape && ch == '\\' {
				i++
				continue
			}
			if ch == quote {
				if i+1 < len(text) && text[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if i+1 < len(text) && text[i:i+2] == "--" {
			line = true
			i++
			continue
		}
		if i+1 < len(text) && text[i:i+2] == "/*" {
			block = 1
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			escape = ch == '\'' && i > 0 && (text[i-1] == 'E' || text[i-1] == 'e')
			continue
		}
		if ch == ';' {
			out = append(out, text[start:i+1])
			start = i + 1
		}
	}
	if quote != 0 || block != 0 {
		t.Fatal("unterminated migration string/comment")
	}
	if strings.TrimSpace(text[start:]) != "" {
		out = append(out, text[start:])
	}
	return out
}

func coderApplyMigration(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) {
	t.Helper()
	text, err := coderLateralMigrations.ReadFile("testdata/coder_lateral/" + name + ".up.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i, statement := range coderMigrationStatements(t, string(text)) {
		if _, err := tx.ExecContext(p.Ctx, statement); err != nil {
			t.Fatalf("%s statement %d: %v", name, i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Minimal projections of real prerequisites: 000003/000004 (UUID keys,
// workspace_transition/build_number), 000052/000091/000104/000160 (enums),
// 000089 (parameters), 000140 (deleting_at), 000330 (visible_users),
// 000337/000342 (AI fields), 000354 (ACLs), and 000366 (task model).
// Unrelated required columns are omitted, not replaced by fake migration DDL.
func coderLateralSchema(run func(fixture.Step)) {
	for _, q := range []string{
		`CREATE TYPE workspace_transition AS ENUM ('start','stop','delete')`,
		`CREATE TYPE provisioner_job_status AS ENUM ('pending','running','succeeded','canceling','canceled','failed','unknown')`,
		`CREATE TYPE workspace_agent_lifecycle_state AS ENUM ('created','starting','start_timeout','start_error','ready','shutting_down','shutdown_timeout','shutdown_error','off')`,
		`CREATE TABLE organizations(id uuid PRIMARY KEY)`,
		`CREATE TABLE users(id uuid PRIMARY KEY, username text NOT NULL DEFAULT '', name text NOT NULL DEFAULT '', avatar_url text NOT NULL DEFAULT '')`,
		`CREATE TABLE templates(id uuid PRIMARY KEY)`,
		`CREATE TABLE template_versions(id uuid PRIMARY KEY)`,
		`CREATE TABLE template_version_presets(id uuid PRIMARY KEY)`,
		`CREATE TABLE workspaces(id uuid PRIMARY KEY, owner_id uuid NOT NULL REFERENCES users(id), organization_id uuid NOT NULL REFERENCES organizations(id), template_id uuid NOT NULL REFERENCES templates(id), name varchar(64) NOT NULL, created_at timestamptz NOT NULL, deleted boolean NOT NULL DEFAULT false, deleting_at timestamptz)`,
		`CREATE TABLE provisioner_jobs(id uuid PRIMARY KEY, job_status provisioner_job_status NOT NULL)`,
		`CREATE TABLE workspace_builds(id uuid PRIMARY KEY, workspace_id uuid NOT NULL REFERENCES workspaces(id), template_version_id uuid NOT NULL REFERENCES template_versions(id), job_id uuid NOT NULL UNIQUE REFERENCES provisioner_jobs(id), template_version_preset_id uuid REFERENCES template_version_presets(id), transition workspace_transition NOT NULL, created_at timestamptz NOT NULL, build_number integer NOT NULL, UNIQUE(workspace_id,build_number))`,
		`CREATE TABLE workspace_resources(id uuid PRIMARY KEY, job_id uuid NOT NULL REFERENCES provisioner_jobs(id))`,
		`CREATE TABLE workspace_agents(id uuid PRIMARY KEY, resource_id uuid NOT NULL REFERENCES workspace_resources(id), lifecycle_state workspace_agent_lifecycle_state NOT NULL DEFAULT 'created')`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec(`INSERT INTO organizations VALUES($1)`, 1, coderID(1)))
	run(fixture.Exec(`INSERT INTO users VALUES($1,'alice','Alice Example','https://example.test/alice.png'),('c42fdf75-3097-471c-8c33-fb52454d81c0','prebuilds','Prebuilds','')`, 2, coderID(2)))
	run(fixture.Exec(`INSERT INTO templates VALUES($1)`, 1, coderID(3)))
	run(fixture.Exec(`INSERT INTO template_versions VALUES($1),($2)`, 2, coderID(4), coderID(5)))
	run(fixture.Exec(`INSERT INTO template_version_presets VALUES($1)`, 1, coderID(6)))
}

func coderLateralTaskSchema(run func(fixture.Step)) {
	coderLateralSchema(run)
	for _, q := range []string{
		`CREATE TYPE workspace_app_health AS ENUM ('disabled','initializing','healthy','unhealthy')`,
		`CREATE VIEW visible_users AS SELECT id,username,name,avatar_url FROM users`,
		`ALTER TABLE workspaces ADD COLUMN group_acl jsonb NOT NULL DEFAULT '{}', ADD COLUMN user_acl jsonb NOT NULL DEFAULT '{}'`,
		`CREATE TABLE workspace_apps(id uuid PRIMARY KEY, agent_id uuid NOT NULL REFERENCES workspace_agents(id), health workspace_app_health NOT NULL DEFAULT 'disabled')`,
		`ALTER TABLE workspace_builds ADD COLUMN has_ai_task boolean, ADD COLUMN ai_task_sidebar_app_id uuid REFERENCES workspace_apps(id)`,
		`ALTER TABLE workspace_builds ADD CONSTRAINT workspace_builds_ai_task_sidebar_app_id_required CHECK (((has_ai_task IS NULL OR has_ai_task=false) AND ai_task_sidebar_app_id IS NULL) OR (has_ai_task=true AND ai_task_sidebar_app_id IS NOT NULL))`,
		`CREATE TABLE workspace_build_parameters(workspace_build_id uuid NOT NULL REFERENCES workspace_builds(id), name text NOT NULL, value text NOT NULL, UNIQUE(workspace_build_id,name))`,
		`CREATE TABLE tasks(id uuid PRIMARY KEY, organization_id uuid NOT NULL REFERENCES organizations(id), owner_id uuid NOT NULL REFERENCES users(id), name text NOT NULL, workspace_id uuid REFERENCES workspaces(id), template_version_id uuid NOT NULL REFERENCES template_versions(id), template_parameters jsonb NOT NULL DEFAULT '{}', prompt text NOT NULL, created_at timestamptz NOT NULL, deleted_at timestamptz)`,
		`CREATE TABLE task_workspace_apps(task_id uuid NOT NULL REFERENCES tasks(id), workspace_build_id uuid NOT NULL REFERENCES workspace_builds(id), workspace_agent_id uuid NOT NULL REFERENCES workspace_agents(id), workspace_app_id uuid NOT NULL REFERENCES workspace_apps(id))`,
	} {
		run(fixture.Exec(q, 0))
	}
}

func coderWorkspace(run func(fixture.Step), id int, name string, deleted bool) {
	run(fixture.Exec(`INSERT INTO workspaces(id,owner_id,organization_id,template_id,name,created_at,deleted) VALUES($1,$2,$3,$4,$5,'2025-01-01T00:00:00Z',$6)`, 1, coderID(id), coderID(2), coderID(1), coderID(3), name, deleted))
}

func coderBuild(run func(fixture.Step), id, workspace, number int, transition, status string) {
	run(fixture.Exec(`INSERT INTO provisioner_jobs VALUES($1,$2)`, 1, coderID(id+1000), status))
	run(fixture.Exec(`INSERT INTO workspace_builds(id,workspace_id,template_version_id,job_id,transition,created_at,build_number) VALUES($1,$2,$3,$4,$5,'2025-01-02T00:00:00Z',$6)`, 1, coderID(id), coderID(workspace), coderID(4), coderID(id+1000), transition, number))
}

func coderAgentApp(run func(fixture.Step), build, agent, app int, health string) {
	run(fixture.Exec(`INSERT INTO workspace_resources VALUES($1,$2)`, 1, coderID(agent+1000), coderID(build+1000)))
	run(fixture.Exec(`INSERT INTO workspace_agents VALUES($1,$2,'ready')`, 1, coderID(agent), coderID(agent+1000)))
	run(fixture.Exec(`INSERT INTO workspace_apps VALUES($1,$2,$3)`, 1, coderID(app), coderID(agent), health))
}
