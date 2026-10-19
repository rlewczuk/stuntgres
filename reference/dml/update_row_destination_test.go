package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateRowDestinationDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, set, code, message, token, hint string }{
		{"missing", "(missing,a)=ROW(1,2)", "42703", `column "missing" of relation "t" does not exist`, "missing", ""},
		{"system", "(ctid,a)=ROW(1,2)", "0A000", `cannot assign to system column "ctid"`, "ctid", ""},
		{"qualified", "(t.a,b)=ROW(1,2)", "42703", `column "t" of relation "t" does not exist`, "t.a", "SET target columns cannot be qualified with the relation name."},
		{"scalar_singleton", "(a)=(1)", "0A000", "source for a multiple-column UPDATE item must be a sub-SELECT or ROW() expression", "1)", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				updateRowSeed(t, p, b)
				q := "UPDATE t SET " + tc.set
				for _, route := range []string{"exec", "query", "prepare"} {
					t.Run(route, func(t *testing.T) {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: updateRowPosition(q, tc.token)})
						s.SelectRows = route == "query"
						if route == "prepare" {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					})
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			})
		})
	}
	for _, generated := range []bool{false, true} {
		t.Run(map[bool]string{false: "identity", true: "generated"}[generated], func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				definition := "CREATE TABLE t(a INT GENERATED ALWAYS AS IDENTITY,b INT)"
				seed := "INSERT INTO t(b) VALUES(10)"
				detail := `Column "a" is an identity column defined as GENERATED ALWAYS.`
				if generated {
					definition = "CREATE TABLE t(a INT GENERATED ALWAYS AS (b+1) STORED,b INT)"
					detail = `Column "a" is a generated column.`
				}
				run(fixture.Exec(definition, 0))
				run(fixture.Exec(seed, 1))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("UPDATE t SET (a,b)=ROW(7,8)", fixture.Diagnostic{Code: "428C9", Message: `column "a" can only be updated to DEFAULT`, Detail: detail})
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				a := int64(1)
				if generated {
					a = 11
				}
				run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{a, int64(10)}}))
			})
		})
	}
}

func TestReferenceUpdateRowCoercionAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, typ, source string
		d                 fixture.Diagnostic
	}{
		{"smallint", "SMALLINT", "40000", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"}},
		{"varchar", "VARCHAR(3)", "'long'::text", fixture.Diagnostic{Code: "22001", Message: "value too long for type character varying(3)"}},
		{"domain", "positive", "-1", fixture.Diagnostic{Code: "23514", Message: `value for domain positive violates check constraint "positive_check"`, Schema: "public", DataType: "positive", Constraint: "positive_check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE DOMAIN positive AS INT CHECK(VALUE>0)", 0))
				run(fixture.Exec("CREATE TABLE t(id INT,a "+tc.typ+",b INT)", 0))
				seed := "INSERT INTO t VALUES(1,1,10),(2,2,20)"
				if tc.name == "varchar" {
					seed = "INSERT INTO t VALUES(1,'1',10),(2,'2',20)"
				}
				run(fixture.Exec(seed, 2))
				run(fixture.Exec("CREATE TABLE s(id INT,x "+map[bool]string{false: "INT", true: "TEXT"}[tc.name == "varchar"]+")", 0))
				run(fixture.Exec("INSERT INTO s VALUES(1,'3'),(2,"+tc.source+")", 2))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("UPDATE t SET (a,b)=(SELECT x,99 FROM s WHERE s.id=t.id) RETURNING *", tc.d)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				// Read actual stored destinations. SMALLINT-to-TEXT explicit casts
				// are an adjacent native boundary, not part of assignment coercion.
				typ := "INT4"
				var a1, a2 any = int64(1), int64(2)
				if tc.name == "smallint" {
					typ = "INT2"
				}
				if tc.name == "varchar" {
					typ, a1, a2 = "VARCHAR", "1", "2"
				}
				run(fixture.Select("SELECT id,a,b FROM t ORDER BY id", []string{"id", "a", "b"}, []string{"INT4", typ, "INT4"}, [][]any{{int64(1), a1, int64(10)}, {int64(2), a2, int64(20)}}))
			})
		})
	}
}

func TestReferenceUpdateRowDomainDefaults(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			for _, q := range []string{
				"CREATE DOMAIN row_default AS INT DEFAULT 22",
				"CREATE DOMAIN row_nullable AS INT CHECK(VALUE IS NULL OR VALUE>0)",
				"CREATE DOMAIN row_required AS INT NOT NULL",
				"CREATE DOMAIN row_positive AS INT CHECK(VALUE>0)",
				"CREATE TABLE row_defaults(id INT,a row_default DEFAULT 44,b row_default,c row_default DEFAULT NULL,d row_nullable,r row_required,v row_positive)",
			} {
				run(fixture.Exec(q, 0))
			}
			seed := "INSERT INTO row_defaults VALUES(1,1,2,3,4,5,6),(2,7,8,9,10,11,12)"
			run(fixture.Exec(seed, 2))
			columns := []string{"id", "a", "b", "c", "d", "r", "v"}
			types := []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}
			original := [][]any{
				{int64(1), int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)},
				{int64(2), int64(7), int64(8), int64(9), int64(10), int64(11), int64(12)},
			}
			// The leading scalar puts DEFAULT destinations at flat indices 1..4.
			// Distinct seed/default values expose an origin/index mixup.
			q := "UPDATE row_defaults SET id=id+$1,(a,b)=ROW(DEFAULT,DEFAULT),(c,d)=ROW(DEFAULT,DEFAULT) WHERE id=$2"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			for _, route := range []string{"direct", "held"} {
				t.Run(route, func(t *testing.T) {
					run := func(s fixture.Step) {
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					}
					snapshot := func(rows [][]any) {
						run(fixture.Select("SELECT id,a,b,c,d,r,v FROM row_defaults ORDER BY id", columns, types, rows))
					}
					reset := func() {
						run(fixture.Exec("DELETE FROM row_defaults", 2))
						run(fixture.Exec(seed, 2))
					}
					reset()
					exec := func(s fixture.Step) {
						if route == "held" {
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
						} else {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}
					}
					exec(fixture.Exec(q, 1, int64(10), int64(1)))
					snapshot([][]any{
						{int64(2), int64(7), int64(8), int64(9), int64(10), int64(11), int64(12)},
						{int64(11), int64(44), int64(22), nil, nil, int64(5), int64(6)},
					})
					// Reuse the exact same prepared object with new arguments/row.
					exec(fixture.Exec(q, 1, int64(20), int64(2)))
					snapshot([][]any{
						{int64(11), int64(44), int64(22), nil, nil, int64(5), int64(6)},
						{int64(22), int64(44), int64(22), nil, nil, int64(11), int64(12)},
					})
					// Supplied NULL bypasses both column and domain default recipes.
					nullQ := "UPDATE row_defaults SET id=id+1,(a,b)=ROW(NULL,NULL),(c,d)=ROW(NULL,NULL)"
					nullStmt, err := conn.PrepareContext(p.Ctx, nullQ)
					if err != nil {
						t.Fatal(err)
					}
					defer nullStmt.Close()
					var nullExecutor fixture.Executor = conn
					if route == "held" {
						nullExecutor = fixture.HeldStatement{Executor: conn, Statement: nullStmt, SQL: nullQ}
					}
					fixture.RunStep(t, p.Ctx, nullExecutor, backend.Recorder, fixture.Exec(nullQ, 2))
					snapshot([][]any{
						{int64(12), nil, nil, nil, nil, int64(5), int64(6)},
						{int64(23), nil, nil, nil, nil, int64(11), int64(12)},
					})
					reset()
					// Runtime failures occur after earlier SET items. The check case
					// distinguishes the two rows without promising scan order.
					for _, failure := range []struct {
						query string
						want  fixture.Diagnostic
					}{
						{
							"UPDATE row_defaults SET id=id+10,(a,b)=ROW(DEFAULT,DEFAULT),(d,r)=ROW(DEFAULT,DEFAULT) RETURNING *",
							fixture.Diagnostic{Code: "23502", Message: "domain row_required does not allow null values", Schema: "public", DataType: "row_required"},
						},
						{
							"UPDATE row_defaults SET id=id+10,(a,b)=ROW(DEFAULT,DEFAULT),(c,d)=ROW(DEFAULT,DEFAULT),v=CASE WHEN id=2 THEN -1 ELSE 9 END RETURNING *",
							fixture.Diagnostic{Code: "23514", Message: `value for domain row_positive violates check constraint "row_positive_check"`, Schema: "public", DataType: "row_positive", Constraint: "row_positive_check"},
						},
					} {
						failedStmt, err := conn.PrepareContext(p.Ctx, failure.query)
						if err != nil {
							t.Fatal(err)
						}
						defer failedStmt.Close()
						s := fixture.ImplicitError(failure.query, failure.want)
						s.SelectRows, s.ErrorStage = true, "execute"
						if route == "held" {
							failedHeld := fixture.HeldStatement{Executor: conn, Statement: failedStmt, SQL: failure.query}
							fixture.RunStep(t, p.Ctx, failedHeld, backend.Recorder, s)
						} else {
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						}
						failedStmt.Close()
						snapshot(original)
					}
				})
			}
		})
	}
}
