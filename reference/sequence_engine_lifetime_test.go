package reference_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSequenceEngineIsolationAndBorrowerMemory(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			backend.db.SetMaxOpenConns(1)
			backend.db.SetMaxIdleConns(1)
			c := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s")
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 1)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			borrowed := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeValue(t, p.ctx, borrowed, "SELECT currval('s')", 1)
			sequenceProbeValue(t, p.ctx, borrowed, "SELECT lastval()", 1)
			if err := borrowed.Close(); err != nil {
				t.Fatal(err)
			}
			if backend.pg {
				// A separate physical PG session shares the sequence, not currval.
				backend.db.SetMaxOpenConns(2)
				a, b := referenceLockConnections(t, p.ctx, backend.db)
				sequenceProbeValue(t, p.ctx, a, "SELECT currval('s')", 1)
				want := controlsReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`)
				sequenceProbeError(t, p.ctx, b, "SELECT currval('s')", &want)
				sequenceProbeValue(t, p.ctx, b, "SELECT nextval('s')", 2)
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
	a := sequenceCoreConn(t, p.ctx, open(d, dsn))
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeValue(t, p.ctx, a, "SELECT nextval('s')", 1)
	shared := sequenceCoreConn(t, p.ctx, open(d, dsn))
	sequenceProbeValue(t, p.ctx, shared, "SELECT nextval('s')", 2)
	for _, db := range []*sql.DB{open(d, dsn+"-different"), open(stuntgres.NewDriver(), dsn)} {
		fresh := sequenceCoreConn(t, p.ctx, db)
		want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
		want.Position = "16"
		sequenceProbeError(t, p.ctx, fresh, "SELECT nextval('s')", &want)
		sequenceProbeExec(t, p.ctx, fresh, "CREATE SEQUENCE s")
		sequenceProbeValue(t, p.ctx, fresh, "SELECT nextval('s')", 1)
	}
}
