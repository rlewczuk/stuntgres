package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func partitionDetachControlSetup(run func(fixture.Step)) {
	partitionDetachSetup(run, "pd_parent")
	run(fixture.Exec("CREATE TABLE __stuntgres_partition_replay_1(k INT)", 0))
	run(fixture.Exec("INSERT INTO __stuntgres_partition_replay_1 VALUES(42)", 1))
	run(fixture.Exec("CREATE VIEW pd_parent_view AS SELECT k,payload FROM pd_parent", 0))
	run(fixture.Exec("CREATE VIEW pd_child_view AS SELECT k,payload FROM pd_child", 0))
	run(fixture.Exec("CREATE TABLE pd_control_refs(ref REGCLASS DEFAULT 'pd_child'::regclass)", 0))
	run(fixture.Exec("INSERT INTO pd_control_refs DEFAULT VALUES", 1))
	run(fixture.Exec("CREATE INDEX pd_control_local ON pd_child((length(payload))) WHERE k>0", 0))
	run(fixture.Exec("ALTER TABLE pd_parent DETACH PARTITION pd_child", 0))
	run(fixture.Exec("CREATE TABLE pd_control_outer(k INT,j INT,CONSTRAINT pd_control_outer_key PRIMARY KEY(k,j)) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE pd_control_mid(j INT NOT NULL,k INT NOT NULL,CONSTRAINT pd_control_mid_key PRIMARY KEY(k,j)) PARTITION BY RANGE(j)", 0))
	run(fixture.Exec("CREATE TABLE pd_control_leaf PARTITION OF pd_control_mid DEFAULT", 0))
	run(fixture.Exec("INSERT INTO pd_control_leaf VALUES(9,1)", 1))
	run(fixture.Exec("ALTER TABLE pd_control_outer ATTACH PARTITION pd_control_mid DEFAULT", 0))
	run(fixture.Exec("ALTER TABLE pd_control_outer DETACH PARTITION pd_control_mid", 0))
	partitionDetachInvalidSetup(run)
	run(fixture.Exec("ALTER TABLE pd_invalid DETACH PARTITION pd_unindexed", 0))
	run(fixture.Exec("ALTER TABLE pd_invalid DETACH PARTITION pd_indexed", 0))
	partitionDetachNestedInvalidSetup(run)
	run(fixture.Exec("ALTER TABLE pd_ix_mid DETACH PARTITION pd_ix_b", 0))
	run(fixture.Exec("ALTER TABLE pd_ix_root DETACH PARTITION pd_ix_mid", 0))
}

func partitionDetachControlState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT k,payload FROM pd_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}})
		s.Prepared = prepared
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{false, true})
		run(s)
		s = fixture.Select("SELECT * FROM pd_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"b", int64(2)}})
		s.Prepared = prepared
		s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, false})
		run(s)
	}
	run(partitionDetachClass("pd_child", false))
	run(partitionDetachMember("pd_child", "pd_parent", false))
	run(partitionDetachTopEdges("pd_child_key", false))
	run(partitionConstraintEdge("pd_sibling_k_key", "pd_key", "pd_sibling"))
	run(partitionConstraintEdge("pd_default_k_key", "pd_key", "pd_default"))
	run(fixture.Select("SELECT conname,conparentid=0 AS independent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pd_child'::regclass::OID ORDER BY conname", []string{"conname", "independent", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "INT2"}, [][]any{{"pd_check", true, true, int64(0)}, {"pd_child_key", true, true, int64(0)}, {"pd_child_nn", true, true, int64(0)}, {"pd_local", true, true, int64(0)}}))
	run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass::OID AND d.objid='pd_child_key'::regclass::OID AND d.refclassid='pg_constraint'::regclass::OID AND c.conname='pd_child_key' AND d.deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(referenceRangeRows("SELECT k FROM pd_parent_view", 3))
	run(referenceRangeRows("SELECT k FROM pd_child_view ORDER BY k", 1, 2))
	run(fixture.Select("SELECT ref='pd_child'::regclass AS same FROM pd_control_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	// k is the last surviving physical column and both survivors have defaults.
	// Whole-catalog replay may remint attribute numbers, not ownership targets.
	run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_attrdef a ON a.adrelid=d.refobjid AND a.adnum=d.refobjsubid WHERE d.classid='pg_class'::regclass::OID AND d.objid='pd_seq'::regclass::OID AND d.refobjid='pd_child'::regclass::OID AND d.deptype='a' AND a.adnum=(SELECT max(adnum) FROM pg_attrdef WHERE adrelid='pd_child'::regclass::OID)", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(partitionDetachClass("pd_control_mid", false))
	run(partitionDetachClass("pd_control_leaf", true))
	run(partitionDetachTopEdges("pd_control_mid_key", false))
	run(partitionConstraintEdge("pd_control_leaf_pkey", "pd_control_mid_key", "pd_control_leaf"))
	run(referenceRangeRows("SELECT k FROM pd_control_mid", 1))
	run(fixture.Select("SELECT k FROM pd_control_outer", []string{"k"}, []string{"INT4"}, nil))
	partitionDetachInvalidFinal(run)
	partitionDetachNestedInvalidFinal(run)
	run(referenceRangeRows("SELECT k FROM __stuntgres_partition_replay_1", 42))
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname LIKE '__stuntgres_partition_replay_%' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"__stuntgres_partition_replay_1"}}))
	run(fixture.Select("SELECT c.relname,i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid='pd_ix_root'::regclass::OID OR i.indrelid='pd_ix_mid'::regclass::OID OR i.indrelid='pd_ix_a'::regclass::OID ORDER BY c.relname", []string{"relname", "indisvalid"}, []string{"NAME", "BOOL"}, [][]any{{"pd_ix_a_idx", true}, {"pd_ix_mid_idx", false}, {"pd_ix_root_idx", false}}))
}

func partitionDetachControlFuture(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(fixture.Exec("INSERT INTO pd_child(payload) VALUES('sequence')", 1))
	run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2, 4))
	run(fixture.Exec("INSERT INTO pd_parent VALUES(1,'new-default')", 1))
	run(referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 1, 3))
	run(partitionConstraintDuplicate("INSERT INTO pd_child(payload,k) VALUES('duplicate',1)", "pd_child", "pd_child_key", "Key (k)=(1) already exists."))
	run(fixture.ImplicitError("INSERT INTO pd_child(payload,k) VALUES('zero',0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pd_child" violates check constraint "pd_check"`, Detail: "Failing row contains (zero, 0).", Schema: "public", Table: "pd_child", Constraint: "pd_check"}))
	run(fixture.Exec("INSERT INTO pd_control_mid VALUES(10,7)", 1))
	run(referenceRangeRows("SELECT k FROM pd_control_leaf ORDER BY k", 1, 7))
	run(partitionConstraintDuplicate("INSERT INTO pd_indexed VALUES(1)", "pd_indexed", "pd_indexed_idx", "Key (k)=(1) already exists."))
	run(fixture.Exec("DROP VIEW pd_parent_view", 0))
	run(fixture.Exec("DROP TABLE pd_parent", 0))
	run(referenceRangeRows("SELECT k FROM pd_child_view ORDER BY k", 1, 2, 4))
	run(fixture.Exec("ALTER TABLE pd_child DROP CONSTRAINT pd_child_key", 0))
	run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='pd_child_key'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

type partitionDetachFailReader struct{ err error }

func (r partitionDetachFailReader) Read([]byte) (int, error) { return 0, r.err }

type partitionDetachCancelReader struct {
	cancel context.CancelFunc
	reader io.Reader
}

func (r *partitionDetachCancelReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.cancel()
	return n, e
}

func TestReferencePartitionDetachControls(t *testing.T) {
	partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		partitionDetachControlSetup(run)
		partitionDetachControlState(t, p, b.DB, b.Recorder, b.PG)
		if b.PG {
			partitionDetachControlFuture(t, p, b.DB, b.Recorder)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT k FROM pd_child ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		referenceRangeMetadata(false, []string{"k"}, []string{"INT4"}, []bool{false})(t, retained)
		for range 2 {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			partitionDetachControlState(t, p, b.DB, b.Recorder, false)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatalf("canonical repeated replay changed: %v\n%s", err, again.String())
			}
		}
		var old []int64
		for retained.Next() {
			var k int64
			if err := retained.Scan(&k); err != nil {
				t.Fatal(err)
			}
			old = append(old, k)
		}
		if retained.Err() != nil || !reflect.DeepEqual(old, []int64{1, 2}) {
			t.Fatal("retained snapshot", old, retained.Err())
		}
		destination := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(destination.Ctx, destination.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		partitionDetachControlState(t, destination, destination.Memory, destination.MemoryRecord, false)
		partitionDetachControlFuture(t, destination, destination.Memory, destination.MemoryRecord)
		run(fixture.Exec("CREATE TABLE pd_sentinel(k INT)", 0))
		run(fixture.Exec("INSERT INTO pd_sentinel VALUES(91)", 1))
		sentinel, err := b.DB.QueryContext(p.Ctx, "SELECT k FROM pd_sentinel")
		if err != nil {
			t.Fatal(err)
		}
		defer sentinel.Close()
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		readErr := errors.New("detach control reader failed")
		if err := stuntgres.Restore(p.Ctx, p.Native, partitionDetachFailReader{readErr}); !errors.Is(err, readErr) {
			t.Fatal("reader error identity", err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &partitionDetachCancelReader{cancel: cancel, reader: bytes.NewReader(dump.Bytes())})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("canceled reader", err)
		}
		malformed := dump.String() + "CREATE TABLE pd_bad("
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(malformed)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(utf8.RuneCountInString(malformed) + 1)})
		refused := dump.String() + "CREATE TABLE public.__stuntgres_partition_replay_fake(k INT); DROP TABLE public.__stuntgres_partition_replay_fake;"
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(refused)), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore accepts only generated empty partition replay teardown"})
		partitionDetachControlState(t, p, b.DB, b.Recorder, false)
		run(referenceRangeRows("SELECT k FROM pd_sentinel", 91))
		if !sentinel.Next() {
			t.Fatal("failed replacement lost retained sentinel")
		}
		var k int64
		if err := sentinel.Scan(&k); err != nil || k != 91 {
			t.Fatal(k, err)
		}
		if sentinel.Next() || sentinel.Err() != nil {
			t.Fatal(sentinel.Err())
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed replacement partially published", err)
		}
		if p.Postgres != nil {
			partitionAttachReplay(t, p, dump.String(), func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
				partitionDetachControlState(t, p, x, r, pg)
				partitionDetachControlFuture(t, p, x, r)
			})
		}
	})
}
