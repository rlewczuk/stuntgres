package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"testing"
	"time"

	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// SFUNC adapters retain binary meaning, while strict/no-init executor startup
// independently rechecks current binary compatibility (including empty input).
func TestReferenceCastDefinitionAggregateBinary(t *testing.T) {
	for _, tc := range []struct {
		name, edge, method, context, code, message, input, explicit string
	}{
		{"explicit_binary", "WITHOUT FUNCTION", "b", "e", "42883", "function pg_catalog.byteacat(bytea, text) does not exist", `\x4142`, "5c7834313432"},
		{"assignment_binary", "WITHOUT FUNCTION AS ASSIGNMENT", "b", "a", "42883", "function pg_catalog.byteacat(bytea, text) does not exist", `\x4142`, "5c7834313432"},
		{"implicit_inout", "WITH INOUT AS IMPLICIT", "i", "i", "42804", "function pg_catalog.byteacat(bytea, bytea) requires run-time type coercion", `\x4142`, "4142"},
		{"implicit_function", "WITH FUNCTION cab_scope.decode_text(text) AS IMPLICIT", "f", "i", "42804", "function pg_catalog.byteacat(bytea, bytea) requires run-time type coercion", "4142", "4142"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					castDefinitionCleanup(t, p, backend, [2]string{"text", "bytea"})
					castAggregateBinaryCleanup(t, p, backend)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE SCHEMA cab_scope", 0))
					if tc.method == "f" {
						run(fixture.Exec("CREATE FUNCTION cab_scope.decode_text(text) RETURNS bytea LANGUAGE SQL AS $$ SELECT pg_catalog.decode($1,'hex') $$", 0))
					}
					run(fixture.Exec("CREATE CAST (text AS bytea) "+tc.edge, 0))
					for _, seed := range []string{",INITCOND=''", ""} {
						q := "CREATE AGGREGATE cab_scope.rejected(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea" + seed + ")"
						for _, prepared := range []bool{false, true} {
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
							s.Prepared, s.ErrorStage = prepared, "execute"
							run(s)
							// Failure cannot leave either a callable or aggregate row.
							run(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Select("SELECT count(*) AS n FROM pg_aggregate a JOIN pg_proc p ON a.aggfnoid=p.oid JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					}
					// The edge itself is valid and executable: only support-function
					// admission is binary-only, not ordinary call-argument coercion.
					run(fixture.Select("SELECT encode(($1::text)::bytea,'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.explicit}}, tc.input))
					run(fixture.Exec("CREATE AGGREGATE cab_scope.ordinary(bytea) (SFUNC=pg_catalog.byteacat,STYPE=bytea,INITCOND='')", 0))
					if tc.context == "i" {
						for _, prepared := range []bool{false, true} {
							s := fixture.Select("SELECT encode(cab_scope.ordinary($1::text),'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.explicit}}, tc.input)
							s.Prepared = prepared
							run(s)
						}
					}
					function := "castfunc=0::oid"
					if tc.method == "f" {
						function = "castfunc='cab_scope.decode_text(text)'::regprocedure::oid"
					}
					run(fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context,"+function+" AS routine FROM pg_cast WHERE castsource='text'::regtype AND casttarget='bytea'::regtype", []string{"method", "context", "routine"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{tc.method, tc.context, true}}))
				})
			}
		})
	}

	for _, final := range []string{"absent", "inout"} {
		t.Run("retained_"+final, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					castDefinitionCleanup(t, p, backend, [2]string{"text", "bytea"})
					castAggregateBinaryCleanup(t, p, backend)
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) {
						t.Helper()
						defer func() {
							if t.Failed() {
								t.Logf("SQL: %s", s.Query)
							}
						}()
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					}
					for _, q := range []string{
						"CREATE SCHEMA cab_scope",
						"CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT",
						"CREATE AGGREGATE cab_scope.bytes(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea,INITCOND='')",
						"CREATE AGGREGATE cab_scope.seed(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea)",
						"CREATE TABLE cab_scope.input(id integer,v text)",
					} {
						run(fixture.Exec(q, 0))
					}
					run(fixture.Exec(`INSERT INTO cab_scope.input VALUES(1,NULL),(2,'a'),(3,'b'),(4,'c'),(5,NULL),(6,'\x4142')`, 6))
					run(fixture.Exec("CREATE VIEW cab_scope.saved AS SELECT encode(cab_scope.bytes(v ORDER BY id),'hex') AS initialized FROM cab_scope.input", 0))
					run(fixture.Exec("CREATE VIEW cab_scope.seed_saved AS SELECT encode(cab_scope.seed(v ORDER BY id),'hex') AS seeded FROM cab_scope.input", 0))
					q := "SELECT encode(cab_scope.bytes(v ORDER BY id),'hex') AS initialized,encode(cab_scope.seed(v ORDER BY id),'hex') AS seeded FROM (VALUES(1,$1::text),(2,$2::text),(3,NULL::text)) s(id,v)"
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					values := func() {
						// No INITCOND: the first non-NULL TEXT datum must become an
						// actual BYTEA datum before byteacat sees the second input.
						for _, tc := range []struct{ first, second, initialized, seeded any }{
							{"a", "bc", "616263", "616263"},
							{`\x4142`, "", "5c7834313432", "5c7834313432"},
							{nil, "abc", "616263", "616263"},
							{"", "", "", ""},
							{nil, nil, "", nil},
						} {
							s := fixture.Select(q, []string{"initialized", "seeded"}, []string{"TEXT", "TEXT"}, [][]any{{tc.initialized, tc.seeded}}, tc.first, tc.second)
							run(s)
							run(fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
						}
					}
					values()
					for _, s := range castAggregateBinaryChecks("binary") {
						run(s)
					}
					// DROP preserves the aggregate declaration. Its initialized SFUNC
					// adapter remains meaningful; strict/no-init startup rechecks
					// current binary compatibility independently of that adapter.
					run(fixture.Exec("DROP CAST (text AS bytea) RESTRICT", 0))
					if final == "inout" {
						run(fixture.Exec("CREATE CAST (text AS bytea) WITH INOUT AS IMPLICIT", 0))
						// Current ordinary conversion decodes BYTEA input; the
						// already-declared initialized aggregate retains raw bytes.
						run(fixture.Select(`SELECT encode(('\x4142'::text)::bytea,'hex') AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"4142"}}))
					}
					heldFailure := fixture.ImplicitError(q, castAggregateSeedDiagnostic(t, p.Ctx, conn), "a", "bc")
					heldFailure.SelectRows, heldFailure.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, heldFailure)
					check := func(ex fixture.Executor, b fixture.Backend) {
						for _, s := range castAggregateBinaryChecks(final) {
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Prepared(s))
						}
						diagnostic := castAggregateSeedDiagnostic(t, p.Ctx, ex)
						for _, query := range []string{
							"SELECT encode(cab_scope.seed(v),'hex') FROM cab_scope.input",
							"SELECT encode(cab_scope.seed(v),'hex') FROM cab_scope.input WHERE FALSE",
							"SELECT seeded FROM cab_scope.seed_saved",
						} {
							s := fixture.ImplicitError(query, diagnostic)
							s.SelectRows, s.ErrorStage = true, "execute"
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Prepared(s))
						}
						code, message := "42883", "function pg_catalog.byteacat(bytea, text) does not exist"
						if final == "inout" {
							code, message = "42804", "function pg_catalog.byteacat(bytea, bytea) requires run-time type coercion"
						}
						for _, seed := range []string{",INITCOND=''", ""} {
							s := fixture.ImplicitError("CREATE AGGREGATE cab_scope.fresh(text) (SFUNC=pg_catalog.byteacat,STYPE=bytea"+seed+")", fixture.Diagnostic{Code: code, Message: message})
							s.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, s)
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Prepared(s))
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='fresh'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					}
					check(conn, backend)
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					if backend.PG {
						return
					}
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatalf("aggregate binary Restore: %v\n%s", err, dump.String())
					}
					check(backend.DB, backend)
					// A new Driver is a fresh native catalog/provider owner, not a
					// second connection borrowing the original descriptor graph.
					driver := stuntgres.NewDriver()
					connector, err := driver.OpenConnector("cast-aggregate-binary/" + t.Name())
					if err != nil {
						t.Fatal(err)
					}
					fresh := sql.OpenDB(connector)
					defer fresh.Close()
					if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
					configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("")
					if err != nil {
						t.Fatal(err)
					}
					observed := sql.OpenDB(configured)
					defer observed.Close()
					check(observed, fixture.Backend{Recorder: &fixture.Recorder{RawBackend: fixture.RawNative}})
					if p.Postgres != nil {
						pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
						castDefinitionCleanup(t, p, pg, [2]string{"text", "bytea"})
						castAggregateBinaryCleanup(t, p, pg)
						// Execute the actual unchanged emitted SQL. No independent
						// setup, cast substitution or dump-text assertion can prove it.
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatalf("actual aggregate binary SQL PostgreSQL replay: %v\n%s", err, dump.String())
						}
						check(p.Postgres, pg)
					}
				})
			}
		})
	}
}

func castAggregateBinaryCleanup(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if !backend.PG {
			if err := stuntgres.Reset(ctx, p.Native); err != nil {
				t.Errorf("aggregate binary native cleanup: %v", err)
			}
			return
		}
		if _, err := backend.DB.ExecContext(ctx, "DROP SCHEMA IF EXISTS cab_scope CASCADE"); err != nil {
			t.Errorf("aggregate binary schema cleanup: %v", err)
		}
	})
}

func castAggregateSeedDiagnostic(t *testing.T, ctx context.Context, ex fixture.Executor) fixture.Diagnostic {
	t.Helper()
	rows, err := ex.QueryContext(ctx, "SELECT 'cab_scope.seed(text)'::regprocedure::oid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var oid int64
	if !rows.Next() {
		t.Fatal("missing seed aggregate identity", rows.Err())
	}
	if err := rows.Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("invalid seed aggregate identity", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return fixture.Diagnostic{Code: "42P13", Message: fmt.Sprintf("aggregate %d needs to have compatible input type and transition type", oid)}
}

func castAggregateBinaryChecks(final string) []fixture.Step {
	result := func(input, from string, initialized, seeded any, args ...any) fixture.Step {
		query := "SELECT encode(cab_scope.bytes(" + input + "),'hex') AS initialized"
		columns, types, values := []string{"initialized"}, []string{"TEXT"}, []any{initialized}
		if final == "binary" {
			query += ",encode(cab_scope.seed(" + input + "),'hex') AS seeded"
			columns, types, values = append(columns, "seeded"), append(types, "TEXT"), append(values, seeded)
		}
		return fixture.Select(query+" FROM "+from, columns, types, [][]any{values}, args...)
	}
	steps := []fixture.Step{
		fixture.Select("SELECT initialized FROM cab_scope.saved", []string{"initialized"}, []string{"TEXT"}, [][]any{{"6162635c7834313432"}}),
		result("v ORDER BY id", "cab_scope.input WHERE id<6", "616263", "616263"),
		result("v", "cab_scope.input WHERE id=6", "5c7834313432", "5c7834313432"),
		result("v", "cab_scope.input WHERE v IS NULL", "", nil),
		result("v", "cab_scope.input WHERE FALSE", "", nil),
		fixture.Select("SELECT p.proname AS name,p.prokind::text AS kind,p.prorettype='bytea'::regtype AS result_type,a.aggtransfn='pg_catalog.byteacat(bytea,bytea)'::regprocedure::oid AS support,a.aggtranstype='bytea'::regtype AS state_type,f.proisstrict AS strict,a.agginitval FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE n.nspname='cab_scope' ORDER BY p.proname", []string{"name", "kind", "result_type", "support", "state_type", "strict", "agginitval"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{"bytes", "a", true, true, true, true, ""}, {"seed", "a", true, true, true, true, nil}}),
	}
	if final == "binary" {
		steps = append(steps, fixture.Select("SELECT seeded FROM cab_scope.seed_saved", []string{"seeded"}, []string{"TEXT"}, [][]any{{"6162635c7834313432"}}))
	}
	for _, tc := range []struct{ first, second, initialized, seeded any }{
		{"a", "bc", "616263", "616263"},
		{`\x4142`, "", "5c7834313432", "5c7834313432"},
		{nil, "abc", "616263", "616263"},
		{"", "", "", ""},
		{nil, nil, "", nil},
	} {
		steps = append(steps, result("v ORDER BY id", "(VALUES(1,$1::text),(2,$2::text),(3,NULL::text)) s(id,v)", tc.initialized, tc.seeded, tc.first, tc.second))
	}
	if final == "absent" {
		return append(steps, fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='text'::regtype AND casttarget='bytea'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	method := "i"
	if final == "binary" {
		method = "b"
	}
	return append(steps, fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context,castfunc=0::oid AS no_function FROM pg_cast WHERE castsource='text'::regtype AND casttarget='bytea'::regtype", []string{"method", "context", "no_function"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{method, "i", true}}))
}

func TestReferenceCastDefinitionAggregatePolymorphicBinary(t *testing.T) {
	for _, final := range []string{"absent", "assignment_io"} {
		t.Run(final, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					castDefinitionCleanup(t, p, backend, [2]string{"oid", "real"})
					castAggregateBinaryCleanup(t, p, backend)
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) {
						t.Helper()
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					}
					for _, query := range []string{
						"CREATE SCHEMA cab_scope",
						"CREATE CAST (oid AS real) WITHOUT FUNCTION AS IMPLICIT",
						"CREATE AGGREGATE cab_scope.collect_word(oid) (SFUNC=pg_catalog.array_append,STYPE=real[],INITCOND='{}')",
						"CREATE TABLE cab_scope.poly_input(id integer,v oid)",
					} {
						run(fixture.Exec(query, 0))
					}
					run(fixture.Exec("INSERT INTO cab_scope.poly_input VALUES(1,1065353216::oid),(2,1073741824::oid),(3,NULL)", 3))
					run(fixture.Exec("CREATE VIEW cab_scope.poly_saved AS SELECT cab_scope.collect_word(v ORDER BY id) AS a FROM cab_scope.poly_input", 0))
					projection := "array_lower(a,1) AS lo,array_upper(a,1) AS hi,a[1] AS first_value,a[2] AS second_value,a[3] AS third_value"
					columns := []string{"lo", "hi", "first_value", "second_value", "third_value"}
					types := []string{"INT4", "INT4", "FLOAT4", "FLOAT4", "FLOAT4"}
					want := [][]any{{int64(1), int64(3), float64(1), float64(2), nil}}
					query := "SELECT " + projection + " FROM(SELECT cab_scope.collect_word(v ORDER BY id) AS a FROM(VALUES(1,$1::oid),(2,$2::oid),(3,NULL::oid)) s(id,v)) q"
					value := fixture.Select(query, columns, types, want, int64(1065353216), int64(1073741824))
					held, err := conn.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					check := func(ex fixture.Executor, b fixture.Backend) {
						t.Helper()
						for _, step := range []fixture.Step{
							value,
							fixture.Select("SELECT "+projection+" FROM cab_scope.poly_saved", columns, types, want),
							fixture.Select("SELECT cardinality(cab_scope.collect_word(v)) AS n FROM cab_scope.poly_input WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}),
							fixture.Select("SELECT cardinality(a) AS n,a[1] IS NULL AS null_value FROM(SELECT cab_scope.collect_word(v) AS a FROM cab_scope.poly_input WHERE v IS NULL) q", []string{"n", "null_value"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}),
							fixture.Select("SELECT a.aggtransfn=378::oid AS support,a.aggtranstype='real[]'::regtype AS state_type,p.prorettype='real[]'::regtype AS result_type,f.proisstrict,a.agginitval FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE n.nspname='cab_scope' AND p.proname='collect_word'", []string{"support", "state_type", "result_type", "proisstrict", "agginitval"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, false, "{}"}}),
						} {
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, step)
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Prepared(step))
						}
					}
					check(conn, backend)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, value)
					run(fixture.Exec("DROP CAST (oid AS real) RESTRICT", 0))
					if final == "assignment_io" {
						run(fixture.Exec("CREATE CAST (oid AS real) WITH INOUT AS ASSIGNMENT", 0))
					}
					checkFinal := func(ex fixture.Executor, b fixture.Backend) {
						t.Helper()
						check(ex, b)
						var edge fixture.Step
						if final == "absent" {
							edge = fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='oid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
						} else {
							edge = fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context,castfunc=0::oid AS no_function FROM pg_cast WHERE castsource='oid'::regtype AND casttarget='real'::regtype", []string{"method", "context", "no_function"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"i", "a", true}})
							fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Select("SELECT ((1065353216::oid)::real)::double precision AS current_value", []string{"current_value"}, []string{"FLOAT8"}, [][]any{{float64(1065353216)}}))
						}
						fixture.RunStep(t, p.Ctx, ex, b.Recorder, edge)
						rejected := fixture.ImplicitError("CREATE AGGREGATE cab_scope.fresh_poly(oid) (SFUNC=pg_catalog.array_append,STYPE=real[],INITCOND='{}')", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.array_append(real[], oid) does not exist"})
						rejected.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, ex, b.Recorder, rejected)
						fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Prepared(rejected))
						fixture.RunStep(t, p.Ctx, ex, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='fresh_poly'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
					checkFinal(conn, backend)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, value)
					if err := held.Close(); err != nil {
						t.Fatal(err)
					}
					if err := conn.Close(); err != nil {
						t.Fatal(err)
					}
					if backend.PG {
						return
					}
					var snapshot bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
						t.Fatalf("polymorphic binary Restore: %v\n%s", err, snapshot.String())
					}
					checkFinal(backend.DB, backend)
					target := fixture.NewFixture(t, false, true)
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
						t.Fatal(err)
					}
					checkFinal(target.Memory, fixture.Backend{Recorder: target.MemoryRecord})
					if p.Postgres != nil {
						pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
						castDefinitionCleanup(t, p, pg, [2]string{"oid", "real"})
						castAggregateBinaryCleanup(t, p, pg)
						if _, err := p.Postgres.ExecContext(p.Ctx, snapshot.String()); err != nil {
							t.Fatalf("actual polymorphic binary SQL PostgreSQL replay: %v\n%s", err, snapshot.String())
						}
						checkFinal(p.Postgres, pg)
					}
				})
			}
		})
	}
}
