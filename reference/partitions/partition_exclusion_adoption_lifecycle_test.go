package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func partitionAdoptionIdentity(t *testing.T, p *fixture.Pair, x interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table string) [2]int64 {
	t.Helper()
	var ids [2]int64
	if err := x.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid=$1::regclass::OID AND contype='x' ORDER BY conname", table).Scan(&ids[0], &ids[1]); err != nil {
		t.Fatal(err)
	}
	if ids[0] <= 0 || ids[1] <= 0 || ids[0] == ids[1] {
		t.Fatal("invalid constraint/backing identities", ids)
	}
	return ids
}

func partitionAdoptionSame(table string, ids [2]int64) fixture.Step {
	return fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index FROM pg_constraint WHERE conrelid='"+table+"'::regclass::OID AND contype='x' AND oid=$1::OID", []string{"same_constraint", "same_index"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, ids[0], ids[1])
}

func partitionAdoptionNamedIdentity(t *testing.T, p *fixture.Pair, x interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, table, constraint string) [2]int64 {
	t.Helper()
	var ids [2]int64
	if err := x.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid=$1::regclass::OID AND contype='x' AND conname=$2", table, constraint).Scan(&ids[0], &ids[1]); err != nil {
		t.Fatal(err)
	}
	if ids[0] <= 0 || ids[1] <= 0 || ids[0] == ids[1] {
		t.Fatal("invalid selected constraint/backing identities", ids)
	}
	return ids
}

func partitionAdoptionRow(table, name string, parent any, local, noinherit bool, kind string, valid bool) []any {
	count := int64(0)
	if !local {
		count = 1
	}
	return []any{table, name, parent, local, count, noinherit, false, false, name, kind, valid, true, true, parent, true, true, true, true}
}

func partitionAdoptionGraph(rows [][]any) fixture.Step {
	return fixture.Select("SELECT t.relname,c.conname,p.conname AS parent,c.conislocal,c.coninhcount,c.connoinherit,c.condeferrable,c.condeferred,i.relname AS backing,i.relkind,x.indisvalid,x.indisready,x.indislive,ip.relname AS index_parent,c.convalidated,c.conenforced,x.indisexclusion,t.relowner=i.relowner AS same_owner FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid LEFT JOIN pg_constraint p ON p.oid=c.conparentid LEFT JOIN pg_inherits h ON h.inhrelid=i.oid LEFT JOIN pg_class ip ON ip.oid=h.inhparent WHERE t.relname LIKE 'pe_%' AND c.contype='x' ORDER BY t.relname,c.conname",
		[]string{"relname", "conname", "parent", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred", "backing", "relkind", "indisvalid", "indisready", "indislive", "index_parent", "convalidated", "conenforced", "indisexclusion", "same_owner"},
		[]string{"NAME", "NAME", "NAME", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "NAME", "CHAR", "BOOL", "BOOL", "BOOL", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, rows)
}

func partitionAdoptionNested(run func(fixture.Step), operation string) {
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	if operation == "attach" {
		run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
		run(fixture.Exec("CREATE TABLE pe_mid(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
	} else {
		run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT PARTITION BY RANGE(tenant)", 0))
	}
	run(fixture.Exec("CREATE TABLE pe_leaf PARTITION OF pe_mid FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
	run(fixture.Exec("ALTER TABLE pe_leaf ADD CONSTRAINT pe_saved EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("ALTER TABLE pe_mid ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
	run(fixture.Exec("CREATE TABLE pe_fresh PARTITION OF pe_mid DEFAULT", 0))
	run(fixture.Exec("INSERT INTO pe_mid VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[1,5)')", 2))
}

func partitionAdoptionNestedRows(attached bool) [][]any {
	var parent any
	if attached {
		parent = "pe_ex"
	}
	rows := [][]any{
		partitionAdoptionRow("pe_fresh", "pe_fresh_tenant_during_excl", "pe_local", false, false, "i", true),
		partitionAdoptionRow("pe_leaf", "pe_saved", "pe_local", false, true, "i", true),
		partitionAdoptionRow("pe_mid", "pe_local", parent, !attached, true, "I", true),
		partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
	}
	return rows
}

func TestReferencePartitionExclusionAdoptionLifecycle(t *testing.T) {
	for _, operation := range []string{"attach", "recursive_add"} {
		for _, ending := range []string{"parent_constraint", "detached_constraint", "child_table", "parent_table"} {
			partitionExclusionCase(t, operation+"/"+ending, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := partitionConstraintRun(t, p, b, prepared)
				partitionAdoptionNested(run, operation)
				middle := partitionAdoptionIdentity(t, p, b.DB, "pe_mid")
				leaf := partitionAdoptionIdentity(t, p, b.DB, "pe_leaf")
				fresh := partitionAdoptionIdentity(t, p, b.DB, "pe_fresh")
				conflictSQL := "INSERT INTO pe_mid VALUES(3,'10.0.0.1','[3,7)')"
				conflictStmt, err := b.DB.PrepareContext(p.Ctx, conflictSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer conflictStmt.Close()
				querySQL := "SELECT id FROM pe_mid ORDER BY id"
				queryStmt, err := b.DB.PrepareContext(p.Ctx, querySQL)
				if err != nil {
					t.Fatal(err)
				}
				defer queryStmt.Close()
				transition := "ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT"
				if operation == "recursive_add" {
					transition = "ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist(" + partitionExclusionKeys + ")"
				}
				ddl, err := b.DB.PrepareContext(p.Ctx, transition)
				if err != nil {
					t.Fatal(err)
				}
				defer ddl.Close()
				if prepared {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: ddl, SQL: transition}, b.Recorder, fixture.Exec(transition, 0))
				} else {
					run(fixture.Exec(transition, 0))
				}
				check := func(attached bool) {
					t.Helper()
					run(partitionAdoptionSame("pe_mid", middle))
					run(partitionAdoptionSame("pe_leaf", leaf))
					run(partitionAdoptionSame("pe_fresh", fresh))
					run(partitionAdoptionGraph(partitionAdoptionNestedRows(attached)))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: conflictStmt, SQL: conflictSQL}, b.Recorder, partitionExclusionConflict(conflictSQL, "pe_leaf", "pe_saved", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: queryStmt, SQL: querySQL}, b.Recorder, fixture.Select(querySQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				}
				check(true)
				run(partitionConstraintEdge("pe_local", "pe_ex", "pe_mid"))
				run(partitionConstraintEdge("pe_saved", "pe_local", "pe_leaf"))
				run(referenceRangeError("ALTER TABLE pe_mid DROP CONSTRAINT pe_local", "42P16", `cannot drop inherited constraint "pe_local" of relation "pe_mid"`, fixture.Diagnostic{}))
				for range 2 {
					run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_mid", 0))
					check(false)
					run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT", 0))
					check(true)
				}
				switch ending {
				case "parent_constraint":
					run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
					run(partitionAdoptionGraph(nil))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: conflictStmt, SQL: conflictSQL}, b.Recorder, fixture.Exec(conflictSQL, 1))
				case "detached_constraint":
					run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_mid", 0))
					run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
					rows := partitionAdoptionNestedRows(false)
					run(partitionAdoptionGraph(rows[:3]))
					run(partitionAdoptionSame("pe_leaf", leaf))
					run(partitionExclusionConflict(conflictSQL, "pe_leaf", "pe_saved", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
					run(fixture.Exec("ALTER TABLE pe_mid DROP CONSTRAINT pe_local", 0))
					run(partitionAdoptionGraph(nil))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: conflictStmt, SQL: conflictSQL}, b.Recorder, fixture.Exec(conflictSQL, 1))
				case "child_table":
					run(fixture.Exec("DROP TABLE pe_leaf", 0))
					rows := partitionAdoptionNestedRows(true)
					run(partitionAdoptionGraph([][]any{rows[0], rows[2], rows[3]}))
					run(partitionAdoptionSame("pe_mid", middle))
					run(partitionExclusionConflict("INSERT INTO pe_root VALUES(3,'10.0.0.129','[3,7)')", "pe_fresh", "pe_fresh_tenant_during_excl", "10.0.0.129, [3,7)", "10.0.0.129, [1,5)"))
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: queryStmt, SQL: querySQL}, b.Recorder, fixture.Select(querySQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				case "parent_table":
					run(fixture.Exec("DROP TABLE pe_root", 0))
					run(partitionAdoptionGraph(nil))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pe_root','pe_mid','pe_leaf','pe_fresh','pe_ex','pe_local','pe_saved','pe_fresh_tenant_during_excl') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
				}
			})
		}
	}
	partitionExclusionCase(t, "adoption_savepoint", partitionAdoptionSavepoint)
}

func partitionAdoptionSavepoint(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	c := fixture.ConcurrentIndexConn(t, p, b)
	run := func(s fixture.Step) { s.Prepared = prepared; fixture.ExclusionReferenceRun(t, p, b, c, s) }
	run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
	run(fixture.Exec("CREATE TABLE pe_child(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+"))", 0))
	run(fixture.Exec("INSERT INTO pe_child VALUES(1,'10.0.0.1','[1,5)')", 1))
	ids := partitionAdoptionIdentity(t, p, c, "pe_child")
	old := [][]any{partitionAdoptionRow("pe_child", "pe_local", nil, true, true, "i", true), partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true)}
	q := "ALTER TABLE pe_root ATTACH PARTITION pe_child DEFAULT"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, rollback := range []string{"ROLLBACK TO before_adoption", "ROLLBACK"} {
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_adoption", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
		run(partitionAdoptionSame("pe_child", ids))
		run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_child", "pe_local", "pe_ex", false, true, "i", true), old[1]}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, partitionAdoptionGraph(old))
		run(fixture.Exec(rollback, 0))
		run(partitionAdoptionSame("pe_child", ids))
		run(partitionAdoptionGraph(old))
		run(partitionExclusionConflict("INSERT INTO pe_child VALUES(2,'10.0.0.1','[3,7)')", "pe_child", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		if rollback != "ROLLBACK" {
			run(fixture.ImplicitError("SELECT id FROM pe_child", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(fixture.Exec("ROLLBACK", 0))
		}
	}
	run(fixture.Exec(q, 0))
	run(partitionAdoptionSame("pe_child", ids))
}
