package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationSessions(t *testing.T) {
	isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, installer *sql.Conn) {
		isnConversionRun(t, p, b, installer)(fixture.Exec("CREATE EXTENSION isn", 0))
		var noticesA, noticesB []fixture.Diagnostic
		pool, a, recorderA := fixture.NoticeReferencePool(t, p, b.PG, &noticesA)
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		_, other, recorderB := fixture.NoticeReferencePool(t, p, b.PG, &noticesB)
		defer other.Close()
		defer isnValidationCleanup(t, other)
		ba := fixture.Backend{PG: b.PG, DB: pool, Recorder: recorderA}
		bb := fixture.Backend{PG: b.PG, Recorder: recorderB}
		runA := isnConversionRun(t, p, ba, a)
		runB := isnConversionRun(t, p, bb, other)
		defer a.Close()
		var pid int64
		if b.PG {
			pid = fixture.OwnershipID(t, p, a, "SELECT pg_backend_pid()::bigint")
			if pid == fixture.OwnershipID(t, p, other, "SELECT pg_backend_pid()::bigint") {
				t.Fatal("simultaneous borrowers share a physical backend")
			}
		}
		isnValidationMode(runA, true)
		runB(isnValidationBool("SELECT isn_weak() AS v", false))
		for _, expression := range []string{"$1::ean13", "$1::text::ean13"} {
			q := "SELECT is_valid(" + expression + ") AS v"
			isnConversionRoutes(t, p, ba, a, isnValidationBool(q, false, isnValidationBad[0]))
			d := isnValidationChecksum(0, isnValidationBad[0])
			if expression == "$1::ean13" {
				d.Context = "unnamed portal parameter $1 = '...'"
			}
			isnConversionRoutes(t, p, bb, other, isnValidationQueryError(q, d, isnValidationBad[0]))
		}
		// Intentional reuse is NOT a new session. Same max-open-one pool, no
		// intervening cleanup/reset, and a relational PG identity assertion.
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		pooledGetter := fixture.IdentityHeld(t, p.Ctx, pool, "SELECT isn_weak() AS v")
		defer pooledGetter.Statement.Close()
		fixture.RunStep(t, p.Ctx, pooledGetter, recorderA, isnValidationBool(pooledGetter.SQL, true))
		reused, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if b.PG && fixture.OwnershipID(t, p, reused, "SELECT pg_backend_pid()::bigint") != pid {
			t.Fatal("pool reuse witness did not retain the same physical session")
		}
		runReuse := isnConversionRun(t, p, ba, reused)
		runReuse(isnValidationBool("SELECT isn_weak() AS v", true))
		isnConversionRoutes(t, p, ba, reused, isnValidationBool("SELECT is_valid($1::ean13) AS v", false, isnValidationBad[0]))
		// Closing idle connections really retires that physical owner; a new
		// connection in the SAME engine must default off without leaking mode.
		pool.SetMaxIdleConns(0)
		if err := reused.Close(); err != nil {
			t.Fatal(err)
		}
		func() {
			fresh, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			defer isnValidationCleanup(t, fresh)
			if b.PG && fixture.OwnershipID(t, p, fresh, "SELECT pg_backend_pid()::bigint") == pid {
				t.Fatal("new physical borrower reused retired identity")
			}
			runFresh := isnConversionRun(t, p, ba, fresh)
			runFresh(isnValidationBool("SELECT isn_weak() AS v", false))
			d := isnValidationChecksum(0, isnValidationBad[0])
			d.Context = "unnamed portal parameter $1 = '...'"
			runFresh(isnValidationQueryError("SELECT $1::ean13", d, isnValidationBad[0]))
		}()
		// A database/sql statement prepared on the previous physical owner
		// executes on the new owner without retaining its original setting.
		fixture.RunStep(t, p.Ctx, pooledGetter, recorderA, isnValidationBool(pooledGetter.SQL, false))
		if b.PG {
			return
		}
		// A distinct Driver is a distinct engine as well as a distinct owner.
		connector, err := stuntgres.NewDriver().OpenConnector("isn_validation_independent")
		if err != nil {
			t.Fatal(err)
		}
		db, recorder := fixture.RecordedNativePool(t, connector)
		x, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		defer isnValidationCleanup(t, x)
		independent := isnConversionRun(t, p, fixture.Backend{DB: db, Recorder: recorder}, x)
		independent(fixture.Exec("CREATE EXTENSION isn", 0))
		independent(isnValidationBool("SELECT isn_weak() AS v", false))
	})
}
