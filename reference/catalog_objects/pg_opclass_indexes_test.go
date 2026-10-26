package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// This witness deliberately runs all existing JSONB hash operations BEFORE the
// new catalog read. It can therefore prove admission against pre-cutover code.
func pgOpclassJSONBAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE opclass_jsonb(id INT,j JSONB)", 0))
	run(fixture.Exec(`INSERT INTO opclass_jsonb VALUES(1,'{"a":1}'),(2,'{"a":1.0}'),(3,'{"b":2}'),(4,NULL)`, 4))
	run(fixture.Exec("CREATE INDEX opclass_jsonb_hash ON opclass_jsonb USING hash(j)", 0))
	q := "SELECT id FROM opclass_jsonb WHERE j=$1::jsonb ORDER BY id"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
	check := func(want [][]any) {
		s := fixture.Select(q, []string{"id"}, []string{"INT4"}, want, `{"a":1}`)
		run(s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	check([][]any{{int64(1)}, {int64(2)}})
	run(fixture.Exec(`UPDATE opclass_jsonb SET j='{"b":2}' WHERE id=1`, 1))
	run(fixture.Exec(`INSERT INTO opclass_jsonb VALUES(5,'{"a":1}')`, 1))
	check([][]any{{int64(2)}, {int64(5)}})
	run(fixture.Exec("DELETE FROM opclass_jsonb WHERE id=2", 1))
	check([][]any{{int64(5)}})
	fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT relam FROM pg_class WHERE relname='opclass_jsonb_hash'", []string{"relam"}, []string{"OID"}, [][]any{{int64(405)}}))
	run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='opclass_jsonb_hash'", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX opclass_jsonb_hash ON public.opclass_jsonb USING hash (j)"}}))
}

func TestReferencePGOpclassJSONBIdentity(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		t.Run("admission", func(t *testing.T) { pgOpclassJSONBAdmission(t, p, b) })
		if t.Failed() {
			return
		}
		t.Run("identity", func(t *testing.T) {
			want := [][]any{
				{int64(10088), int64(403), "jsonb_ops", int64(11), int64(10), int64(4033), int64(3802), true, int64(0)},
				{int64(10089), int64(405), "jsonb_ops", int64(11), int64(10), int64(4034), int64(3802), true, int64(0)},
			}
			for _, prepared := range []bool{false, true} {
				pgOpclassRows(t, p, b, b.DB, "SELECT * FROM pg_opclass WHERE opcname='jsonb_ops' AND opcmethod IN (403,405) ORDER BY oid", want, prepared)
			}
		})
	})
}

func pgOpclassIndexSemantics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	facts := pgOpclassFacts(pgOpclassPGLSNOID(t, p, b))
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE DOMAIN opclass_domain AS INT", 0))
	run(fixture.Exec("CREATE TYPE opclass_enum AS ENUM('a','b')", 0))
	run(fixture.Exec("CREATE TABLE opclass_keys(n INT,v VARCHAR,nm NAME,d opclass_domain,e opclass_enum)", 0))
	run(fixture.Exec("INSERT INTO opclass_keys VALUES(1,'one','one',1,'a'),(2,'two','two',2,'b')", 2))
	for i, tc := range []struct {
		key, method, class, render string
		oid                        int64
	}{
		{"n", "btree", "", "n", 1978},
		{"n", "hash", "pg_catalog.int4_ops", "n", 10020},
		{"v", "btree", "varchar_ops", "v varchar_ops", 10044},
		{"v", "hash", "varchar_ops", "v varchar_ops", 10045},
		{"v", "btree", "varchar_pattern_ops", "v varchar_pattern_ops", 4218},
		{"v", "hash", "varchar_pattern_ops", "v varchar_pattern_ops", 10057},
		{"nm", "btree", "name_ops", "nm", 10028},
		{"nm", "hash", "name_ops", "nm", 10029},
		{"d", "btree", "", "d", 1978},
		{"d", "hash", "", "d", 10020},
		{"e", "btree", "", "e", 10069},
		{"e", "hash", "", "e", 10070},
	} {
		name := fmt.Sprintf("opclass_key_%d", i)
		q := "CREATE INDEX " + name + " ON opclass_keys USING " + tc.method + "(" + tc.key
		if tc.class != "" {
			q += " " + tc.class
		}
		q += ")"
		if i%2 == 0 {
			run(fixture.Exec(q, 0))
		} else {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		method := int64(403)
		if tc.method == "hash" {
			method = 405
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT relam FROM pg_class WHERE relname='"+name+"'", []string{"relam"}, []string{"OID"}, [][]any{{method}}))
		run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='"+name+"'", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX " + name + " ON public.opclass_keys USING " + tc.method + " (" + tc.render + ")"}}))
		var want [][]any
		for _, r := range facts {
			if r[0] == tc.oid {
				want = append(want, r)
			}
		}
		pgOpclassRows(t, p, b, b.DB, "SELECT * FROM pg_opclass WHERE oid=$1::oid", want, true, tc.oid)
	}
	q := "SELECT v FROM opclass_keys WHERE n=$1 ORDER BY v"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"VARCHAR"}, [][]any{{"one"}}, 1))
	run(fixture.Exec("UPDATE opclass_keys SET n=3,v='three',nm='three',d=3,e='b' WHERE n=1", 1))
	run(fixture.Exec("DELETE FROM opclass_keys WHERE n=2", 1))
	run(fixture.Exec("INSERT INTO opclass_keys VALUES(4,'four','four',4,'a')", 1))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"VARCHAR"}, nil, 1))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"VARCHAR"}, [][]any{{"three"}}, 3))
	run(fixture.Select("SELECT n,v,nm,d::int,e::text FROM opclass_keys ORDER BY n", []string{"n", "v", "nm", "d", "e"}, []string{"INT4", "VARCHAR", "NAME", "INT4", "TEXT"}, [][]any{{int64(3), "three", "three", int64(3), "b"}, {int64(4), "four", "four", int64(4), "a"}}))
	pgOpclassInventory(t, p, b)
}

func TestReferencePGOpclassIndexSemantics(t *testing.T) {
	pgOpclassScenario(t, pgOpclassIndexSemantics)
}

func pgOpclassDiagnosticFailures(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	for _, tc := range []struct{ q, pos string }{
		{"SELECT missing FROM pg_catalog.pg_opclass", "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_opclass", "14"},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: tc.pos})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
	}
	run(fixture.Exec("CREATE TABLE opclass_retry(n INT,j JSON)", 0))
	run(fixture.Exec("INSERT INTO opclass_retry VALUES(7,'{}')", 1))
	for _, tc := range []struct{ key, method, code, message, hint string }{
		{"n missing_ops", "btree", "42704", `operator class "missing_ops" does not exist for access method "btree"`, ""},
		{"n missing_ops", "hash", "42704", `operator class "missing_ops" does not exist for access method "hash"`, ""},
		{"n text_ops", "btree", "42804", `operator class "text_ops" does not accept data type integer`, ""},
		{"n text_ops", "hash", "42804", `operator class "text_ops" does not accept data type integer`, ""},
		{"n int4_ops(foo=1)", "btree", "22023", "operator class int4_ops has no options", ""},
		{"n absent_hash_schema.int4_ops", "hash", "3F000", `schema "absent_hash_schema" does not exist`, ""},
		{"j", "hash", "42704", `data type json has no default operator class for access method "hash"`, "You must specify an operator class for the index or define a default operator class for the data type."},
	} {
		q := "CREATE INDEX opclass_failed ON opclass_retry USING " + tc.method + "(" + tc.key + ")"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Prepared(s))
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("quiet prepare", err)
		}
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, b.Recorder, s)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='opclass_failed'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT n FROM opclass_retry", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	run(fixture.Exec("CREATE INDEX opclass_failed ON opclass_retry USING hash(n int4_ops)", 0))
	if !b.PG {
		for _, tc := range []struct{ q, stage string }{
			{"INSERT INTO pg_catalog.pg_opclass VALUES(1,403,'fake',11,10,1976,23,true,0)", "execute"},
			{"UPDATE pg_catalog.pg_opclass SET opcdefault=false", "prepare"},
			{"DELETE FROM pg_catalog.pg_opclass", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				run(s)
			}
		}
	}
	pgOpclassInventory(t, p, b)
}

func TestReferencePGOpclassDiagnostics(t *testing.T) {
	pgOpclassScenario(t, pgOpclassDiagnosticFailures)
}
