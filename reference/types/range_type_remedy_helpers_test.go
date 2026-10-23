package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func rangeTypeRemedyRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		next := s
		next.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, next)
	}
}

// Native replacement has no PG API equivalent. Its ordinary SQL checks remain
// shared, including checks of the actual emitted stream on the real oracle.
func rangeTypeRemedyReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, roles []string, check func(fixture.Backend, fixture.Executor), insert fixture.Step, schemas []string) {
	t.Helper()
	if b.PG {
		return
	}
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	target := fixture.NewFixture(t, false, true)
	owner, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	for _, role := range roles {
		fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Exec("CREATE ROLE "+role, 0))
	}
	fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, fixture.Exec("CREATE TEMP TABLE remedy_occupied(k integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
		t.Fatal("fresh remedy Restore", err)
	}
	dest := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	check(dest, owner)
	var redump bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(redump.Bytes())); err != nil {
		t.Fatal("remedy redump Restore", err)
	}
	check(dest, owner)
	if insert.Query != "" {
		fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, insert)
	}
	check(b, c)
	if p.Postgres != nil {
		clear := ""
		for _, schema := range schemas {
			clear += "DROP SCHEMA IF EXISTS " + schema + " CASCADE;"
		}
		clear += "DROP SCHEMA public CASCADE;CREATE SCHEMA public;"
		oracle, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer oracle.Close()
		var createdRoles []string
		// Register before the first creation. Failed replay can leave session
		// authorization changed; cleanup uses the privileged pinned connection
		// and a separate bounded context even after the scenario deadline.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := oracle.ExecContext(ctx, "SET SESSION AUTHORIZATION DEFAULT;RESET ROLE;"+clear); err != nil {
				t.Error("remedy replay cleanup", err)
			}
			for _, role := range createdRoles {
				if _, err := oracle.ExecContext(ctx, "DROP OWNED BY "+role+" CASCADE;DROP ROLE "+role); err != nil {
					t.Error("remedy role cleanup", role, err)
				}
			}
		}()
		if _, err := oracle.ExecContext(p.Ctx, clear); err != nil {
			t.Fatal(err)
		}
		for _, role := range roles {
			if _, err := oracle.ExecContext(p.Ctx, "CREATE ROLE "+role); err != nil {
				t.Fatal(err)
			}
			createdRoles = append(createdRoles, role)
		}
		if _, err := oracle.ExecContext(p.Ctx, script.String()); err != nil {
			t.Fatal("actual remedy dump PG replay", err)
		}
		check(fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, oracle)
	}
}
