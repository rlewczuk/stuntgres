package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGShmemLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() { t.Run(b.Name, func(t *testing.T) {
		pgSequenceExec(t, p, b, b.DB, "CREATE TABLE shmem_life (n integer)")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO shmem_life VALUES (23)", 1))
		for i := range shmemNames { shmemRead(t, p, b, b.DB, i, true, false) }
		if b.PG { return } // Controls and eager detached rows are native APIs.
		var retained []*sql.Rows
		for i, name := range shmemNames {
			rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_catalog."+name); if err != nil { t.Fatal(err) }
			defer rows.Close(); retained = append(retained, rows)
			stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT * FROM pg_catalog."+name); if err != nil { t.Fatal(err) }; defer stmt.Close()
			canceled, cancel := context.WithCancel(p.Ctx); cancel()
			expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0)); defer expire()
			for _, ctx := range []context.Context{canceled, expired} {
				for _, prepared := range []bool{false,true} {
					var out *sql.Rows; var err error
					if prepared { out, err = stmt.QueryContext(ctx) } else { out, err = b.DB.QueryContext(ctx, "SELECT * FROM pg_catalog."+name) }
					if out != nil || !errors.Is(err, ctx.Err()) { t.Fatal("ordinary cancellation", out, err, ctx.Err()) }
				}
			}
			shmemRead(t, p, b, b.DB, i, false, false)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
		if strings.Contains(dump.String(), "pg_shmem_allocations") || strings.Contains(dump.String(), "pg_get_shmem_allocations") || strings.Contains(dump.String(), "stuntgres simulated arena") { t.Fatal("default system metadata or virtual profile serialized", dump.String()) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"not valid SQL;")); err == nil { t.Fatal("invalid restore succeeded") }
		canceled, cancel := context.WithCancel(p.Ctx); cancel()
		if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) { t.Fatal(err) }
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT n FROM shmem_life", []string{"n"}, []string{"INT4"}, [][]any{{int64(23)}}))
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("shmem-independent")
		if err != nil { t.Fatal(err) }
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		for i := range shmemNames { shmemRead(t, p, b, fresh, i, false, false) }
		var sentinel int64
		if err := fresh.QueryRowContext(p.Ctx, "SELECT n FROM shmem_life").Scan(&sentinel); err != nil || sentinel != 23 { t.Fatal(sentinel, err) }
		if err := fresh.Close(); err != nil { t.Fatal(err) }
		reopened := sql.OpenDB(connector)
		defer reopened.Close()
		if err := reopened.QueryRowContext(p.Ctx, "SELECT n FROM shmem_life").Scan(&sentinel); err != nil || sentinel != 23 { t.Fatal("reopen", sentinel, err) }
		for i, name := range shmemNames {
			shmemRead(t, p, b, reopened, i, true, false)
			shmemDefaultACL(t, p, reopened, name, false)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		for i := range shmemNames { shmemRead(t, p, b, b.DB, i, false, false) }
		var present int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='shmem_life'").Scan(&present); err != nil || present != 0 { t.Fatal("reset sentinel", present, err) }
		for _, name := range shmemNames { shmemDefaultACL(t, p, b.DB, name, false) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
		for i, rows := range retained {
			shmemRetained(t, rows, i)
			shmemRead(t, p, b, b.DB, i, true, false)
		}
		shmemACLControls(t, p, b)
	}) }
}
