package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCheckReviewReplayHistory(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE SEQUENCE s START 1", 0))
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))", 0))
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(referenceExec("INSERT INTO t VALUES(9)", 1))
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(referenceExec("CREATE SEQUENCE replay_s START 1", 0))
			run(referenceExec("CREATE TABLE replay_t(a INT CONSTRAINT c CHECK(nextval('replay_s')=2))", 0))
			run(referenceImplicitError("INSERT INTO replay_t VALUES(9)", referenceCheckViolation("replay_t", "c", "9")))
			run(referenceSelect("SELECT a FROM replay_t", []string{"a"}, []string{"INT4"}, [][]any{}))
			run(referenceSelect("SELECT currval('replay_s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}

func TestReferenceCheckReviewReplayOwnedSequence(t *testing.T) {
	p := newReferenceScenario(t)
	var stream []byte
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if b.pg {
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			}
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
			run(referenceExec("ALTER SEQUENCE s OWNED BY t.a", 0))
			run(referencePrepared(referenceExec("INSERT INTO t VALUES($1)", 1, 11)))
			run(referenceExec("INSERT INTO t VALUES(22)", 1))
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			if !b.pg {
				stream = referenceCheckDump(t, p)
				if !strings.Contains(string(stream), `OWNED BY "public"."t"."a"`) {
					t.Fatal(string(stream))
				}
				referenceCheckRoundtrip(t, p, stream)
			} else {
				run(referenceExec("DROP TABLE t", 0))
				if _, err := conn.ExecContext(p.ctx, string(stream)); err != nil {
					t.Fatal("owner-bearing PG replay", err)
				}
				var owner string
				if err := conn.QueryRowContext(p.ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='t'::regclass").Scan(&owner); err != nil || owner != "postgres" {
					t.Fatal(owner, err)
				}
			}
			run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(22)}}))
			run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(referenceSelect("SELECT setval('s',-1)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(-1)}}))
			run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "33"), 33)))
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(22)}}))
			run(referenceExec("DROP TABLE t", 0))
			run(referenceImplicitError("SELECT nextval('s')", referenceDiagnostic{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}))
		})
	}
}

func TestReferenceCheckReviewReplayHistoryPublicRestore(t *testing.T) {
	p := newReferenceScenario(t)
	conn, err := p.memory.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, p.memoryRecord, s) }
	run(referenceExec("CREATE SEQUENCE s START 1", 0))
	run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))", 0))
	run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(referenceExec("INSERT INTO t VALUES(9)", 1))
	dump := referenceCheckDump(t, p)
	run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
	target := newReferenceFixture(t, false, true)
	referenceCheckRun(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE TABLE sentinel(a INT)", 0))
	referenceCheckRun(t, target.ctx, target.memory, target.memoryRecord, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
	saved, err := target.memory.QueryContext(target.ctx, "SELECT a FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	types, err := saved.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != "a" || types[0].DatabaseTypeName() != "INT4" {
		t.Fatal(types, err)
	}
	if nullable, known := types[0].Nullable(); !nullable || !known {
		t.Fatal("lost sentinel nullability")
	}
	before := referenceCheckDump(t, target)
	err = stuntgres.Restore(target.ctx, target.native, bytes.NewReader(dump))
	referenceState(t, err, "23514")
	want := referenceCheckViolation("t", "c", "9")
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, err, &want)
	if after := referenceCheckDump(t, target); !bytes.Equal(before, after) {
		t.Fatal("failed history replay changed target")
	}
	referenceCheckRun(t, target.ctx, target.memory, target.memoryRecord, referenceSelect("SELECT a FROM sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	if !saved.Next() {
		t.Fatal(saved.Err())
	}
	var value int64
	if err := saved.Scan(&value); err != nil || value != 7 {
		t.Fatal(value, err)
	}
	if saved.Next() || saved.Err() != nil {
		t.Fatal("changed retained cursor", saved.Err())
	}
	if err := saved.Close(); err != nil {
		t.Fatal(err)
	}
}
