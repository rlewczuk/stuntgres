package reference_test

import (
	"testing"
	"time"
)

func TestReferenceUpdateReturningTypedVersions(t *testing.T) {
	updateReturningTypesContextReplay(t)
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		db := backend.db
		updateReturningReferenceExec(t, db, "CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)")
		updateReturningReferenceExec(t, db, "INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)")
		q := "UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b"
		want := [][]any{{"2000-01-02", "2000-01-02", "04:05:06.124", "04:05:06.124", time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC), "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", string([]byte{1, 2}), string([]byte{1, 2})}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}}
		if backend.pg {
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
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, types: []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, rows: want, bag: true})
		rows, err = db.Query("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", nil)
		deleteReferenceConsume(t, backend.pg, rows, err, referenceStep{columns: []string{"id", "id"}, types: []string{"INT4", "INT4"}, rows: [][]any{}, bag: false})
	}
}

func updateReturningTypesContextReplay(t *testing.T) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE t(id INT,d DATE,clock TIME(3),stamp TIMESTAMP(3),u UUID,n NUMERIC(8,2),b BYTEA)", 0))
			run(referenceExec(`INSERT INTO t VALUES(1,'2000-01-02','04:05:06.124','2000-01-02 04:05:06.124','a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',12.30,'\x0102'),(2,NULL,NULL,NULL,NULL,NULL,NULL)`, 2))
			var date, clock any = "2000-01-02", "04:05:06.124"
			if backend.pg {
				date = time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
				clock = time.Date(0, 1, 1, 4, 5, 6, 124000000, time.UTC)
			}
			stamp := time.Date(2000, 1, 2, 4, 5, 6, 124000000, time.UTC)
			s := referenceSelect("UPDATE t SET id=id RETURNING old.d,new.d,old.clock,new.clock,old.stamp,new.stamp,old.u,new.u,old.n,new.n,old.b,new.b", []string{"d", "d", "clock", "clock", "stamp", "stamp", "u", "u", "n", "n", "b", "b"}, []string{"DATE", "DATE", "TIME", "TIME", "TIMESTAMP", "TIMESTAMP", "UUID", "UUID", "NUMERIC", "NUMERIC", "BYTEA", "BYTEA"}, [][]any{{date, date, clock, clock, stamp, stamp, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.30", "12.30", "\x01\x02", "\x01\x02"}, {nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}})
			s.prepared, s.bag = true, true
			run(s)
			run(referenceSelect("UPDATE t SET id=3 WHERE d=$1 RETURNING old.id,new.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{}, nil))
		})
	}
}
