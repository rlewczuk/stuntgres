package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceEngineIsolationAndBorrowerMemory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			backend.DB.SetMaxOpenConns(1)
			backend.DB.SetMaxIdleConns(1)
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s")
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 1)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			borrowed := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeValue(t, p.Ctx, borrowed, "SELECT currval('s')", 1)
			fixture.SequenceProbeValue(t, p.Ctx, borrowed, "SELECT lastval()", 1)
			if err := borrowed.Close(); err != nil {
				t.Fatal(err)
			}
			if backend.PG {
				// A separate physical PG session shares the sequence, not currval.
				backend.DB.SetMaxOpenConns(2)
				a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
				fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT currval('s')", 1)
				want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
				fixture.SequenceProbeError(t, p.Ctx, b, "SELECT currval('s')", &want)
				fixture.SequenceProbeValue(t, p.Ctx, b, "SELECT nextval('s')", 2)
			}
		})
	}
	// Native exact opaque DSN/Driver identity has no PostgreSQL analogue.
	d := stuntgres.NewDriver()
	open := func(d *stuntgres.Driver, dsn string) *sql.DB {
		t.Helper()
		connector, err := d.OpenConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		db := sql.OpenDB(connector)
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Error(err)
			}
		})
		return db
	}
	dsn := t.Name()
	a := fixture.SequenceCoreConn(t, p.Ctx, open(d, dsn))
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
	fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT nextval('s')", 1)
	shared := fixture.SequenceCoreConn(t, p.Ctx, open(d, dsn))
	fixture.SequenceProbeValue(t, p.Ctx, shared, "SELECT nextval('s')", 2)
	for _, db := range []*sql.DB{open(d, dsn+"-different"), open(stuntgres.NewDriver(), dsn)} {
		fresh := fixture.SequenceCoreConn(t, p.Ctx, db)
		want := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
		want.Position = "16"
		fixture.SequenceProbeError(t, p.Ctx, fresh, "SELECT nextval('s')", &want)
		fixture.SequenceProbeExec(t, p.Ctx, fresh, "CREATE SEQUENCE s")
		fixture.SequenceProbeValue(t, p.Ctx, fresh, "SELECT nextval('s')", 1)
	}
}
