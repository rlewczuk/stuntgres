package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func indexMaintenanceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func indexMaintenanceRows() fixture.Step {
	return fixture.Select("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "z"}, {int64(2), "b"}, {int64(2), "b"}, {int64(4), "c"}, {nil, nil}})
}

func indexMaintenanceSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(n INT,s TEXT)", 0),
		fixture.Exec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
		fixture.Exec("CREATE INDEX ni ON t(n)", 0),
		fixture.Exec("CREATE INDEX si ON t(s DESC NULLS LAST,n)", 0),
		fixture.Exec("CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", 0),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func TestReferenceIndexMaintenanceLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "imoved")
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		indexMaintenanceSeed(t, p, b)
		var id int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ni'").Scan(&id); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER INDEX ni RENAME TO new_ni", 0))
		run(fixture.Select("SELECT oid::bigint,relkind FROM pg_class WHERE relname='new_ni'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{id, "i"}}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ni'", []string{"relname"}, []string{"NAME"}, nil))
		for _, s := range []fixture.Step{fixture.Exec("UPDATE t SET n=4 WHERE n=3", 1), fixture.Exec("DELETE FROM t WHERE n=1", 1), fixture.Exec("INSERT INTO t VALUES(0,'z')", 1)} {
			run(s)
		}
		for _, q := range []string{"REINDEX INDEX new_ni", "REINDEX TABLE t"} {
			for _, plain := range []bool{false, true} {
				s := fixture.Prepared(fixture.Exec(q, 0))
				s.NonContext = plain
				run(s)
				s = indexMaintenanceRows()
				s.NonContext = plain
				run(s)
			}
		}
		run(fixture.Exec("DROP INDEX new_ni", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='new_ni'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT pg_get_indexdef('si'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX si ON public.t USING btree (s DESC NULLS LAST, n)"}}))
		run(indexMaintenanceRows())
		run(fixture.Exec("CREATE INDEX new_ni ON t(n)", 0))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN n TO number", 0))
		run(fixture.Exec("ALTER TABLE t RENAME TO renamed_t", 0))
		run(fixture.Exec("CREATE SCHEMA imoved", 0))
		run(fixture.Exec("ALTER TABLE renamed_t SET SCHEMA imoved", 0))
		run(fixture.Select("SELECT pg_get_indexdef('imoved.new_ni'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX new_ni ON imoved.renamed_t USING btree (number)"}}))
		run(fixture.Exec("DROP TABLE imoved.renamed_t", 0))
		for _, name := range []string{"si", "ei", "new_ni"} {
			s := fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil)
			s.Args = []any{name}
			run(s)
		}
	})
}

func TestReferenceIndexMaintenanceUnique(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0))
		run(fixture.Exec("INSERT INTO q VALUES(1),(NULL)", 2))
		var index, constraint int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='u'").Scan(&index); err != nil {
			t.Fatal(err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='u'").Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER INDEX u RENAME TO u_new", 0))
		run(fixture.Select("SELECT oid::bigint,relkind FROM pg_class WHERE relname='u_new'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
		run(fixture.Select("SELECT oid::bigint,conname FROM pg_constraint WHERE conname='u_new'", []string{"oid", "conname"}, []string{"INT8", "NAME"}, [][]any{{constraint, "u_new"}}))
		run(fixture.Select("SELECT conindid::bigint FROM pg_constraint WHERE conname='u_new'", []string{"conindid"}, []string{"INT8"}, [][]any{{index}}))
		for _, q := range []string{"REINDEX INDEX u_new", "REINDEX TABLE q"} {
			run(fixture.Prepared(fixture.Exec(q, 0)))
		}
		for _, c := range []struct{ value, detail string }{{"1", "Key (n)=(1) already exists."}, {"NULL", "Key (n)=(null) already exists."}} {
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO q VALUES("+c.value+")", fixture.UniqueReferenceDuplicate("q", "u_new", c.detail))))
		}
		for _, mode := range []string{"RESTRICT", "CASCADE"} {
			run(fixture.ImplicitError("DROP INDEX u_new "+mode, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index u_new because constraint u_new on table q requires it", Hint: "You can drop constraint u_new on table q instead."}))
		}
		run(fixture.Select("SELECT n FROM q ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
	})
}

func TestReferenceIndexMaintenanceSmoke(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		indexMaintenanceSeed(t, p, b)
		run(fixture.Exec("ALTER INDEX ni RENAME TO new_ni", 0))
		run(fixture.Exec("UPDATE t SET n=4 WHERE n=3", 1))
		run(fixture.Exec("DELETE FROM t WHERE n=1", 1))
		run(fixture.Exec("INSERT INTO t VALUES(0,'z')", 1))
		stmt, err := b.DB.PrepareContext(p.Ctx, "REINDEX INDEX new_ni")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: "REINDEX INDEX new_ni"}, b.Recorder, fixture.Exec("REINDEX INDEX new_ni", 0))
		run(fixture.Exec("REINDEX TABLE t", 0))
		run(indexMaintenanceRows())
		run(fixture.Exec("DROP INDEX new_ni", 0))
		run(fixture.ImplicitError("DROP INDEX si,missing", fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`}))
		for _, name := range []string{"ei", "si"} {
			s := fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}})
			s.Args = []any{name}
			run(s)
		}
		run(fixture.ImplicitError("ALTER INDEX si SET(fillfactor=9)", fixture.Diagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}))
		run(fixture.Exec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE)", 0))
		run(fixture.Exec("INSERT INTO q VALUES(1)", 1))
		run(fixture.Exec("ALTER INDEX u RENAME TO u_new", 0))
		run(fixture.Prepared(fixture.Exec("REINDEX INDEX u_new", 0)))
		run(fixture.ErrorDiagnostic("INSERT INTO q VALUES(1)", fixture.UniqueReferenceDuplicate("q", "u_new", "Key (n)=(1) already exists.")))
		run(fixture.ImplicitError("DROP INDEX u_new CASCADE", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index u_new because constraint u_new on table q requires it", Hint: "You can drop constraint u_new on table q instead."}))
		run(fixture.Exec("ALTER INDEX si SET(fillfactor=80)", 0))
	})
}

// Keep explicit native eager-cursor expectations separate from pq streaming.
func indexMaintenanceRetainedRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("retained rows %v", got)
	}
}

func TestReferenceIndexMaintenanceUniqueDomains(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_n AS INT",
			"CREATE DOMAIN nested_n AS domain_n",
			"CREATE DOMAIN domain_s AS TEXT",
			`CREATE TABLE q(n nested_n CONSTRAINT un UNIQUE NULLS NOT DISTINCT,s domain_s COLLATE "C" CONSTRAINT us UNIQUE NULLS NOT DISTINCT)`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO q VALUES(1,'a'),(NULL,NULL)", 2))
		var index, constraint int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_class WHERE relname='un'").Scan(&index); err != nil {
			t.Fatal(err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='un'").Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("ALTER INDEX un RENAME TO renamed_un", 0))
		for _, q := range []string{"REINDEX INDEX renamed_un", "REINDEX INDEX us", "REINDEX TABLE q"} {
			run(fixture.Prepared(fixture.Exec(q, 0)))
			run(fixture.Select("SELECT oid::bigint,relkind FROM pg_class WHERE relname='renamed_un'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
			run(fixture.Select("SELECT oid::bigint,conname FROM pg_constraint WHERE conname='renamed_un'", []string{"oid", "conname"}, []string{"INT8", "NAME"}, [][]any{{constraint, "renamed_un"}}))
		}
		run(fixture.Select("SELECT n::int4 AS n,s::text AS s FROM q ORDER BY q.n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {nil, nil}}))
		if b.PG {
			for _, c := range []struct{ name, key, class, collation string }{{"renamed_un", "1", "1978", "0"}, {"us", "2", "3126", "950"}} {
				s := fixture.Select(`SELECT i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text,i.indexrelid=co.conindid AS same_backing FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_constraint co ON co.conindid=i.indexrelid WHERE c.relname=$1`, []string{"indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indkey", "indclass", "indcollation", "indoption", "same_backing"}, []string{"INT2", "INT2", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), int64(1), true, true, c.key, c.class, c.collation, "0", true}})
				s.Args = []any{c.name}
				run(s)
			}
		}
		for _, c := range []struct{ values, constraint, detail string }{
			{"(1,'b')", "renamed_un", "Key (n)=(1) already exists."},
			{"(2,'a')", "us", "Key (s)=(a) already exists."},
			{"(NULL,'b')", "renamed_un", "Key (n)=(null) already exists."},
		} {
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO q VALUES"+c.values, fixture.UniqueReferenceDuplicate("q", c.constraint, c.detail))))
		}
		run(fixture.ImplicitError("DROP INDEX renamed_un CASCADE", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index renamed_un because constraint renamed_un on table q requires it", Hint: "You can drop constraint renamed_un on table q instead."}))
		run(fixture.Select("SELECT n::int4 AS n,s::text AS s FROM q ORDER BY q.n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {nil, nil}}))
	})
}
