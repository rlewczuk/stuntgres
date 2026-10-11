package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func regtypeValuesSetup(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RegtypeSetup(t, p, conn, recorder)
	for _, q := range []string{
		"CREATE DOMAIN regtype_a.identifier AS regtype",
		"CREATE TABLE regtype_a.values_subject(id integer NOT NULL,r regtype,d regtype_a.identifier,a regtype[],fallback regtype DEFAULT 'regtype_a.same'::regtype)",
	} {
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.values_subject(id,r,d,a) VALUES(1,'integer','regtype_a.same','{integer,regtype_b.same,NULL}'),(2,NULL,NULL,NULL),(3,'4294967295','-','{}')", 3))
}

func regtypeValuesRows(postgres bool) fixture.Step {
	s := fixture.Select("SELECT id,r,d,a,fallback FROM regtype_a.values_subject ORDER BY id", []string{"id", "r", "d", "a", "fallback"}, []string{"INT4", "REGTYPE", "REGTYPE", "_REGTYPE", "REGTYPE"}, [][]any{
		{int64(1), "integer", "same", "{integer,regtype_b.same,NULL}", "same"},
		{int64(2), nil, nil, nil, "same"},
		{int64(3), "4294967295", "-", "{}", "same"},
	})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.RegtypeCursorMetadata(t, rows, postgres, s.Columns, s.Types, []bool{false, true, true, true, true})
	}
	return s
}

func TestReferenceRegtypeValues(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeValuesSetup(t, p, conn, recorder)
		fixture.RegtypeBoth(t, p, conn, recorder, regtypeValuesRows(backend.PG))
		for _, predicate := range []string{"id=99", "id=2"} {
			s := fixture.Select("SELECT r,d,a FROM regtype_a.values_subject WHERE "+predicate, []string{"r", "d", "a"}, []string{"REGTYPE", "REGTYPE", "_REGTYPE"}, [][]any{})
			if predicate == "id=2" {
				s.Rows = [][]any{{nil, nil, nil}}
			}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.RegtypeCursorMetadata(t, rows, backend.PG, s.Columns, s.Types, []bool{true, true, true})
			}
			fixture.RegtypeBoth(t, p, conn, recorder, s)
		}
		s := fixture.Select("SELECT r,r,r FROM regtype_a.values_subject ORDER BY id", []string{"r", "r", "r"}, []string{"REGTYPE", "REGTYPE", "REGTYPE"}, [][]any{{"integer", "integer", "integer"}, {nil, nil, nil}, {"4294967295", "4294967295", "4294967295"}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			fixture.RegtypeCursorMetadata(t, rows, backend.PG, s.Columns, s.Types, []bool{true, true, true})
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) { fixture.RegtypeScan(t, rows, index, backend.PG) }
		fixture.RegtypeBoth(t, p, conn, recorder, s)
		for i, value := range []any{"integer", []byte("regtype_b.same"), nil} {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(fixture.Exec("INSERT INTO regtype_a.values_subject(id,r) VALUES($1,$2)", 1, int64(i+4), value)))
		}
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,r FROM regtype_a.values_subject WHERE id>=4 ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(4), "integer"}, {int64(5), "regtype_b.same"}, {int64(6), nil}}))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT CASE WHEN id=1 THEN r ELSE fallback END AS r,COALESCE(d,r) AS d FROM regtype_a.values_subject WHERE id<=3 ORDER BY id", []string{"r", "d"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"integer", "same"}, {"same", nil}, {"same", "-"}}),
			fixture.Select("SELECT '0'::regtype<'23'::regtype AS a,'23'::regtype<'4294967295'::regtype AS b,'int4'::regtype='integer'::regtype AS c", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}),
			fixture.Select("SELECT r,count(*) AS n FROM regtype_a.values_subject WHERE id<=3 GROUP BY r ORDER BY r", []string{"r", "n"}, []string{"REGTYPE", "INT8"}, [][]any{{"integer", int64(1)}, {"4294967295", int64(1)}, {nil, int64(1)}}),
			fixture.Select("UPDATE regtype_a.values_subject SET r='name' WHERE id=4 RETURNING id,r", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(4), "name"}}),
			fixture.Select("DELETE FROM regtype_a.values_subject WHERE id=6 RETURNING id,r", []string{"id", "r"}, []string{"INT4", "REGTYPE"}, [][]any{{int64(6), nil}}),
		} {
			fixture.RegtypeRunStep(t, p.Ctx, conn, recorder, s)
		}
		// Late recursive conversion must not publish any of the first valid element.
		result, err := conn.ExecContext(p.Ctx, "INSERT INTO regtype_a.values_subject(id,a) VALUES(7,$1)", "{integer,missing_regtype_name}")
		if result != nil {
			t.Error("invalid recursive frame exposed result")
		}
		fixture.RegtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, "INSERT INTO regtype_a.values_subject(id,a) VALUES(7,$1)", "late-array-frame", "execute")
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT count(*) AS n FROM regtype_a.values_subject WHERE id=7", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceRegtypeHeldFrames(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		for _, query := range []string{"SELECT $1::regtype AS r", "SELECT $1::text::regtype AS r"} {
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
			for _, value := range []any{"same", "missing_regtype_name", nil, []byte("pg_catalog.int4"), "same"} {
				if value == "missing_regtype_name" {
					rows, err := held.QueryContext(p.Ctx, value)
					if rows != nil {
						rows.Close()
						t.Error("failed held query returned cursor")
					}
					fixture.RegtypeAssertError(t, err, "42704", `type "missing_regtype_name" does not exist`, query, "same-held-recovery", "execute")
					continue
				}
				expected := any("integer")
				if value == nil {
					expected = nil
				}
				if value == "same" {
					expected = "same"
				}
				fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{expected}}, value))
			}
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_b,regtype_a,public", 0))
			fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"r"}, []string{"REGTYPE"}, [][]any{{"same"}}, "same"))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_a,regtype_b,public", 0))
			held.Close()
		}
	})
}

func TestReferenceRegtypeHeldLiterals(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		var a, b int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.same'::regtype::bigint,'regtype_b.same'::regtype::bigint").Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		if a == 0 || b == 0 || a == b {
			t.Fatal("distinct user type identities", a, b)
		}
		const query = "SELECT 'same'::regtype::bigint AS oid,'same'::regtype AS r"
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
		observe := func(phase string, oid int64, output string) {
			t.Run(phase, func(t *testing.T) {
				fixture.RegtypeRunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"oid", "r"}, []string{"INT8", "REGTYPE"}, [][]any{{oid, output}}))
			})
		}
		observe("initial", a, "same")
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_b,regtype_a,public", 0))
		observe("reverse-path", b, "same")
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_a,regtype_b,public", 0))
		observe("restore-path", a, "same")
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP DOMAIN regtype_a.same", 0))
		observe("drop-A", a, fmt.Sprint(a))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a.same AS integer", 0))
		var recreated int64
		if err := conn.QueryRowContext(p.Ctx, "SELECT 'regtype_a.same'::regtype::bigint").Scan(&recreated); err != nil {
			t.Fatal(err)
		}
		if recreated == a {
			t.Fatal("recreated type reused dropped identity", a)
		}
		observe("recreate-A", a, fmt.Sprint(a))
	})
}

func TestReferenceRegtypeWarnings(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		const query = "SELECT 'timestamp(7)'::regtype AS a"
		*notices = nil
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6", Position: "8"}})
		*notices = nil
		for range 2 {
			fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, recorder, fixture.RegtypeSelect(query, "timestamp without time zone"))
			fixture.RegtypeAssertNotices(t, *notices, nil)
			*notices = nil
		}
		for _, q := range []string{"SELECT $1::regtype AS a", "SELECT $1::text::regtype AS a"} {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RegtypeAssertNotices(t, *notices, nil)
			*notices = nil
			for range 2 {
				s := fixture.RegtypeSelect(q, "timestamp without time zone")
				s.Args = []any{"timestamp(7)"}
				fixture.RegtypeRunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, recorder, s)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
				if q == "SELECT $1::regtype AS a" {
					want.Context = "unnamed portal parameter $1 = '...'"
				}
				fixture.RegtypeAssertNotices(t, *notices, []fixture.Diagnostic{want})
				*notices = nil
			}
			stmt.Close()
		}
	})
}
