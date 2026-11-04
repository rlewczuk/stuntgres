package reference_test

import (
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSepgsqlSimulationSharedLabels(t *testing.T) {
	var databases []string
	fixture.DatabaseReferenceCleanup(t, &databases)
	config := sepgsqlConfig()
	p := fixture.NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return d.RegisterSepgsqlSimulation(config) })
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step fixture.Step) {
				t.Helper()
				defer func() {
					if t.Failed() {
						t.Logf("database owner SQL: %s", step.Query)
					}
				}()
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, step)
			}
			const name = "sepgsql_shared_label_peer"
			if backend.PG {
				databases = append(databases, name)
			}
			run(fixture.Exec("CREATE DATABASE "+name, 0))
			peer, _, recorder := fixture.DatabaseScenarioPool(t, p, backend.PG, name, "")
			other, err := peer.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			peerRun := func(step fixture.Step) {
				t.Helper()
				defer func() {
					if t.Failed() {
						t.Logf("database peer SQL: %s", step.Query)
					}
				}()
				fixture.RunStep(t, p.Ctx, other, recorder, step)
			}
			for _, execute := range []sepgsqlRun{run, peerRun} {
				execute(sepgsqlCount("SELECT count(*) FROM pg_database WHERE datname='"+name+"'", 1))
			}
			predicate := "classoid='pg_database'::regclass AND objoid=(SELECT oid FROM pg_database WHERE datname='" + name + "') AND provider='selinux'"
			if backend.PG {
				run(fixture.ImplicitError("SECURITY LABEL FOR selinux ON DATABASE "+name+" IS '"+sepgsqlS+"'", fixture.Diagnostic{Code: "22023", Message: "security label provider \"selinux\" is not loaded"}))
				for _, execute := range []sepgsqlRun{run, peerRun} {
					execute(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE "+predicate, 0))
					execute(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE "+predicate, 0)))
					execute(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE "+predicate, 0))
					execute(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE "+predicate, 0)))
				}
				return
			}
			labelCount := func(context string) fixture.Step {
				return sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE "+predicate+" AND label='"+context+"'", 1)
			}
			for _, context := range []string{sepgsqlS, sepgsqlP} {
				run(fixture.Exec("SECURITY LABEL ON DATABASE "+name+" IS '"+context+"'", 0))
				for _, execute := range []sepgsqlRun{run, peerRun} {
					execute(labelCount(context))
					execute(fixture.Prepared(labelCount(context)))
					execute(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE "+predicate, 0))
					presentation := sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE "+predicate+" AND objtype='database' AND objnamespace IS NULL AND objname='"+name+"' AND label='"+context+"'", 1)
					execute(presentation)
					execute(fixture.Prepared(presentation))
				}
			}
			run(fixture.ImplicitError("SECURITY LABEL ON DATABASE "+name+" IS 'invalid'", fixture.Diagnostic{Code: "42602", Message: "SELinux: invalid security label: \"invalid\""}))
			run(labelCount(sepgsqlP))
			peerRun(labelCount(sepgsqlP))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SECURITY LABEL ON DATABASE "+name+" IS '"+sepgsqlS+"'", 0))
			run(labelCount(sepgsqlS))
			peerRun(labelCount(sepgsqlP))
			run(fixture.Exec("ROLLBACK", 0))
			run(labelCount(sepgsqlP))
			peerRun(labelCount(sepgsqlP))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SECURITY LABEL ON DATABASE "+name+" IS '"+sepgsqlS+"'", 0))
			run(fixture.Exec("COMMIT", 0))
			peerRun(labelCount(sepgsqlS))
			run(fixture.Exec("SECURITY LABEL ON DATABASE "+name+" IS NULL", 0))
			for _, execute := range []sepgsqlRun{run, peerRun} {
				execute(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE "+predicate, 0))
				execute(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM pg_shseclabel WHERE "+predicate, 0)))
				execute(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE "+predicate, 0))
				execute(fixture.Prepared(sepgsqlCount("SELECT count(*) FROM pg_seclabels WHERE "+predicate, 0)))
			}
		})
	}
}
