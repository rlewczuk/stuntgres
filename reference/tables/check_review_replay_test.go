package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckReviewReplayHistory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))", 0))
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(fixture.Exec("CREATE SEQUENCE replay_s START 1", 0))
			run(fixture.Exec("CREATE TABLE replay_t(a INT CONSTRAINT c CHECK(nextval('replay_s')=2))", 0))
			run(fixture.ImplicitError("INSERT INTO replay_t VALUES(9)", fixture.CheckViolation("replay_t", "c", "9")))
			run(fixture.Select("SELECT a FROM replay_t", []string{"a"}, []string{"INT4"}, [][]any{}))
			run(fixture.Select("SELECT currval('replay_s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}

func TestReferenceCheckReviewReplayOwnedSequence(t *testing.T) {
	p := fixture.NewScenario(t)
	var stream []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if b.PG {
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			}
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s MINVALUE -10 START 1", 0))
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0))
			run(fixture.Exec("ALTER SEQUENCE s OWNED BY t.a", 0))
			run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES($1)", 1, 11)))
			run(fixture.Exec("INSERT INTO t VALUES(22)", 1))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			if !b.PG {
				stream = fixture.CheckDump(t, p)
				if !strings.Contains(string(stream), `OWNED BY "public"."t"."a"`) {
					t.Fatal(string(stream))
				}
				fixture.CheckRoundtrip(t, p, stream)
			} else {
				run(fixture.Exec("DROP TABLE t", 0))
				if _, err := conn.ExecContext(p.Ctx, string(stream)); err != nil {
					t.Fatal("owner-bearing PG replay", err)
				}
				var owner string
				if err := conn.QueryRowContext(p.Ctx, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='t'::regclass").Scan(&owner); err != nil || owner != "postgres" {
					t.Fatal(owner, err)
				}
			}
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(22)}}))
			run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(fixture.Select("SELECT setval('s',-1)", []string{"setval"}, []string{"INT8"}, [][]any{{int64(-1)}}))
			run(fixture.Prepared(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "c", "33"), 33)))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(22)}}))
			run(fixture.Exec("DROP TABLE t", 0))
			run(fixture.ImplicitError("SELECT nextval('s')", fixture.Diagnostic{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"}))
		})
	}
}

func TestReferenceCheckReviewReplayHistoryPublicRestore(t *testing.T) {
	p := fixture.NewScenario(t)
	conn, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, p.MemoryRecord, s) }
	run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
	run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')=2))", 0))
	run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
	dump := fixture.CheckDump(t, p)
	run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
	target := fixture.NewFixture(t, false, true)
	fixture.CheckRun(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE sentinel(a INT)", 0))
	fixture.CheckRun(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
	saved, err := target.Memory.QueryContext(target.Ctx, "SELECT a FROM sentinel")
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
	before := fixture.CheckDump(t, target)
	err = stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump))
	fixture.State(t, err, "23514")
	want := fixture.CheckViolation("t", "c", "9")
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	fixture.AssertDiagnostic(t, err, &want)
	if after := fixture.CheckDump(t, target); !bytes.Equal(before, after) {
		t.Fatal("failed history replay changed target")
	}
	fixture.CheckRun(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT a FROM sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
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
