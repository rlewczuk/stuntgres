package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

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
			var snapshot []byte
			t.Run("memory", func(t *testing.T) {
				p := fixture.NewFixture(t, false, true)
				b := p.Backends()[0]
				castDefinitionCleanup(t, p, b, [2]string{"text", "bytea"})
				castAggregateBinaryCleanup(t, p, b)
				castBinaryLifecycle(t, p.Ctx, b, final)
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
				castBinaryFinal(t, p.Ctx, b.DB, b, final)
				snapshot = dump.Bytes()
			})
			if len(snapshot) == 0 {
				t.Fatal("native lifecycle did not produce a snapshot")
			}
			t.Run("fresh_native", func(t *testing.T) {
				p := fixture.NewFixture(t, false, true)
				b := p.Backends()[0]
				castDefinitionCleanup(t, p, b, [2]string{"text", "bytea"})
				castAggregateBinaryCleanup(t, p, b)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot)); err != nil {
					t.Fatal(err)
				}
				castBinaryFinal(t, p.Ctx, b.DB, b, final)
			})
			if *fixture.Mode {
				t.Run("postgres_replay", func(t *testing.T) {
					p := fixture.NewScenario(t)
					b := p.Backends()[1]
					castDefinitionCleanup(t, p, b, [2]string{"text", "bytea"})
					castAggregateBinaryCleanup(t, p, b)
					if _, err := b.DB.ExecContext(p.Ctx, string(snapshot)); err != nil {
						t.Fatalf("actual aggregate binary SQL PostgreSQL replay: %v\n%s", err, snapshot)
					}
					castBinaryFinal(t, p.Ctx, b.DB, b, final)
				})
				t.Run("postgres", func(t *testing.T) {
					p := fixture.NewScenario(t)
					b := p.Backends()[1]
					castDefinitionCleanup(t, p, b, [2]string{"text", "bytea"})
					castAggregateBinaryCleanup(t, p, b)
					castBinaryLifecycle(t, p.Ctx, b, final)
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
		// Filter all namespace-matching owners before the catalog products,
		// retaining both aggregates, multiplicity and actual support identities.
		fixture.Select("SELECT p.proname AS name,p.prokind::text AS kind,p.prorettype='bytea'::regtype AS result_type,a.aggtransfn='pg_catalog.byteacat(bytea,bytea)'::regprocedure::oid AS support,a.aggtranstype='bytea'::regtype AS state_type,f.proisstrict AS strict,a.agginitval FROM (SELECT p.oid,p.pronamespace,p.proname,p.prokind,p.prorettype FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope') p JOIN pg_namespace n ON p.pronamespace=n.oid JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE n.nspname='cab_scope' ORDER BY p.proname", []string{"name", "kind", "result_type", "support", "state_type", "strict", "agginitval"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{"bytes", "a", true, true, true, true, ""}, {"seed", "a", true, true, true, true, nil}}),
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
			var snapshot []byte
			// Each subtest completes its fixture cleanup before the next owner
			// starts. In particular, no PostgreSQL reset lifetime overlaps.
			t.Run("memory", func(t *testing.T) {
				p := fixture.NewFixture(t, false, true)
				b := p.Backends()[0]
				castDefinitionCleanup(t, p, b, [2]string{"oid", "real"})
				castAggregateBinaryCleanup(t, p, b)
				castPolymorphicLifecycle(t, p.Ctx, b, final)
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatalf("polymorphic binary Restore: %v\n%s", err, dump.String())
				}
				castPolymorphicFinal(t, p.Ctx, b.DB, b, final)
				snapshot = dump.Bytes()
			})
			if len(snapshot) == 0 {
				t.Fatal("native lifecycle did not produce a snapshot")
			}
			t.Run("fresh_native", func(t *testing.T) {
				p := fixture.NewFixture(t, false, true)
				b := p.Backends()[0]
				castDefinitionCleanup(t, p, b, [2]string{"oid", "real"})
				castAggregateBinaryCleanup(t, p, b)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot)); err != nil {
					t.Fatal(err)
				}
				castPolymorphicFinal(t, p.Ctx, b.DB, b, final)
			})
			if *fixture.Mode {
				t.Run("postgres_replay", func(t *testing.T) {
					p := fixture.NewScenario(t)
					b := p.Backends()[1]
					castDefinitionCleanup(t, p, b, [2]string{"oid", "real"})
					castAggregateBinaryCleanup(t, p, b)
					if _, err := b.DB.ExecContext(p.Ctx, string(snapshot)); err != nil {
						t.Fatalf("actual polymorphic binary SQL PostgreSQL replay: %v\n%s", err, snapshot)
					}
					castPolymorphicFinal(t, p.Ctx, b.DB, b, final)
				})
				t.Run("postgres", func(t *testing.T) {
					p := fixture.NewScenario(t)
					b := p.Backends()[1]
					castDefinitionCleanup(t, p, b, [2]string{"oid", "real"})
					castAggregateBinaryCleanup(t, p, b)
					castPolymorphicLifecycle(t, p.Ctx, b, final)
				})
			}
		})
	}
}

func castPolymorphicValue() fixture.Step {
	return fixture.Select(
		"SELECT array_lower(a,1) AS lo,array_upper(a,1) AS hi,a[1] AS first_value,a[2] AS second_value,a[3] AS third_value FROM(SELECT cab_scope.collect_word(v ORDER BY id) AS a FROM(VALUES(1,$1::oid),(2,$2::oid),(3,NULL::oid)) s(id,v)) q",
		[]string{"lo", "hi", "first_value", "second_value", "third_value"},
		[]string{"INT4", "INT4", "FLOAT4", "FLOAT4", "FLOAT4"},
		[][]any{{int64(1), int64(3), float64(1), float64(2), nil}},
		int64(1065353216), int64(1073741824))
}

func castPolymorphicLifecycle(t *testing.T, ctx context.Context, b fixture.Backend, final string) {
	t.Helper()
	conn, err := b.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE SCHEMA cab_scope", 0),
		fixture.Exec("CREATE CAST (oid AS real) WITHOUT FUNCTION AS IMPLICIT", 0),
		fixture.Exec("CREATE AGGREGATE cab_scope.collect_word(oid) (SFUNC=pg_catalog.array_append,STYPE=real[],INITCOND='{}')", 0),
		fixture.Exec("CREATE TABLE cab_scope.poly_input(id integer,v oid)", 0),
		fixture.Exec("INSERT INTO cab_scope.poly_input VALUES(1,1065353216::oid),(2,1073741824::oid),(3,NULL)", 3),
		fixture.Exec("CREATE VIEW cab_scope.poly_saved AS SELECT cab_scope.collect_word(v ORDER BY id) AS a FROM cab_scope.poly_input", 0),
	} {
		fixture.RunStep(t, ctx, conn, b.Recorder, step)
	}
	value := castPolymorphicValue()
	held, err := conn.PrepareContext(ctx, value.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	castPolymorphicChecks(t, ctx, conn, b)
	fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: value.Query}, b.Recorder, value)
	fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Exec("DROP CAST (oid AS real) RESTRICT", 0))
	if final == "assignment_io" {
		fixture.RunStep(t, ctx, conn, b.Recorder, fixture.Exec("CREATE CAST (oid AS real) WITH INOUT AS ASSIGNMENT", 0))
	}
	castPolymorphicFinal(t, ctx, conn, b, final)
	fixture.RunStep(t, ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: value.Query}, b.Recorder, value)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func castPolymorphicChecks(t *testing.T, ctx context.Context, ex fixture.Executor, b fixture.Backend) {
	t.Helper()
	value := castPolymorphicValue()
	// Filter owning procedures before the catalog joins, as in the aggregate
	// control fixture. Retain every candidate and all actual catalog edges.
	steps := []fixture.Step{
		value,
		fixture.Select("SELECT array_lower(a,1) AS lo,array_upper(a,1) AS hi,a[1] AS first_value,a[2] AS second_value,a[3] AS third_value FROM cab_scope.poly_saved", value.Columns, value.Types, value.Rows),
		fixture.Select("SELECT cardinality(cab_scope.collect_word(v)) AS n FROM cab_scope.poly_input WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT cardinality(a) AS n,a[1] IS NULL AS null_value FROM(SELECT cab_scope.collect_word(v) AS a FROM cab_scope.poly_input WHERE v IS NULL) q", []string{"n", "null_value"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}),
		fixture.Select("SELECT a.aggtransfn=378::oid AS support,a.aggtranstype='real[]'::regtype AS state_type,p.prorettype='real[]'::regtype AS result_type,f.proisstrict,a.agginitval FROM (SELECT oid,pronamespace,prorettype FROM pg_proc WHERE proname='collect_word') p JOIN pg_namespace n ON p.pronamespace=n.oid JOIN pg_aggregate a ON a.aggfnoid=p.oid JOIN pg_proc f ON f.oid=a.aggtransfn WHERE n.nspname='cab_scope'", []string{"support", "state_type", "result_type", "proisstrict", "agginitval"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, false, "{}"}}),
	}
	for _, step := range steps {
		fixture.RunStep(t, ctx, ex, b.Recorder, step)
		fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Prepared(step))
	}
}

func castPolymorphicFinal(t *testing.T, ctx context.Context, ex fixture.Executor, b fixture.Backend, final string) {
	t.Helper()
	castPolymorphicChecks(t, ctx, ex, b)
	var edge fixture.Step
	if final == "absent" {
		edge = fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='oid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
	} else {
		edge = fixture.Select("SELECT castmethod::text AS method,castcontext::text AS context,castfunc=0::oid AS no_function FROM pg_cast WHERE castsource='oid'::regtype AND casttarget='real'::regtype", []string{"method", "context", "no_function"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"i", "a", true}})
		fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Select("SELECT ((1065353216::oid)::real)::double precision AS current_value", []string{"current_value"}, []string{"FLOAT8"}, [][]any{{float64(1065353216)}}))
	}
	fixture.RunStep(t, ctx, ex, b.Recorder, edge)
	rejected := fixture.ImplicitError("CREATE AGGREGATE cab_scope.fresh_poly(oid) (SFUNC=pg_catalog.array_append,STYPE=real[],INITCOND='{}')", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.array_append(real[], oid) does not exist"})
	rejected.ErrorStage = "execute"
	fixture.RunStep(t, ctx, ex, b.Recorder, rejected)
	fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Prepared(rejected))
	fixture.RunStep(t, ctx, ex, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON p.pronamespace=n.oid WHERE n.nspname='cab_scope' AND p.proname='fresh_poly'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}
