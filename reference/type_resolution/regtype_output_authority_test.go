package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// These expectations are independent of allocator values and native formatting.
func regtypeOutputAuthoritySetup(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RegtypeSetup(t, p, conn, recorder)
	for _, query := range []string{
		"CREATE DOMAIN regtype_a.identifier AS regtype",
		"CREATE DOMAIN regtype_a.int4 AS text",
		"CREATE TABLE regtype_a.output_source(id integer,r regtype,d regtype_a.identifier)",
		"INSERT INTO regtype_a.output_source VALUES(1,'integer','integer'),(2,'regtype_a.same','regtype_a.same'),(3,NULL,NULL)",
		"CREATE TABLE regtype_a.output_assignment(id integer,t text,v varchar,dt text,dv varchar)",
		"CREATE TABLE regtype_a.output_defaults(id integer,t text DEFAULT 'regtype_a.same'::regtype,v varchar DEFAULT ('regtype_a.same'::regtype)::varchar,dt text DEFAULT 'integer'::regtype_a.identifier,dv varchar DEFAULT ('integer'::regtype_a.identifier)::varchar)",
		"CREATE TABLE regtype_a.output_assignment_defaults(id integer,t text DEFAULT 'regtype_a.same'::regtype,v varchar DEFAULT 'regtype_a.same'::regtype,dt text DEFAULT 'integer'::regtype_a.identifier,dv varchar DEFAULT 'integer'::regtype_a.identifier)",
		"CREATE TABLE regtype_a.output_builtin_default(v varchar DEFAULT ('integer'::regtype)::varchar)",
	} {
		count := int64(0)
		if query == "INSERT INTO regtype_a.output_source VALUES(1,'integer','integer'),(2,'regtype_a.same','regtype_a.same'),(3,NULL,NULL)" {
			count = 3
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(query, count))
	}
}

func regtypeOutputAuthorityRows(table string) fixture.Step {
	return fixture.Select("SELECT id,t,v,dt,dv FROM regtype_a."+table+" ORDER BY id", []string{"id", "t", "v", "dt", "dv"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{
		{int64(1), "integer", "integer", "integer", "integer"},
		{int64(2), "same", "same", "same", "same"},
		{int64(3), nil, nil, nil, nil},
	})
}

// This cursor is a native computed VARCHAR result, not a REGTYPE result.
func regtypeOutputVarcharMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"v", "dv"}
	if len(columns) != len(names) {
		t.Fatalf("VARCHAR column count %d want %d", len(columns), len(names))
	}
	for i, typ := range columns {
		length, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		nullable, nullableOK := typ.Nullable()
		if typ.Name() != names[i] || typ.DatabaseTypeName() != "VARCHAR" || typ.ScanType() != reflect.TypeFor[string]() ||
			length != math.MaxInt64 || !lengthOK || precision != 0 || scale != 0 || decimalOK || nullable || nullableOK {
			t.Fatalf("VARCHAR metadata column %d name=%s type=%s scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t",
				i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func TestReferenceRegtypeOutputAuthority(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeOutputAuthoritySetup(t, p, conn, recorder)
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT 'integer'::regtype::varchar AS v,'integer'::regtype_a.identifier::varchar AS d,NULL::regtype::varchar AS n", []string{"v", "d", "n"}, []string{"VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"integer", "integer", nil}}))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,r::varchar AS v,d::varchar AS dv FROM regtype_a.output_source ORDER BY id", []string{"id", "v", "dv"}, []string{"INT4", "VARCHAR", "VARCHAR"}, [][]any{{int64(1), "integer", "integer"}, {int64(2), "same", "same"}, {int64(3), nil, nil}}))
		for _, predicate := range []string{"id=99", "id=3"} {
			var values [][]any
			if predicate == "id=3" {
				values = [][]any{{nil, nil}}
			}
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT r::varchar AS v,d::varchar AS dv FROM regtype_a.output_source WHERE "+predicate, []string{"v", "dv"}, []string{"VARCHAR", "VARCHAR"}, values))
		}
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("INSERT INTO regtype_a.output_assignment SELECT id,r,r,d,d FROM regtype_a.output_source", 3)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, recorder, step)
			fixture.RegtypeBoth(t, p, conn, recorder, regtypeOutputAuthorityRows("output_assignment"))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM regtype_a.output_assignment", 3))
		}
		const query = "INSERT INTO regtype_a.output_assignment VALUES($1,$2::regtype,$2::regtype,$2::regtype_a.identifier,$2::regtype_a.identifier)"
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, executor := range []fixture.Executor{conn, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}} {
			for i, value := range []any{"integer", "regtype_a.same", nil} {
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec(query, 1, i+1, value))
			}
			fixture.RegtypeBoth(t, p, conn, recorder, regtypeOutputAuthorityRows("output_assignment"))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM regtype_a.output_assignment", 3))
		}
		for _, query := range []string{
			"INSERT INTO regtype_a.output_defaults(id) VALUES(1)",
			"INSERT INTO regtype_a.output_defaults(id) VALUES(2)",
		} {
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Exec(query, 1))
		}
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,t,v,dt,dv FROM regtype_a.output_defaults ORDER BY id", []string{"id", "t", "v", "dt", "dv"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{
			{int64(1), "same", "same", "integer", "integer"}, {int64(1), "same", "same", "integer", "integer"},
			{int64(2), "same", "same", "integer", "integer"}, {int64(2), "same", "same", "integer", "integer"},
		}))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_assignment_defaults(id) VALUES(1)", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT t,v,dt,dv FROM regtype_a.output_assignment_defaults ORDER BY id", []string{"t", "v", "dt", "dv"}, []string{"TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{"same", "same", "integer", "integer"}, {"same", "same", "integer", "integer"}}))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_builtin_default DEFAULT VALUES", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT v FROM regtype_a.output_builtin_default", []string{"v"}, []string{"VARCHAR"}, [][]any{{"integer"}, {"integer"}}))
	})
}

func TestReferenceRegtypeOutputAuthorityAtomicity(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeOutputAuthoritySetup(t, p, conn, recorder)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_assignment VALUES(9,'sentinel','sentinel','sentinel','sentinel')", 1))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE regtype_a.output_late(id integer,s text)", 0))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_late VALUES(1,'integer'),(2,'missing_regtype_name')", 2))
		for _, query := range []string{
			"INSERT INTO regtype_a.output_assignment SELECT id,s::regtype,s::regtype,s::regtype_a.identifier,s::regtype_a.identifier FROM regtype_a.output_late ORDER BY id",
			"UPDATE regtype_a.output_assignment SET t=$1::text::regtype,v=$1::text::regtype,dt=$1::text::regtype_a.identifier,dv=$1::text::regtype_a.identifier",
		} {
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal("runtime conversion must Prepare successfully", err)
			}
			for _, prepared := range []bool{false, true} {
				var args []any
				if query[0] == 'U' {
					args = []any{"missing_regtype_name"}
				}
				var result sql.Result
				if prepared {
					result, err = held.ExecContext(p.Ctx, args...)
				} else {
					result, err = conn.ExecContext(p.Ctx, query, args...)
				}
				if err == nil || result != nil {
					t.Fatalf("failed conversion returned result=%v error=%v", result, err)
				}
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `type "missing_regtype_name" does not exist`})
				fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,t,v,dt,dv FROM regtype_a.output_assignment", []string{"id", "t", "v", "dt", "dv"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{int64(9), "sentinel", "sentinel", "sentinel", "sentinel"}}))
			}
			if query[0] == 'U' {
				executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec(query, 1, nil))
				fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT t,v,dt,dv FROM regtype_a.output_assignment", []string{"t", "v", "dt", "dv"}, []string{"TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{nil, nil, nil, nil}}))
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec(query, 1, "integer"))
				fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT t,v,dt,dv FROM regtype_a.output_assignment", []string{"t", "v", "dt", "dv"}, []string{"TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{"integer", "integer", "integer", "integer"}}))
			}
			held.Close()
		}
	})
}

func TestReferenceRegtypeOutputAuthorityNativeControls(t *testing.T) {
	var emitted []byte
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeOutputAuthoritySetup(t, p, conn, recorder)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_assignment SELECT id,r,r,d,d FROM regtype_a.output_source", 3))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_defaults(id) VALUES(4)", 1))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_assignment_defaults(id) VALUES(4)", 1))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			fixture.RegtypeBoth(t, p, db, record, regtypeOutputAuthorityRows("output_assignment"))
			fixture.RegtypeBoth(t, p, db, record, fixture.Select("SELECT id,t,v,dt,dv FROM regtype_a.output_defaults ORDER BY id", []string{"id", "t", "v", "dt", "dv"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{int64(4), "same", "same", "integer", "integer"}}))
			fixture.RegtypeBoth(t, p, db, record, fixture.Select("SELECT id,t,v,dt,dv FROM regtype_a.output_assignment_defaults ORDER BY id", []string{"id", "t", "v", "dt", "dv"}, []string{"INT4", "TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{int64(4), "same", "same", "integer", "integer"}}))
		}
		check(conn, recorder)
		if backend.PG {
			if emitted == nil {
				t.Fatal("actual native Dump stream required")
			}
			for _, schema := range []string{"regtype_a", "regtype_b", "Odd.Regtype"} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP SCHEMA "+fixture.RegtypeIdentifier(schema)+" CASCADE", 0))
			}
			if _, err := conn.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native Dump PostgreSQL replay", err)
			}
		} else {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("SET search_path TO regtype_a,regtype_b,public", 0))
		check(conn, recorder)
		// Re-exercise stored coercions after replay, not merely dumped row strings.
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_defaults(id) VALUES(5)", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT t,v,dt,dv FROM regtype_a.output_defaults WHERE id=5", []string{"t", "v", "dt", "dv"}, []string{"TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{"same", "same", "integer", "integer"}}))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_assignment_defaults(id) VALUES(5)", 1))
		fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT t,v,dt,dv FROM regtype_a.output_assignment_defaults WHERE id=5", []string{"t", "v", "dt", "dv"}, []string{"TEXT", "VARCHAR", "TEXT", "VARCHAR"}, [][]any{{"same", "same", "integer", "integer"}}))
		if !backend.PG {
			retained, err := conn.QueryContext(p.Ctx, "SELECT 'regtype_a.same'::regtype::varchar AS v,'regtype_a.same'::regtype_a.identifier::varchar AS dv")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("ALTER SCHEMA regtype_a RENAME TO regtype_renamed", 0))
			fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT 'regtype_renamed.same'::regtype::varchar AS v,'regtype_renamed.same'::regtype_renamed.identifier::varchar AS dv", []string{"v", "dv"}, []string{"VARCHAR", "VARCHAR"}, [][]any{{"regtype_renamed.same", "regtype_renamed.same"}}))
			fixture.RegtypeAssertRowsWithMetadata(t, retained, [][]any{{"same", "same"}}, false, regtypeOutputVarcharMetadata)
		}
	})
}

func TestReferenceRegtypeOutputAuthorityHeld(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeOutputAuthoritySetup(t, p, conn, recorder)
		for _, query := range []string{
			"SELECT $1::text::regtype::varchar AS v",
			"SELECT $1::text::regtype_a.identifier::varchar AS v",
		} {
			held, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			executor := fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}
			for _, value := range []any{"integer", "missing_regtype_name", nil, "regtype_a.same", "integer"} {
				if value == "missing_regtype_name" {
					rows, err := held.QueryContext(p.Ctx, value)
					if rows != nil {
						rows.Close()
						t.Fatal("failed VARCHAR cast exposed rows")
					}
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `type "missing_regtype_name" does not exist`})
					continue
				}
				var expected any
				if value != nil {
					expected = "integer"
					if value == "regtype_a.same" {
						expected = "same"
					}
				}
				step := fixture.Select(query, []string{"v"}, []string{"VARCHAR"}, [][]any{{expected}}, value)
				fixture.RunStep(t, p.Ctx, conn, recorder, step)
				fixture.RunStep(t, p.Ctx, executor, recorder, step)
			}
			held.Close()
		}
	})
}

func TestReferenceRegtypeOutputAuthorityAlter(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		regtypeOutputAuthoritySetup(t, p, conn, recorder)
		for _, prepared := range []bool{false, true} {
			for _, families := range [][]string{{"TEXT", "VARCHAR"}, {"VARCHAR", "TEXT"}} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE regtype_a.output_alter(id integer,r regtype,d regtype_a.identifier)", 0))
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a.output_alter VALUES(1,'integer','integer'),(2,'regtype_a.same','regtype_a.same'),(3,NULL,NULL)", 3))
				alter := fixture.Exec("ALTER TABLE regtype_a.output_alter ALTER COLUMN r TYPE "+families[0]+",ALTER COLUMN d TYPE "+families[1], 0)
				alter.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, recorder, alter)
				step := fixture.Select("SELECT id,r,d FROM regtype_a.output_alter ORDER BY id", []string{"id", "r", "d"}, []string{"INT4", families[0], families[1]}, [][]any{
					{int64(1), "integer", "integer"}, {int64(2), "same", "same"}, {int64(3), nil, nil},
				})
				if !backend.PG {
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						t.Helper()
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						for i, typ := range columns {
							wantLength, wantKnown := int64(0), false
							if i > 0 {
								wantLength, wantKnown = math.MaxInt64, true
							}
							length, lengthOK := typ.Length()
							precision, scale, decimalOK := typ.DecimalSize()
							nullable, nullableOK := typ.Nullable()
							if length != wantLength || lengthOK != wantKnown || precision != 0 || scale != 0 || decimalOK || !nullable || !nullableOK {
								t.Fatalf("ALTER metadata %s length=%d/%t decimal=%d,%d/%t nullable=%t/%t",
									typ.Name(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
							}
						}
					}
				}
				fixture.RegtypeBoth(t, p, conn, recorder, step)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP TABLE regtype_a.output_alter", 0))
			}
		}
	})
}
