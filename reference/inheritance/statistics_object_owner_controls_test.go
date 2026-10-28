package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectControlsOwners(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"statistics_control_owner", "statistics_table_owner", "statistics_schema_owner"})
	fixture.SchemaReferenceCleanup(t, "statistics_control_schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, q := range []string{"CREATE ROLE statistics_control_owner", "CREATE ROLE statistics_table_owner", "CREATE ROLE statistics_schema_owner"} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
	}
	native := p.Backends()[0]
	conn, err := native.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, native.Recorder, s) }
	for _, q := range []string{
		"CREATE SCHEMA statistics_control_schema AUTHORIZATION statistics_schema_owner",
		"GRANT USAGE,CREATE ON SCHEMA statistics_control_schema TO statistics_control_owner",
		"CREATE TABLE statistics_control_schema.input(a INT,b INT)",
		"ALTER TABLE statistics_control_schema.input OWNER TO statistics_control_owner",
		"SET ROLE statistics_control_owner",
		"CREATE STATISTICS statistics_control_schema.definition (mcv,dependencies) ON b,a FROM statistics_control_schema.input",
		"RESET ROLE",
		"ALTER TABLE statistics_control_schema.input OWNER TO statistics_table_owner",
		"REVOKE CREATE ON SCHEMA statistics_control_schema FROM statistics_control_owner",
	} {
		run(fixture.Exec(q, 0))
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Len() == 0 {
		t.Fatal("empty mixed-owner dump")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			executor := fixture.Executor(b.DB)
			if b.PG {
				connection := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer connection.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := connection.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("PostgreSQL mixed-owner replay", err)
				}
				executor = connection
			} else {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			q := "SELECT s.stxkeys,s.stxkind,so.rolname AS statistics_owner,ro.rolname AS relation_owner,no.rolname AS namespace_owner FROM pg_statistic_ext s JOIN pg_class c ON c.oid=s.stxrelid JOIN pg_namespace n ON n.oid=s.stxnamespace JOIN pg_roles so ON so.oid=s.stxowner JOIN pg_roles ro ON ro.oid=c.relowner JOIN pg_roles no ON no.oid=n.nspowner WHERE s.stxname='definition' AND n.nspname='statistics_control_schema'"
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"stxkeys", "stxkind", "statistics_owner", "relation_owner", "namespace_owner"}, []string{"INT2VECTOR", "_CHAR", "NAME", "NAME", "NAME"}, [][]any{{"1 2", "{f,m}", "statistics_control_owner", "statistics_table_owner", "statistics_schema_owner"}}))
			if !b.PG {
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved.Bytes(), again.Bytes()) {
					t.Fatal("mixed-owner redump changed")
				}
			}
		})
	}
}
