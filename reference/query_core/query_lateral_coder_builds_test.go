package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func coderLateralLatestBuilds(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := subqueryRun(t, p, b, c)
	coderLateralSchema(run)
	coderWorkspace(run, 10, "live", false)
	coderWorkspace(run, 11, "empty", false)
	coderWorkspace(run, 12, "deleted", true)
	run(fixture.Exec(`UPDATE workspaces SET owner_id='c42fdf75-3097-471c-8c33-fb52454d81c0'`, 3))
	coderBuild(run, 100, 10, 1, "start", "succeeded")
	coderBuild(run, 101, 10, 2, "start", "succeeded")
	coderBuild(run, 102, 12, 1, "start", "succeeded")
	run(fixture.Exec(`UPDATE workspace_builds SET template_version_preset_id=$1 WHERE id IN ($2,$3)`, 2, coderID(6), coderID(100), coderID(102)))
	for _, pair := range []struct{ build, agent int }{{101, 200}, {102, 201}} {
		run(fixture.Exec(`INSERT INTO workspace_resources VALUES($1,$2)`, 1, coderID(pair.agent+1000), coderID(pair.build+1000)))
		run(fixture.Exec(`INSERT INTO workspace_agents VALUES($1,$2,'ready')`, 1, coderID(pair.agent), coderID(pair.agent+1000)))
	}
	coderApplyMigration(t, p, c, "prerequisite_000314_views")
	coderApplyMigration(t, p, c, "000323_workspace_latest_builds_optimization")

	// The view deliberately projects the child's workspace_id, not its parent's:
	// a live workspace without builds contributes a fully NULL child row.
	subqueryBoth(run, fixture.Select(`SELECT id,workspace_id,template_version_id,job_id,template_version_preset_id,transition::text,job_status::text FROM workspace_latest_builds ORDER BY id NULLS LAST`,
		[]string{"id", "workspace_id", "template_version_id", "job_id", "template_version_preset_id", "transition", "job_status"},
		[]string{"UUID", "UUID", "UUID", "UUID", "UUID", "TEXT", "TEXT"}, [][]any{
			{coderID(101), coderID(10), coderID(4), coderID(1101), nil, "start", "succeeded"},
			{nil, nil, nil, nil, nil, nil, nil},
		}))
	subqueryBoth(run, fixture.Select(`SELECT id,name,template_id,ready,current_preset_id FROM workspace_prebuilds ORDER BY id`,
		[]string{"id", "name", "template_id", "ready", "current_preset_id"}, []string{"UUID", "VARCHAR", "UUID", "BOOL", "UUID"}, [][]any{
			{coderID(10), "live", coderID(3), true, coderID(6)},
			{coderID(12), "deleted", coderID(3), false, coderID(6)},
		}))
	// Deleted prebuilds remain in this historical prebuild view, but their
	// readiness is false because latest_builds now excludes their builds.
	subqueryBoth(run, fixture.Select(`SELECT count(*) FROM workspace_latest_builds v JOIN workspace_builds wb ON wb.id=v.id WHERE v.job_id=wb.job_id AND v.workspace_id=wb.workspace_id`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}
