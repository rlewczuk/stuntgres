package reference_test

import "testing"

func TestReferenceRangePartitionConsumers(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_source(k INT NOT NULL,payload TEXT) PARTITION BY RANGE(k)", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT * FROM rp_source", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil)
			s.prepared = prepared
			s.checkMetadata = referenceRangeMetadata(b.pg, s.columns, s.types, []bool{false, true})
			run(s)
		}
		run(referenceExec("CREATE TABLE rp_source_a PARTITION OF rp_source FOR VALUES FROM(1) TO(3)", 0))
		run(referenceExec("CREATE TABLE rp_source_b PARTITION OF rp_source FOR VALUES FROM(3) TO(5)", 0))
		run(referenceExec("INSERT INTO rp_source VALUES(1,'a'),(2,'b'),(3,'c'),(4,'d')", 4))
		rows := [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}, {int64(4), "d"}}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT * FROM rp_source ORDER BY k", "SELECT k,payload FROM (SELECT * FROM rp_source) s ORDER BY k"} {
				s := referenceSelect(q, []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows)
				s.prepared = prepared
				s.checkMetadata = referenceRangeMetadata(b.pg, s.columns, s.types, []bool{false, true})
				run(s)
			}
			s := referenceSelect("SELECT COUNT(*),SUM(k) FROM rp_source", []string{"count", "sum"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(10)}})
			s.prepared = prepared
			run(s)
			s = referenceSelect("SELECT a.k,a.payload FROM rp_source a JOIN rp_source b ON a.k=b.k ORDER BY a.k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows)
			s.prepared = prepared
			run(s)
			s = referenceSelect("SELECT k,payload FROM rp_source WHERE k=$1", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}}, 3)
			s.prepared = prepared
			run(s)
			for _, relation := range []string{"rp_source", "rp_source_a", "rp_source_b"} {
				s = referenceSelect("SELECT k,payload FROM "+relation+" WHERE k=99", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil)
				s.prepared = prepared
				s.checkMetadata = referenceRangeMetadata(b.pg, s.columns, s.types, []bool{false, true})
				run(s)
			}
		}
		run(referenceExec("CREATE VIEW rp_view AS SELECT k,payload FROM rp_source", 0))
		run(referenceSelect("SELECT * FROM rp_view ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows))
		run(referenceExec("CREATE TABLE rp_copy(k INT,payload TEXT)", 0))
		run(referencePrepared(referenceExec("INSERT INTO rp_copy SELECT * FROM rp_source", 4)))
		run(referenceSelect("SELECT * FROM rp_copy ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows))
		run(referenceExec("CREATE TABLE rp_ctas AS SELECT * FROM rp_source", 4))
		run(referenceSelect("SELECT * FROM rp_ctas ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, rows))
		run(referenceExec("INSERT INTO rp_source SELECT * FROM rp_copy", 4))
		run(referenceSelect("SELECT COUNT(*) FROM rp_source a JOIN rp_source b ON a.k=b.k", []string{"count"}, []string{"INT8"}, [][]any{{int64(16)}}))
		run(referenceSelect("SELECT relname,relkind,relnatts FROM pg_class WHERE relname='rp_source' OR relname='rp_source_a' OR relname='rp_source_b' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"rp_source", "p", int64(2)}, {"rp_source_a", "r", int64(2)}, {"rp_source_b", "r", int64(2)}}))
		run(referenceSelect("SELECT d.deptype FROM pg_depend d JOIN pg_class c ON d.objid=c.oid JOIN pg_class p ON d.refobjid=p.oid WHERE c.relname='rp_source_a' AND p.relname='rp_source' AND d.classid=1259 AND d.refclassid=1259 AND d.objsubid=0 AND d.refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"a"}}))
	})
}

func TestReferenceRangePartitionHeldTopology(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_held(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_held_a PARTITION OF rp_held FOR VALUES FROM(1) TO(3)", 0))
		q := "INSERT INTO rp_held VALUES($1)"
		insert, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		readQuery := "SELECT k FROM rp_held ORDER BY k"
		read, err := b.db.PrepareContext(p.ctx, readQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write := referenceHeldStatement{referenceExecutor: b.db, statement: insert, query: q}
		reader := referenceHeldStatement{referenceExecutor: b.db, statement: read, query: readQuery}
		referenceRunStep(t, p.ctx, write, b.recorder, referenceExec(q, 1, 1))
		referenceRunStep(t, p.ctx, reader, b.recorder, referenceRangeRows(readQuery, 1))
		run(referenceExec("CREATE TABLE rp_held_b PARTITION OF rp_held FOR VALUES FROM(3) TO(5)", 0))
		referenceRunStep(t, p.ctx, write, b.recorder, referenceExec(q, 1, 3))
		referenceRunStep(t, p.ctx, reader, b.recorder, referenceRangeRows(readQuery, 1, 3))
		run(referenceExec("DROP TABLE rp_held_a", 0))
		referenceRunStep(t, p.ctx, reader, b.recorder, referenceRangeRows(readQuery, 3))
		miss := referenceRangeMiss(q, "rp_held", "k", "1")
		miss.args = []any{1}
		referenceRunStep(t, p.ctx, write, b.recorder, miss)
	})
}
