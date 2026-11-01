package reference_test

import (
	"stuntgres/reference/fixture"
)

func fullTargetTurnover(f *fullVacuumCase, canceled bool) {
	f.t.Helper()
	for _, q := range []string{
		"CREATE TABLE full_early(id INT PRIMARY KEY)",
		"CREATE TABLE full_later(id INT)",
		"CREATE TABLE full_parallel(id INT)",
		"CREATE FUNCTION full_later_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN PERFORM pg_advisory_xact_lock(760019); RETURN i; END $$",
		"CREATE INDEX full_later_ix ON full_later(full_later_key(id))",
		"CREATE INDEX full_parallel_ix ON full_parallel(full_later_key(id))",
	} {
		f.run(fixture.Exec(q, 0))
	}
	for _, name := range []string{"full_early", "full_later", "full_parallel"} {
		f.run(fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1))
	}
	early, later, parallel := f.locator("full_early"), f.locator("full_later"), f.locator("full_parallel")
	holder, observer, other := vacuumConn(f.t, f.p, f.b), vacuumConn(f.t, f.p, f.b), vacuumConn(f.t, f.p, f.b)
	inspect := &fullVacuumCase{t: f.t, p: f.p, b: f.b, c: observer}
	pid, holderPID, otherPID := fixture.LockPID(f.t, f.p.Ctx, f.c), fixture.LockPID(f.t, f.p.Ctx, holder), fixture.LockPID(f.t, f.p.Ctx, other)
	vacuumRun(f.t, f.p, f.b, holder, fixture.Select("SELECT pg_advisory_lock(760019)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
	locked := true
	defer func() {
		if locked {
			if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760019)"); err != nil {
				f.t.Error(err)
			}
		}
	}()
	cancel, join, finished := f.start("VACUUM FULL full_early,full_later")
	fullAwaitAdvisory(f, observer, pid, holderPID, finished)
	inspect.rewritten("full_early", early)
	inspect.unchanged("full_later", later)
	vacuumRun(f.t, f.p, f.b, observer, fixture.Select("SELECT relid::bigint AS relid,command FROM pg_stat_progress_cluster WHERE pid=$1", []string{"relid", "command"}, []string{"INT8", "TEXT"}, [][]any{{later.oid, "VACUUM FULL"}}, pid))
	if canceled {
		fullCancel(f, observer, pid, cancel, join, "SQL statement \"SELECT pg_advisory_xact_lock(760019)\"\nPL/pgSQL function full_later_key(integer) line 1 at PERFORM")
		f.rewritten("full_early", early)
		f.unchanged("full_later", later)
	} else {
		second := &fullVacuumCase{t: f.t, p: f.p, b: f.b, c: other, prepared: f.prepared, held: make(map[string]fixture.HeldStatement)}
		_, joinOther, finishedOther := second.start("VACUUM FULL full_parallel")
		fullAwaitAdvisory(f, observer, otherPID, holderPID, finishedOther)
		vacuumRun(f.t, f.p, f.b, observer, fixture.Select("SELECT relid::bigint AS relid,command FROM pg_stat_progress_cluster WHERE pid=$1 OR pid=$2 ORDER BY relid", []string{"relid", "command"}, []string{"INT8", "TEXT"}, [][]any{{later.oid, "VACUUM FULL"}, {parallel.oid, "VACUUM FULL"}}, pid, otherPID))
		if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760019)"); err != nil {
			f.t.Fatal(err)
		}
		locked = false
		o := join()
		fixture.DeleteReferenceCount(f.t, o.Result, o.Err, 0)
		o = joinOther()
		fixture.DeleteReferenceCount(f.t, o.Result, o.Err, 0)
		f.rewritten("full_later", later)
		f.rewritten("full_parallel", parallel)
	}
	if locked {
		if _, err := holder.ExecContext(f.p.Ctx, "SELECT pg_advisory_unlock(760019)"); err != nil {
			f.t.Fatal(err)
		}
		locked = false
	}
	f.emptyProgress()
}
