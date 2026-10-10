package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceBinaryAccessAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"access_reader"})
	schemaReferenceCleanup(t, "access_private")
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		step := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		defer c.ExecContext(p.ctx, "RESET ROLE")
		for _, q := range []string{"CREATE ROLE access_reader", "CREATE TABLE t(v bytea)", "CREATE SCHEMA access_private", "CREATE TABLE access_private.t(v bytea)"} {
			step(referenceExec(q, 0))
		}
		if b.pg {
			step(referenceExec("GRANT USAGE ON SCHEMA public TO access_reader", 0))
		}
		for _, populated := range []bool{false, true} {
			if populated {
				step(referenceExec("INSERT INTO t VALUES(NULL)", 1))
			}
			step(referenceExec("SET ROLE access_reader", 0))
			for _, q := range []string{"SELECT bit_count(v) FROM public.t", "SELECT get_byte(v,0) FROM public.t WHERE FALSE", "SELECT set_bit(v,8,2) FROM public.t", "SELECT get_byte(v,0) FROM access_private.t"} {
				d := controlsReferenceDiagnostic("42501", "permission denied for table t")
				if strings.Contains(q, "access_private") {
					d.Message = "permission denied for schema access_private"
					d.Position = referencePosition(strings.Index(q, "access_private") + 1)
				}
				s := referenceErrorDiagnostic(q, d)
				s.selectRows = true
				s.errorStage = "execute"
				step(s)
				if d.Position != "" {
					s.errorStage = "prepare"
				}
				step(referencePrepared(s))
			}
			step(referenceExec("RESET ROLE", 0))
		}
		binaryAccessBoth(t, p, b, c, referenceSelect("SELECT bit_count(v) FROM t", []string{"bit_count"}, []string{"INT8"}, [][]any{{nil}}))
	})
}

func TestReferenceBinaryAccessConsumers(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE access_counts(id integer,v bytea,CHECK(bit_count(v)<=8))", 0),
			referenceExec(`INSERT INTO access_counts VALUES(1,'\x'),(2,'\x00'),(3,'\x0180'),(4,'\xff')`, 4),
			referenceSelect("SELECT id,bit_count(v) AS n FROM access_counts WHERE bit_count(v)>=0 ORDER BY bit_count(v),id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(0)}, {int64(3), int64(2)}, {int64(4), int64(8)}}),
			referenceSelect("SELECT bit_count(v) AS n,COUNT(*) AS c FROM access_counts GROUP BY bit_count(v) ORDER BY bit_count(v)", []string{"n", "c"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(2)}, {int64(2), int64(1)}, {int64(8), int64(1)}}),
			referenceSelect("SELECT SUM(bit_count(v)) AS n,COUNT(bit_count(v)) AS c FROM access_counts", []string{"n", "c"}, []string{"NUMERIC", "INT8"}, [][]any{{"10", int64(4)}}),
			referenceExec("CREATE TABLE access_stored(id integer,v bytea,b bytea GENERATED ALWAYS AS (set_byte(v,0,256)) STORED,n bigint GENERATED ALWAYS AS (bit_count(v)) STORED,CHECK(get_byte(v,0)>=0))", 0),
			referenceExec(`INSERT INTO access_stored(id,v) VALUES(1,'\x0180')`, 1),
			referenceExec("CREATE INDEX access_idx ON access_stored(get_bit(v,15::bigint))", 0),
			referenceExec("CREATE VIEW access_view AS SELECT id,"+binaryAccessProjection+" FROM access_stored", 0),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		binaryAccessBoth(t, p, b, c, referenceSelect("SELECT "+binaryAccessProjection+" FROM access_stored", binaryAccessNames, binaryAccessTypes, [][]any{{int64(2), int64(1), int64(1), "\x01\x00", "\x00\x80", "\x80\x01"}}))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`UPDATE access_stored SET v=set_bit(v,8,1) WHERE id=1 RETURNING get_byte(v,1),bit_count(v),b,n`, []string{"get_byte", "bit_count", "b", "n"}, []string{"INT4", "INT8", "BYTEA", "INT8"}, [][]any{{int64(129), int64(3), "\x00\x81", int64(3)}}))
		binaryAccessBoth(t, p, b, c, referenceSelect("SELECT bit_count,get_bit,get_byte,set_bit,set_byte,reverse FROM access_view", binaryAccessNames, binaryAccessTypes, [][]any{{int64(3), int64(1), int64(1), "\x01\x01", "\x00\x81", "\x81\x01"}}))
	})
}

func TestReferenceBinaryAccessAtomicity(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{referenceExec("CREATE TABLE access_atomic(id integer,v bytea,n integer)", 0), referenceExec(`INSERT INTO access_atomic VALUES(1,'\x0180',0),(2,'\x0180',2)`, 2), referenceExec("CREATE TABLE access_destination(v bytea)", 0)} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		want := referenceSelect("SELECT id,v FROM access_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\x01\x80"}, {int64(2), "\x01\x80"}})
		for _, q := range []string{"UPDATE access_atomic SET v=set_byte(v,n,0)", "INSERT INTO access_destination SELECT set_byte(v,n,0) FROM access_atomic ORDER BY id"} {
			binaryAccessBodyError(t, p, b, c, q, "2202E", "index 2 out of valid range, 0..1")
			referenceRunStep(t, p.ctx, c, b.recorder, want)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM access_destination", []string{"v"}, []string{"BYTEA"}, [][]any{}))
		}
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("SAVEPOINT access_before", 0))
		s := referenceErrorDiagnostic("UPDATE access_atomic SET v=set_bit(v,0,n)", controlsReferenceDiagnostic("22023", "new bit must be 0 or 1"))
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, tx, b.recorder, s)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("ROLLBACK TO SAVEPOINT access_before", 0))
		referenceRunStep(t, p.ctx, tx, b.recorder, want)
		referenceRunStep(t, p.ctx, tx, b.recorder, referenceExec("UPDATE access_atomic SET v=set_byte(v,0,256) WHERE id=1", 1))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,v FROM access_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "\x00\x80"}, {int64(2), "\x01\x80"}}))
	})
}
