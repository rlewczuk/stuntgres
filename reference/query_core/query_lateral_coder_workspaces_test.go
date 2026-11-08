package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func coderLateralTaskWorkspaces(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := subqueryRun(t, p, b, c)
	coderLateralTaskSchema(run)
	// At 000387, 000379 has already replaced the build UUID with its number.
	// These are the actual prerequisite column/constraint changes, not a
	// replacement for either INSERT in the migration being exercised.
	run(fixture.Exec(`ALTER TABLE task_workspace_apps DROP COLUMN workspace_build_id, ADD COLUMN workspace_build_number integer NOT NULL, ALTER COLUMN workspace_agent_id DROP NOT NULL, ALTER COLUMN workspace_app_id DROP NOT NULL, ADD CONSTRAINT task_workspace_apps_pkey PRIMARY KEY(task_id,workspace_build_number)`, 0))
	coderWorkspace(run, 10, "eligible", false)
	coderWorkspace(run, 11, "deleted", true)
	coderWorkspace(run, 12, "existing", false)
	run(fixture.Exec(`UPDATE workspaces SET deleting_at='2025-01-04T00:00:00Z' WHERE deleted`, 1))
	coderBuild(run, 100, 10, 1, "start", "succeeded")
	coderBuild(run, 101, 10, 2, "stop", "succeeded")
	coderBuild(run, 102, 10, 3, "start", "succeeded")
	coderBuild(run, 103, 11, 1, "start", "succeeded")
	coderBuild(run, 104, 12, 1, "start", "succeeded")
	for _, build := range []int{100, 102, 103, 104} {
		coderAgentApp(run, build, build+100, build+200, "healthy")
		run(fixture.Exec(`UPDATE workspace_builds SET has_ai_task=true,ai_task_sidebar_app_id=$1 WHERE id=$2`, 1, coderID(build+200), coderID(build)))
	}
	run(fixture.Exec(`UPDATE workspace_builds SET template_version_id=$1 WHERE id=$2`, 1, coderID(5), coderID(102)))
	run(fixture.Exec(`INSERT INTO workspace_build_parameters VALUES($1,'AI Prompt','do work'),($1,'region','west'),($2,'AI Prompt','')`, 3, coderID(100), coderID(102)))
	run(fixture.Exec(`INSERT INTO tasks(id,organization_id,owner_id,name,workspace_id,template_version_id,template_parameters,prompt,created_at) VALUES($1,$2,$3,'already a task',$4,$5,'{"preserved":"yes"}','keep me','2024-12-01T00:00:00Z')`, 1, coderID(400), coderID(1), coderID(2), coderID(12), coderID(4)))
	run(fixture.Exec(`INSERT INTO task_workspace_apps(task_id,workspace_build_number,workspace_agent_id,workspace_app_id) VALUES($1,1,$2,$3)`, 1, coderID(400), coderID(204), coderID(304)))

	coderApplyMigration(t, p, c, "000387_migrate_task_workspaces")
	coderLateralMigratedTaskAssertions(run)
	// Save actual generated identity relationally, without choosing a UUID golden.
	// This also catches a rerun that deletes/recreates a logically similar task.
	run(fixture.Exec(`CREATE TABLE first_migrated_identity AS SELECT id,workspace_id FROM tasks WHERE workspace_id=$1`, 1, coderID(10)))
	coderApplyMigration(t, p, c, "000387_migrate_task_workspaces")
	coderLateralMigratedTaskAssertions(run)
	subqueryBoth(run, fixture.Select(`SELECT count(*) FROM first_migrated_identity first JOIN tasks t ON t.id=first.id AND t.workspace_id=first.workspace_id JOIN task_workspace_apps app ON app.task_id=t.id WHERE app.workspace_build_number=3`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func coderLateralMigratedTaskAssertions(run func(fixture.Step)) {
	subqueryBoth(run, fixture.Select(`SELECT (SELECT count(*) FROM tasks) AS tasks,(SELECT count(*) FROM task_workspace_apps) AS apps,(SELECT count(*) FROM tasks WHERE workspace_id=$1) AS deleted`, []string{"tasks", "apps", "deleted"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(2), int64(0)}}, coderID(11)))
	subqueryBoth(run, fixture.Select(`SELECT organization_id,owner_id,name,workspace_id,template_version_id,prompt,template_parameters='{"region":"west"}'::jsonb AS parameters,created_at='2025-01-01T00:00:00Z'::timestamptz AS created,deleted_at FROM tasks WHERE workspace_id=$1`,
		[]string{"organization_id", "owner_id", "name", "workspace_id", "template_version_id", "prompt", "parameters", "created", "deleted_at"}, []string{"UUID", "UUID", "TEXT", "UUID", "UUID", "TEXT", "BOOL", "BOOL", "TIMESTAMPTZ"},
		[][]any{{coderID(1), coderID(2), "eligible", coderID(10), coderID(5), "do work", true, true, nil}}, coderID(10)))
	subqueryBoth(run, fixture.Select(`SELECT id::text ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$' AS valid_uuid,id<>$2::uuid AS new_identity FROM tasks WHERE workspace_id=$1`, []string{"valid_uuid", "new_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, coderID(10), coderID(400)))
	subqueryBoth(run, fixture.Select(`SELECT app.workspace_build_number,app.workspace_agent_id,app.workspace_app_id,app.workspace_agent_id=wa.agent_id AS same_agent,wb.id AS build_id FROM tasks t JOIN task_workspace_apps app ON app.task_id=t.id JOIN workspace_builds wb ON wb.workspace_id=t.workspace_id AND wb.build_number=app.workspace_build_number JOIN workspace_apps wa ON wa.id=app.workspace_app_id WHERE t.workspace_id=$1`,
		[]string{"workspace_build_number", "workspace_agent_id", "workspace_app_id", "same_agent", "build_id"}, []string{"INT4", "UUID", "UUID", "BOOL", "UUID"}, [][]any{{int64(3), coderID(202), coderID(302), true, coderID(102)}}, coderID(10)))
	subqueryBoth(run, fixture.Select(`SELECT id,name,prompt,template_parameters='{"preserved":"yes"}'::jsonb AS parameters,template_version_id FROM tasks WHERE workspace_id=$1`, []string{"id", "name", "prompt", "parameters", "template_version_id"}, []string{"UUID", "TEXT", "TEXT", "BOOL", "UUID"}, [][]any{{coderID(400), "already a task", "keep me", true, coderID(4)}}, coderID(12)))
	subqueryBoth(run, fixture.Select(`SELECT workspace_build_number,workspace_agent_id,workspace_app_id FROM task_workspace_apps WHERE task_id=$1`, []string{"workspace_build_number", "workspace_agent_id", "workspace_app_id"}, []string{"INT4", "UUID", "UUID"}, [][]any{{int64(1), coderID(204), coderID(304)}}, coderID(400)))
	subqueryBoth(run, fixture.Select(`SELECT id,template_parameters,created_at FROM tasks WHERE false`, []string{"id", "template_parameters", "created_at"}, []string{"UUID", "JSONB", "TIMESTAMPTZ"}, nil))
}
