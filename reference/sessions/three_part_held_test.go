package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

const threePartHeldDelete = "delete from PUBLIC.NONCES where PUBLIC.NONCES.ID = cast($1 as uuid)"
const threePartHeldUUID = "00000000-0000-0000-0000-000000000001"

func threePartHeldScenario(t *testing.T, ctx context.Context, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(s fixture.Step) { fixture.RunStep(t, ctx, conn, b.Recorder, s) }
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA qa", 0), fixture.Exec("CREATE SCHEMA qb", 0),
		fixture.Exec("CREATE TABLE qa.items(id int,v int)", 0), fixture.Exec("CREATE TABLE qb.items(id int,v int)", 0),
		fixture.Exec("INSERT INTO qa.items VALUES(1,10),(2,20),(3,30)", 3), fixture.Exec("INSERT INTO qb.items VALUES(1,900)", 1),
		fixture.Exec("CREATE TABLE public.nonces(id uuid,note text)", 0),
		fixture.Exec("INSERT INTO public.nonces VALUES($1,'match'),('00000000-0000-0000-0000-000000000002','other'),(NULL,'null')", 3, threePartHeldUUID),
	} {
		run(s)
	}
	selectSQL := "SELECT qa.items.id,qa.items.v FROM qa.items WHERE qa.items.id >= $1 ORDER BY qa.items.id DESC"
	updateSQL := "UPDATE qa.items SET v=qa.items.v+$1 WHERE qa.items.id=$2 RETURNING qa.items.id,qa.items.v"
	run(fixture.Select(selectSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}, {int64(2), int64(20)}}, 2))
	run(fixture.Select(updateSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(20)}}, 0, 2))
	run(fixture.Exec(threePartHeldDelete, 0, "00000000-0000-0000-0000-000000000099"))
	selectStmt, err := conn.PrepareContext(ctx, selectSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer selectStmt.Close()
	updateStmt, err := conn.PrepareContext(ctx, updateSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer updateStmt.Close()
	deleteStmt, err := conn.PrepareContext(ctx, threePartHeldDelete)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteStmt.Close()
	selectHeld := fixture.HeldStatement{Executor: conn, Statement: selectStmt, SQL: selectSQL}
	updateHeld := fixture.HeldStatement{Executor: conn, Statement: updateStmt, SQL: updateSQL}
	deleteHeld := fixture.HeldStatement{Executor: conn, Statement: deleteStmt, SQL: threePartHeldDelete}
	for _, path := range []string{"qb,qa", "qa,qb"} {
		run(fixture.Exec("SET search_path="+path, 0))
		for _, arg := range []any{99, 2, nil, 1} {
			var rows [][]any
			if arg == 2 {
				rows = [][]any{{int64(3), int64(30)}, {int64(2), int64(20)}}
			}
			if arg == 1 {
				rows = [][]any{{int64(3), int64(30)}, {int64(2), int64(20)}, {int64(1), int64(10)}}
			}
			fixture.RunStep(t, ctx, selectHeld, b.Recorder, fixture.Select(selectSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, rows, arg))
		}
	}
	for _, tc := range []struct {
		delta, id any
		rows      [][]any
	}{
		{5, 99, nil}, {5, 2, [][]any{{int64(2), int64(25)}}}, {-5, 2, [][]any{{int64(2), int64(20)}}}, {1, nil, nil},
	} {
		fixture.RunStep(t, ctx, updateHeld, b.Recorder, fixture.Select(updateSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, tc.rows, tc.delta, tc.id))
	}
	for _, tc := range []struct {
		arg   any
		count int64
	}{{"00000000-0000-0000-0000-000000000099", 0}, {threePartHeldUUID, 1}, {threePartHeldUUID, 0}, {nil, 0}, {"00000000-0000-0000-0000-000000000002", 1}} {
		fixture.RunStep(t, ctx, deleteHeld, b.Recorder, fixture.Exec(threePartHeldDelete, tc.count, tc.arg))
	}
	run(fixture.Select("SELECT note FROM public.nonces ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"null"}}))
	run(fixture.Exec("INSERT INTO public.nonces VALUES($1,'restored')", 1, threePartHeldUUID))
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, ctx, tx, b.Recorder, fixture.Exec(threePartHeldDelete, 1, threePartHeldUUID))
	fixture.RunStep(t, ctx, tx, b.Recorder, fixture.Exec("UPDATE qa.items SET v=qa.items.v+1 WHERE qa.items.id=2", 1))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT note FROM public.nonces ORDER BY note", []string{"note"}, []string{"TEXT"}, [][]any{{"null"}, {"restored"}}))
	// Reanalysis sees a compatible DDL revision without replacing the held handle.
	run(fixture.Exec("ALTER TABLE qa.items ADD COLUMN extra int", 0))
	fixture.RunStep(t, ctx, selectHeld, b.Recorder, fixture.Select(selectSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(30)}, {int64(2), int64(20)}}, 2))
}

func TestReferenceThreePartHeld(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "qa")
	fixture.SchemaReferenceCleanup(t, "qb")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { threePartHeldScenario(t, p.Ctx, b) })
	}
}
