package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func regtypeValuesSetup(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder) {
	t.Helper()
	regtypeSetup(t, p, conn, recorder)
	for _, q := range []string{
		"CREATE DOMAIN regtype_a.identifier AS regtype",
		"CREATE TABLE regtype_a.values_subject(id integer NOT NULL,r regtype,d regtype_a.identifier,a regtype[],fallback regtype DEFAULT 'regtype_a.same'::regtype)",
	} {
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO regtype_a.values_subject(id,r,d,a) VALUES(1,'integer','regtype_a.same','{integer,regtype_b.same,NULL}'),(2,NULL,NULL,NULL),(3,'4294967295','-','{}')", 3))
}

func regtypeValuesRows(postgres bool) referenceStep {
	s := referenceSelect("SELECT id,r,d,a,fallback FROM regtype_a.values_subject ORDER BY id", []string{"id", "r", "d", "a", "fallback"}, []string{"INT4", "REGTYPE", "REGTYPE", "_REGTYPE", "REGTYPE"}, [][]any{
		{int64(1), "integer", "same", "{integer,regtype_b.same,NULL}", "same"},
		{int64(2), nil, nil, nil, "same"},
		{int64(3), "4294967295", "-", "{}", "same"},
	})
	s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
		regtypeCursorMetadata(t, rows, postgres, s.columns, s.types, []bool{false, true, true, true, true})
	}
	return s
}

func TestReferenceRegtypeValues(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeValuesSetup(t, p, conn, recorder)
		regtypeBoth(t, p, conn, recorder, regtypeValuesRows(backend.pg))
		for _, predicate := range []string{"id=99", "id=2"} {
			s := referenceSelect("SELECT r,d,a FROM regtype_a.values_subject WHERE "+predicate, []string{"r", "d", "a"}, []string{"REGTYPE", "REGTYPE", "_REGTYPE"}, [][]any{})
			if predicate == "id=2" {
				s.rows = [][]any{{nil, nil, nil}}
			}
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				regtypeCursorMetadata(t, rows, backend.pg, s.columns, s.types, []bool{true, true, true})
			}
			regtypeBoth(t, p, conn, recorder, s)
		}
		s := referenceSelect("SELECT r,r,r FROM regtype_a.values_subject ORDER BY id", []string{"r", "r", "r"}, []string{"REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{"integer", "integer", "integer"}, {nil, nil, nil}, {"4294967295", "4294967295", "4294967295"}})
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			regtypeCursorMetadata(t, rows, backend.pg, s.columns, s.types, []bool{true, true, true})
		}
		s.checkScan = func(t *testing.T, rows *sql.Rows, index int) { regtypeScan(t, rows, index, backend.pg) }
		regtypeBoth(t, p, conn, recorder, s)
		for i, value := range []any{"integer", []byte("regtype_b.same"), nil} {
			referenceRunStep(t, p.ctx, conn, recorder, referencePrepared(referenceExec("INSERT INTO regtype_a.values_subject(id,r) VALUES($1,$2)", 1, int64(i+4), value)))
		}
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT id,r FROM regtype_a.values_subject WHERE id>=4 ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(4), "integer"}, {int64(5), "regtype_b.same"}, {int64(6), nil}}))
		for _, s := range []referenceStep{
			referenceSelect("SELECT CASE WHEN id=1 THEN r ELSE fallback END AS r,COALESCE(d,r) AS d FROM regtype_a.values_subject WHERE id<=3 ORDER BY id", []string{"r", "d"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"integer", "same"}, {"same", nil}, {"same", "-"}}),
			referenceSelect("SELECT '0'::regtype<'23'::regtype AS a,'23'::regtype<'4294967295'::regtype AS b,'int4'::regtype='integer'::regtype AS c", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}),
			referenceSelect("SELECT r,count(*) AS n FROM regtype_a.values_subject WHERE id<=3 GROUP BY r ORDER BY r", []string{"r", "n"}, []string{"REGTYPE", "INT8"}, [][]any{{"integer", int64(1)}, {"4294967295", int64(1)}, {nil, int64(1)}}),
			referenceSelect("UPDATE regtype_a.values_subject SET r='name' WHERE id=4 RETURNING id,r", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(4), "name"}}),
			referenceSelect("DELETE FROM regtype_a.values_subject WHERE id=6 RETURNING id,r", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(6), nil}}),
		} {
			regtypeRunStep(t, p.ctx, conn, recorder, s)
		}
		// Late recursive conversion must not publish any of the first valid element.
		result, err := conn.ExecContext(p.ctx, "INSERT INTO regtype_a.values_subject(id,a) VALUES(7,$1)", "{integer,missing_regtype_name}")
		if result != nil {
			t.Error("invalid recursive frame exposed result")
		}
		regtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, "INSERT INTO regtype_a.values_subject(id,a) VALUES(7,$1)", "late-array-frame", "execute")
		regtypeBoth(t, p, conn, recorder, referenceSelect("SELECT count(*) AS n FROM regtype_a.values_subject WHERE id=7", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceRegtypeHeldFrames(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		for _, query := range []string{"SELECT $1::regtype AS r", "SELECT $1::text::regtype AS r"} {
			held, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			executor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}
			for _, value := range []any{"same", "missing_regtype_name", nil, []byte("pg_catalog.int4"), "same"} {
				if value == "missing_regtype_name" {
					rows, err := held.QueryContext(p.ctx, value)
					if rows != nil {
						rows.Close()
						t.Error("failed held query returned cursor")
					}
					regtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, query, "same-held-recovery", "execute")
					continue
				}
				expected := any("integer")
				if value == nil {
					expected = nil
				}
				if value == "same" {
					expected = "same"
				}
				regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{expected}}, value))
			}
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_b,regtype_a,public", 0))
			regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"same"}}, "same"))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_a,regtype_b,public", 0))
			held.Close()
		}
	})
}

func TestReferenceRegtypeHeldLiterals(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		regtypeSetup(t, p, conn, recorder)
		var a, b int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.same'::regtype::bigint,'regtype_b.same'::regtype::bigint").Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		if a == 0 || b == 0 || a == b {
			t.Fatal("distinct user type identities", a, b)
		}
		const query = "SELECT 'same'::regtype::bigint AS oid,'same'::regtype AS r"
		held, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}
		observe := func(phase string, oid int64, output string) {
			t.Run(phase, func(t *testing.T) {
				regtypeRunStep(t, p.ctx, executor, recorder, referenceSelect(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{oid, output}}))
			})
		}
		observe("initial", a, "same")
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_b,regtype_a,public", 0))
		observe("reverse-path", b, "same")
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("SET search_path TO regtype_a,regtype_b,public", 0))
		observe("restore-path", a, "same")
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("DROP DOMAIN regtype_a.same", 0))
		observe("drop-A", a, fmt.Sprint(a))
		referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE DOMAIN regtype_a.same AS integer", 0))
		var recreated int64
		if err := conn.QueryRowContext(p.ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if recreated == a {
			t.Fatal("recreated type reused dropped identity", a)
		}
		observe("recreate-A", a, fmt.Sprint(a))
	})
}

func TestReferenceRegtypeWarnings(t *testing.T) {
	regtypeScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		const query = "SELECT 'timestamp(7)'::regtype AS a"
		*notices = nil
		held, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		regtypeAssertNotices(t, *notices, []referenceDiagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
		*notices = nil
		for range 2 {
			regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: held, query: query}, recorder, regtypeSelect(query, "timestamp without time zone"))
			regtypeAssertNotices(t, *notices, nil)
			*notices = nil
		}
		for _, q := range []string{"SELECT $1::regtype AS a", "SELECT $1::text::regtype AS a"} {
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			regtypeAssertNotices(t, *notices, nil)
			*notices = nil
			for range 2 {
				s := regtypeSelect(q, "timestamp without time zone")
				s.args = []any{"timestamp(7)"}
				regtypeRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}, recorder, s)
				want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
				if q == "SELECT $1::regtype AS a" {
					want.Context = "unnamed portal parameter $1 = '...'"
				}
				regtypeAssertNotices(t, *notices, []referenceDiagnostic{want})
				*notices = nil
			}
			stmt.Close()
		}
	})
}
