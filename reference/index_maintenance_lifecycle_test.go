package reference_test

import (
	"database/sql"
	"testing"
)

func indexMaintenanceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func indexMaintenanceRows() referenceStep {
	return referenceSelect("SELECT n,s FROM t ORDER BY n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "z"}, {int64(2), "b"}, {int64(2), "b"}, {int64(4), "c"}, {nil, nil}})
}

func indexMaintenanceSeed(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE t(n INT,s TEXT)", 0),
		referenceExec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
		referenceExec("CREATE INDEX ni ON t(n)", 0),
		referenceExec("CREATE INDEX si ON t(s DESC NULLS LAST,n)", 0),
		referenceExec("CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", 0),
	} {
		referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	}
}

func TestReferenceIndexMaintenanceLifecycle(t *testing.T) {
	schemaReferenceCleanup(t, "imoved")
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		indexMaintenanceSeed(t, p, b)
		var id int64
		if err := b.db.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ni'").Scan(&id); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("ALTER INDEX ni RENAME TO new_ni", 0))
		run(referenceSelect("SELECT oid::bigint,relkind FROM pg_class WHERE relname='new_ni'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{id, "i"}}))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='ni'", []string{"relname"}, []string{"NAME"}, nil))
		for _, s := range []referenceStep{referenceExec("UPDATE t SET n=4 WHERE n=3", 1), referenceExec("DELETE FROM t WHERE n=1", 1), referenceExec("INSERT INTO t VALUES(0,'z')", 1)} {
			run(s)
		}
		for _, q := range []string{"REINDEX INDEX new_ni", "REINDEX TABLE t"} {
			for _, plain := range []bool{false, true} {
				s := referencePrepared(referenceExec(q, 0))
				s.nonContext = plain
				run(s)
				s = indexMaintenanceRows()
				s.nonContext = plain
				run(s)
			}
		}
		run(referenceExec("DROP INDEX new_ni", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='new_ni'", []string{"relname"}, []string{"NAME"}, nil))
		run(referenceSelect("SELECT pg_get_indexdef('si'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX si ON public.t USING btree (s DESC NULLS LAST, n)"}}))
		run(indexMaintenanceRows())
		run(referenceExec("CREATE INDEX new_ni ON t(n)", 0))
		run(referenceExec("ALTER TABLE t RENAME COLUMN n TO number", 0))
		run(referenceExec("ALTER TABLE t RENAME TO renamed_t", 0))
		run(referenceExec("CREATE SCHEMA imoved", 0))
		run(referenceExec("ALTER TABLE renamed_t SET SCHEMA imoved", 0))
		run(referenceSelect("SELECT pg_get_indexdef('imoved.new_ni'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX new_ni ON imoved.renamed_t USING btree (number)"}}))
		run(referenceExec("DROP TABLE imoved.renamed_t", 0))
		for _, name := range []string{"si", "ei", "new_ni"} {
			s := referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil)
			s.args = []any{name}
			run(s)
		}
	})
}

func TestReferenceIndexMaintenanceUnique(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0))
		run(referenceExec("INSERT INTO q VALUES(1),(NULL)", 2))
		var index, constraint int64
		if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE relname='u'").Scan(&index); err != nil {
			t.Fatal(err)
		}
		if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='u'").Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("ALTER INDEX u RENAME TO u_new", 0))
		run(referenceSelect("SELECT oid::bigint,relkind FROM pg_class WHERE relname='u_new'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
		run(referenceSelect("SELECT oid::bigint,conname FROM pg_constraint WHERE conname='u_new'", []string{"oid", "conname"}, []string{"INT8", "NAME"}, [][]any{{constraint, "u_new"}}))
		if b.pg {
			// Native exposes independent relation/constraint identity, not conindid.
			// Its internal backing-descriptor units prove this relationship directly.
			run(referenceSelect("SELECT conindid::bigint FROM pg_constraint WHERE conname='u_new'", []string{"conindid"}, []string{"INT8"}, [][]any{{index}}))
		}
		for _, q := range []string{"REINDEX INDEX u_new", "REINDEX TABLE q"} {
			run(referencePrepared(referenceExec(q, 0)))
		}
		for _, c := range []struct{ value, detail string }{{"1", "Key (n)=(1) already exists."}, {"NULL", "Key (n)=(null) already exists."}} {
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO q VALUES("+c.value+")", uniqueReferenceDuplicate("q", "u_new", c.detail))))
		}
		for _, mode := range []string{"RESTRICT", "CASCADE"} {
			run(referenceImplicitError("DROP INDEX u_new "+mode, referenceDiagnostic{Code: "2BP01", Message: "cannot drop index u_new because constraint u_new on table q requires it", Hint: "You can drop constraint u_new on table q instead."}))
		}
		run(referenceSelect("SELECT n FROM q ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}))
	})
}

func TestReferenceIndexMaintenanceSmoke(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		indexMaintenanceSeed(t, p, b)
		run(referenceExec("ALTER INDEX ni RENAME TO new_ni", 0))
		run(referenceExec("UPDATE t SET n=4 WHERE n=3", 1))
		run(referenceExec("DELETE FROM t WHERE n=1", 1))
		run(referenceExec("INSERT INTO t VALUES(0,'z')", 1))
		stmt, err := b.db.PrepareContext(p.ctx, "REINDEX INDEX new_ni")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: "REINDEX INDEX new_ni"}, b.recorder, referenceExec("REINDEX INDEX new_ni", 0))
		run(referenceExec("REINDEX TABLE t", 0))
		run(indexMaintenanceRows())
		run(referenceExec("DROP INDEX new_ni", 0))
		run(referenceImplicitError("DROP INDEX si,missing", referenceDiagnostic{Code: "42704", Message: `index "missing" does not exist`}))
		for _, name := range []string{"ei", "si"} {
			s := referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{name}})
			s.args = []any{name}
			run(s)
		}
		run(referenceImplicitError("ALTER INDEX si SET(fillfactor=9)", referenceDiagnostic{Code: "22023", Message: `value 9 out of bounds for option "fillfactor"`, Detail: `Valid values are between "10" and "100".`}))
		run(referenceExec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE)", 0))
		run(referenceExec("INSERT INTO q VALUES(1)", 1))
		run(referenceExec("ALTER INDEX u RENAME TO u_new", 0))
		run(referencePrepared(referenceExec("REINDEX INDEX u_new", 0)))
		run(referenceErrorDiagnostic("INSERT INTO q VALUES(1)", uniqueReferenceDuplicate("q", "u_new", "Key (n)=(1) already exists.")))
		run(referenceImplicitError("DROP INDEX u_new CASCADE", referenceDiagnostic{Code: "2BP01", Message: "cannot drop index u_new because constraint u_new on table q requires it", Hint: "You can drop constraint u_new on table q instead."}))
		s := referenceExec("ALTER INDEX si SET(fillfactor=80)", 0)
		if !b.pg {
			s = referenceImplicitError(s.query, referenceDiagnostic{Code: "0A000", Message: "index storage parameters are unsupported"})
		}
		run(s)
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
	indexMaintenanceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_n AS INT",
			"CREATE DOMAIN nested_n AS domain_n",
			"CREATE DOMAIN domain_s AS TEXT",
			`CREATE TABLE q(n nested_n CONSTRAINT un UNIQUE NULLS NOT DISTINCT,s domain_s COLLATE "C" CONSTRAINT us UNIQUE NULLS NOT DISTINCT)`,
		} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO q VALUES(1,'a'),(NULL,NULL)", 2))
		var index, constraint int64
		if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_class WHERE relname='un'").Scan(&index); err != nil {
			t.Fatal(err)
		}
		if err := b.db.QueryRowContext(p.ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='un'").Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("ALTER INDEX un RENAME TO renamed_un", 0))
		for _, q := range []string{"REINDEX INDEX renamed_un", "REINDEX INDEX us", "REINDEX TABLE q"} {
			run(referencePrepared(referenceExec(q, 0)))
			run(referenceSelect("SELECT oid::bigint,relkind FROM pg_class WHERE relname='renamed_un'", []string{"oid", "relkind"}, []string{"INT8", "CHAR"}, [][]any{{index, "i"}}))
			run(referenceSelect("SELECT oid::bigint,conname FROM pg_constraint WHERE conname='renamed_un'", []string{"oid", "conname"}, []string{"INT8", "NAME"}, [][]any{{constraint, "renamed_un"}}))
		}
		run(referenceSelect("SELECT n::int4 AS n,s::text AS s FROM q ORDER BY q.n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {nil, nil}}))
		if b.pg {
			for _, c := range []struct{ name, key, class, collation string }{{"renamed_un", "1", "1978", "0"}, {"us", "2", "3126", "950"}} {
				s := referenceSelect(`SELECT i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text,i.indexrelid=co.conindid AS same_backing FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_constraint co ON co.conindid=i.indexrelid WHERE c.relname=$1`, []string{"indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indkey", "indclass", "indcollation", "indoption", "same_backing"}, []string{"INT2", "INT2", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(1), int64(1), true, true, c.key, c.class, c.collation, "0", true}})
				s.args = []any{c.name}
				run(s)
			}
		}
		for _, c := range []struct{ values, constraint, detail string }{
			{"(1,'b')", "renamed_un", "Key (n)=(1) already exists."},
			{"(2,'a')", "us", "Key (s)=(a) already exists."},
			{"(NULL,'b')", "renamed_un", "Key (n)=(null) already exists."},
		} {
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO q VALUES"+c.values, uniqueReferenceDuplicate("q", c.constraint, c.detail))))
		}
		run(referenceImplicitError("DROP INDEX renamed_un CASCADE", referenceDiagnostic{Code: "2BP01", Message: "cannot drop index renamed_un because constraint renamed_un on table q requires it", Hint: "You can drop constraint renamed_un on table q instead."}))
		run(referenceSelect("SELECT n::int4 AS n,s::text AS s FROM q ORDER BY q.n NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {nil, nil}}))
	})
}
