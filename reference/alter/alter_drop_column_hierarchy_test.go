//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterDropColumnExternalHierarchy(t *testing.T) {
	p := fixture.NewPair(t)
	conn, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	record := &fixture.Recorder{}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, record, s) }
	run(fixture.Exec("CREATE TABLE parent(a INT,b TEXT)", 0))
	run(fixture.Exec("CREATE TABLE inherited() INHERITS(parent)", 0))
	run(fixture.Exec("CREATE TABLE local(b TEXT) INHERITS(parent)", 0))
	failure := fixture.ImplicitError("ALTER TABLE inherited DROP b", fixture.Diagnostic{Code: "42P16", Message: `cannot drop inherited column "b"`})
	failure.ErrorStage = "execute"
	run(failure)
	run(fixture.Exec("ALTER TABLE parent DROP b", 0))
	run(fixture.Select("SELECT * FROM inherited", []string{"a"}, []string{"INT4"}, nil))
	run(fixture.Select("SELECT * FROM local", []string{"a", "b"}, []string{"INT4", "TEXT"}, nil))
	run(fixture.Exec("CREATE TABLE p1(b INT)", 0))
	run(fixture.Exec("CREATE TABLE p2(b INT)", 0))
	run(fixture.Exec("CREATE TABLE multi() INHERITS(p1,p2)", 0))
	run(fixture.Exec("ALTER TABLE p1 DROP b", 0))
	run(fixture.Select("SELECT * FROM multi", []string{"b"}, []string{"INT4"}, nil))
	run(fixture.Exec("ALTER TABLE p2 DROP b", 0))
	run(fixture.Select("SELECT * FROM multi", []string{}, []string{}, nil))
	// pg_attribute Table52.7 declares attinhcount int2 (not int4).
	run(fixture.Exec("CREATE TABLE only_parent(b INT)", 0))
	run(fixture.Exec("CREATE TABLE only_child() INHERITS(only_parent)", 0))
	run(fixture.Exec("ALTER TABLE ONLY only_parent DROP b", 0))
	run(fixture.Select("SELECT * FROM only_child", []string{"b"}, []string{"INT4"}, nil))
	run(fixture.Select("SELECT attislocal,attinhcount FROM pg_attribute WHERE attrelid='only_child'::regclass AND attname='b'", []string{"attislocal", "attinhcount"}, []string{"BOOL", "INT2"}, [][]any{{true, int64(0)}}))
	run(fixture.Exec("ALTER TABLE only_child DROP b", 0))
	// RANGE definition/routing/key-safety assertions now live in the untagged
	// TestReferenceRangePartitionHierarchyKeySafety. Retain this external-only
	// recursive non-key propagation witness alongside ordinary INHERITS.
	run(fixture.Exec("CREATE TABLE partitioned(k INT,b TEXT) PARTITION BY RANGE(k)", 0))
	run(fixture.Exec("CREATE TABLE part PARTITION OF partitioned FOR VALUES FROM(0) TO(10)", 0))
	run(fixture.Exec("INSERT INTO partitioned VALUES(1,'keep')", 1))
	run(fixture.Exec("ALTER TABLE partitioned DROP b", 0))
	run(fixture.Select("SELECT * FROM part", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
}
