package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func coderLateralTaskViews(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := subqueryRun(t, p, b, c)
	coderLateralTaskSchema(run)
	for i, name := range []string{"active", "paused", "deleted", "queued"} {
		coderWorkspace(run, 10+i, name, false)
	}
	coderBuild(run, 100, 10, 1, "stop", "succeeded")
	coderBuild(run, 101, 10, 2, "start", "succeeded")
	coderBuild(run, 102, 11, 1, "stop", "succeeded")
	coderBuild(run, 103, 12, 1, "start", "succeeded")
	coderBuild(run, 104, 13, 1, "start", "pending")
	for i := range 5 {
		coderAgentApp(run, 100+i, 200+i, 300+i, "healthy")
	}
	run(fixture.Exec(`UPDATE workspace_apps SET health='unhealthy' WHERE id=$1`, 1, coderID(300)))
	for _, task := range []struct {
		id, workspace int
		name          string
	}{
		{400, 0, "pending"}, {401, 10, "active"}, {402, 11, "paused"}, {403, 12, "deleted"}, {404, 13, "queued"},
	} {
		var workspace any
		if task.workspace != 0 {
			workspace = coderID(task.workspace)
		}
		run(fixture.Exec(`INSERT INTO tasks(id,organization_id,owner_id,name,workspace_id,template_version_id,prompt,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'2025-01-01T00:00:00Z')`, 1,
			coderID(task.id), coderID(1), coderID(2), task.name, workspace, coderID(4), "do\nwork"))
	}
	run(fixture.Exec(`UPDATE tasks SET deleted_at='2025-01-03T00:00:00Z' WHERE name='deleted'`, 1))
	for _, app := range []struct{ task, build int }{{401, 100}, {401, 101}, {402, 102}, {403, 103}, {404, 104}} {
		run(fixture.Exec(`INSERT INTO task_workspace_apps VALUES($1,$2,$3,$4)`, 1, coderID(app.task), coderID(app.build), coderID(app.build+100), coderID(app.build+200)))
	}
	run(fixture.Exec(`UPDATE workspaces SET group_acl='{"00000000-0000-0000-0000-000000000007":["read"]}', user_acl='{"00000000-0000-0000-0000-000000000002":["update"]}' WHERE name='active'`, 1))

	for _, version := range []struct {
		number int
		name   string
	}{
		{379, "000379_create_tasks_with_status_view"},
		{382, "000382_add_columns_to_tasks_with_status"},
		{391, "000391_tasks_with_status_user_fields"},
		{398, "000398_update_task_status_view"},
		{400, "000400_add_task_display_name"},
		{415, "000415_fix_task_pending_status"},
		{427, "000427_add_workspace_acl_to_tasks_view"},
	} {
		t.Run(version.name, func(t *testing.T) {
			coderApplyMigration(t, p, c, version.name)
			queued := "initializing"
			if version.number >= 415 {
				queued = "pending"
			}
			run := subqueryRun(t, p, b, c)
			subqueryBoth(run, fixture.Select(`SELECT name,status::text FROM tasks_with_status ORDER BY name`, []string{"name", "status"}, []string{"TEXT", "TEXT"}, [][]any{{"active", "active"}, {"paused", "paused"}, {"pending", "pending"}, {"queued", queued}}))
			// Do not erase the actual enum view column behind only ::text tests.
			subqueryBoth(run, fixture.Select(`SELECT format_type(atttypid,atttypmod) AS declared_type FROM pg_attribute WHERE attrelid='tasks_with_status'::regclass AND attname='status'`, []string{"declared_type"}, []string{"TEXT"}, [][]any{{"task_status"}}))
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(`SELECT status AS v FROM tasks_with_status ORDER BY name`, []string{"v"}, []string{""}, [][]any{{"active"}, {"paused"}, {"pending"}, {queued}})
				s.Prepared = prepared
				s.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, true)
				fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
			}
			subqueryBoth(run, fixture.Select(`SELECT count(*),min(workspace_build_number),max(workspace_build_number) FROM task_workspace_apps`, []string{"count", "min", "max"}, []string{"INT8", "INT4", "INT4"}, [][]any{{int64(5), int64(1), int64(2)}}))
			if version.number >= 382 {
				subqueryBoth(run, fixture.Select(`SELECT name,workspace_build_number,workspace_agent_id,workspace_app_id FROM tasks_with_status ORDER BY name`, []string{"name", "workspace_build_number", "workspace_agent_id", "workspace_app_id"}, []string{"TEXT", "INT4", "UUID", "UUID"}, [][]any{
					{"active", int64(2), coderID(201), coderID(301)}, {"paused", int64(1), coderID(202), coderID(302)}, {"pending", nil, nil, nil}, {"queued", int64(1), coderID(204), coderID(304)},
				}))
			}
			if version.number >= 391 {
				subqueryBoth(run, fixture.Select(`SELECT name,owner_username,owner_name,owner_avatar_url FROM tasks_with_status ORDER BY name`, []string{"name", "owner_username", "owner_name", "owner_avatar_url"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
					{"active", "alice", "Alice Example", "https://example.test/alice.png"}, {"paused", "alice", "Alice Example", "https://example.test/alice.png"}, {"pending", "alice", "Alice Example", "https://example.test/alice.png"}, {"queued", "alice", "Alice Example", "https://example.test/alice.png"},
				}))
			}
			if version.number >= 398 {
				coderLateralDebug(t, p, b, c, queued)
			}
			if version.number >= 398 {
				subqueryBoth(run, fixture.Select(`SELECT attname,format_type(atttypid,atttypmod) AS declared_type FROM pg_attribute WHERE attrelid='tasks_with_status'::regclass AND attname IN ('workspace_agent_lifecycle_state','workspace_app_health') ORDER BY attname`, []string{"attname", "declared_type"}, []string{"NAME", "TEXT"}, [][]any{{"workspace_agent_lifecycle_state", "workspace_agent_lifecycle_state"}, {"workspace_app_health", "workspace_app_health"}}))
			}
			if version.number >= 400 {
				subqueryBoth(run, fixture.Select(`SELECT name,display_name FROM tasks_with_status ORDER BY name`, []string{"name", "display_name"}, []string{"TEXT", "VARCHAR"}, [][]any{{"active", "do work"}, {"paused", "do work"}, {"pending", "do work"}, {"queued", "do work"}}))
				subqueryBoth(run, fixture.Select(`SELECT col_description('tasks'::regclass,attnum) FROM pg_attribute WHERE attrelid='tasks'::regclass AND attname='display_name'`, []string{"col_description"}, []string{"TEXT"}, [][]any{{"Display name is a custom, human-friendly task name."}}))
				subqueryBoth(run, fixture.Select(`SELECT format_type(atttypid,atttypmod) AS declared_type FROM pg_attribute WHERE attrelid='tasks_with_status'::regclass AND attname='display_name'`, []string{"declared_type"}, []string{"TEXT"}, [][]any{{"character varying(127)"}}))
			}
			if version.number >= 427 {
				subqueryBoth(run, fixture.Select(`SELECT name,workspace_group_acl='{"00000000-0000-0000-0000-000000000007":["read"]}'::jsonb AS g,workspace_user_acl='{"00000000-0000-0000-0000-000000000002":["update"]}'::jsonb AS u FROM tasks_with_status WHERE name='active'`, []string{"name", "g", "u"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"active", true, true}}))
				subqueryBoth(run, fixture.Select(`SELECT name,workspace_group_acl='{}'::jsonb AS g,workspace_user_acl='{}'::jsonb AS u FROM tasks_with_status WHERE name='pending'`, []string{"name", "g", "u"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"pending", true, true}}))
				subqueryBoth(run, fixture.Select(`SELECT workspace_group_acl,workspace_user_acl FROM tasks_with_status WHERE false`, []string{"workspace_group_acl", "workspace_user_acl"}, []string{"JSONB", "JSONB"}, nil))
			}
		})
	}
}

func coderLateralDebug(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queued string) {
	run := subqueryRun(t, p, b, c)
	for _, tc := range []struct{ name, debug string }{
		{"active", `{"build":{"transition":"start","job_status":"succeeded","computed":"active"},"agent":{"lifecycle_state":"ready","computed":"active"},"app":{"health":"healthy","computed":"active"}}`},
		{"paused", `{"build":{"transition":"stop","job_status":"succeeded","computed":"paused"},"agent":{"lifecycle_state":"ready","computed":"active"},"app":{"health":"healthy","computed":"active"}}`},
		{"pending", `{"build":{"transition":null,"job_status":null,"computed":"pending"},"agent":{"lifecycle_state":null,"computed":"initializing"},"app":{"health":null,"computed":"unknown"}}`},
		{"queued", `{"build":{"transition":"start","job_status":"pending","computed":"` + queued + `"},"agent":{"lifecycle_state":"ready","computed":"active"},"app":{"health":"healthy","computed":"active"}}`},
	} {
		subqueryBoth(run, fixture.Select(`SELECT status_debug=$1::jsonb AS matches FROM tasks_with_status WHERE name=$2`, []string{"matches"}, []string{"BOOL"}, [][]any{{true}}, tc.debug, tc.name))
	}
	subqueryBoth(run, fixture.Select(`SELECT status_debug,workspace_agent_lifecycle_state,workspace_app_health FROM tasks_with_status WHERE false`, []string{"status_debug", "workspace_agent_lifecycle_state", "workspace_app_health"}, []string{"JSONB", "", ""}, nil))
	for _, column := range []string{"workspace_agent_lifecycle_state", "workspace_app_health"} {
		label := "ready"
		if column == "workspace_app_health" {
			label = "healthy"
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(`SELECT `+column+` AS v FROM tasks_with_status WHERE name='active'`, []string{"v"}, []string{""}, [][]any{{label}})
			s.Prepared = prepared
			s.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, true)
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
}
