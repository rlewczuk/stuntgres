package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceUpdateReturningTypedVersions(t *testing.T) {
	updateReturningTypesContextReplay(t)
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		fixture.UpdateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)")
		fixture.UpdateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)")
		q := "UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b"
		want := [][]any{{"2000-01-02", "2000-01-02", "04:05:06.124", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", string([]byte{1, 2}), string([]byte{1, 2})}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}}
		if backend.PG {
			for _, index := range []int{0, 1} {
				want[0][index] = time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
			}
			for _, index := range []int{2, 3} {
				want[0][index] = time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC)
			}
		}
		held, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		rows, err := held.Query()
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, Types: []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, Rows: want, Bag: true})
		rows, err = db.Query("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", nil)
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id", "id"}, Types: []string{"INT4", "INT4"}, Rows: [][]any{}, Bag: false})
	}
}

func updateReturningTypesContextReplay(t *testing.T) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)", 0))
			run(fixture.Exec(`INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)`, 2))
			var date, clock any = "2000-01-02", "04:05:06.124"
			if backend.PG {
				date = time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
				clock = time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC)
			}
			stamp := time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)
			s := fixture.Select("UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b", []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, [][]any{{date, date, clock, clock, stamp, stamp, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", "\x01\x02", "\x01\x02"}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}})
			s.Prepared, s.Bag = true, true
			run(s)
			run(fixture.Select("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{}, nil))
		})
	}
}
