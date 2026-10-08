package stuntgres_test

import (
	"context"
	"fmt"
	"stuntgres"
	"sync"
	"testing"
)

func TestSerialRollbackAndRename(t *testing.T) {
	db := openLocal(t,stuntgres.NewDriver(),t.Name())
	conn := sequenceConn(t,db)
	ctx := context.Background()
	sequenceExec(t,conn,`CREATE TABLE t(id SERIAL)`)
	held,err := conn.PrepareContext(ctx,`INSERT INTO t DEFAULT VALUES`)
	if err != nil { t.Fatal(err) }; defer held.Close()
	sequenceExec(t,conn,`BEGIN`)
	if _,err := held.ExecContext(ctx); err != nil { t.Fatal(err) }
	sequenceExec(t,conn,`ROLLBACK`)
	sequenceValue(t,conn,`SELECT currval('t_id_seq')`,1)
	sequenceExec(t,conn,`ALTER SEQUENCE t_id_seq RENAME TO renamed`)
	sequenceExec(t,conn,`CREATE SEQUENCE t_id_seq START 100`)
	if _,err := held.ExecContext(ctx); err != nil { t.Fatal(err) }
	sequenceValue(t,conn,`SELECT id FROM t`,2)
	sequenceValue(t,conn,`SELECT nextval('t_id_seq')`,100)
	sequenceExec(t,conn,`ALTER TABLE t RENAME COLUMN id TO renamed_id`)
	sequenceExec(t,conn,`ALTER TABLE t RENAME TO renamed_table`)
	if result,err := conn.ExecContext(ctx,`INSERT INTO renamed_table DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	} else if count,err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal(count,err)
	}
	wantTuples(t,db,`SELECT renamed_id FROM renamed_table ORDER BY renamed_id`,[][]any{{int64(2)},{int64(3)}})
}

func TestSerialConcurrentAllocation(t *testing.T) {
	db := openLocal(t,stuntgres.NewDriver(),t.Name())
	tableCoreExec(t,db,false,`CREATE TABLE t(id SERIAL)`,0)
	const count = 12
	var workers sync.WaitGroup
	errors := make(chan error,count)
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r,err := db.Exec(`INSERT INTO t DEFAULT VALUES`)
			if err == nil { var n int64; n,err = r.RowsAffected(); if err == nil && n != 1 { err = fmt.Errorf("affected %d",n) } }
			errors <- err
		}()
	}
	workers.Wait(); close(errors)
	for err := range errors { if err != nil { t.Fatal(err) } }
	want := make([][]any,count)
	for i := range count { want[i] = []any{int64(i+1)} }
	wantTuples(t,db,`SELECT * FROM t ORDER BY id`,want)
}
