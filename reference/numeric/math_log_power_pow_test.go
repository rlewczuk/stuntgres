package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func mathPowScenario(t *testing.T, body func(*mathPowCase)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(&mathPowCase{t: t, p: p, backend: backend, conn: conn})
		})
	}
}

type mathPowCase struct {
	t       *testing.T
	p       *fixture.Pair
	backend fixture.Backend
	conn    *sql.Conn
}

func (c *mathPowCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.backend.Recorder, s)
}

func (c *mathPowCase) both(s fixture.Step) {
	c.t.Helper()
	c.t.Run(s.Query, func(t *testing.T) {
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, c.conn, c.backend.Recorder, s) }
		mathLogPowerBoth(run, s)
	})
}

func (c *mathPowCase) diagnostic(query string, want fixture.Diagnostic, prepareFailure bool, args ...any) {
	c.t.Helper()
	c.t.Run(query, func(t *testing.T) {
		run := func(s fixture.Step) { fixture.RunStep(t, c.p.Ctx, c.conn, c.backend.Recorder, s) }
		mathLogPowerErrors(run, query, want, prepareFailure, args...)
		mathLogPowerBoth(run, fixture.Select("SELECT pow(2,3) AS recovered", []string{"recovered"}, []string{"FLOAT8"}, [][]any{{float64(8)}}))
	})
}

func (c *mathPowCase) catalog(s fixture.Step) {
	c.t.Helper()
	c.t.Run(s.Query, func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fixture.PgDescriptionExecutorRows(t, c.p, c.backend, c.conn, s)
		}
	})
}

const mathPowFider = "SELECT 1 / pow((EXTRACT(EPOCH FROM current_timestamp - (current_timestamp - interval '1 hour'))/3600) + 2, 1.4) > 0 AS f"
const mathPowOrder = "SELECT x FROM (VALUES (1),(2)) v(x) ORDER BY x / pow(x + 2, 1.4) DESC"

func TestReferenceMathLogPowerPowRepro(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		// Literal eight-query grind repro, including its aliases and catalog cast.
		decimal := fixture.Select("SELECT pow(2.0, 1.4) AS b", []string{"b"}, []string{"NUMERIC"}, [][]any{{"2.6390158215457885"}})
		decimal.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			fixture.AssertRawValue(t, c.backend.PG, "NUMERIC", raw)
			if got := fixture.Normalize(raw); got != "2.6390158215457885" {
				t.Fatalf("decimal raw result=%v", got)
			}
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT pow(2, 3) AS a", []string{"a"}, []string{"FLOAT8"}, [][]any{{float64(8)}}),
			decimal,
			fixture.Select("SELECT power(2, 3) AS c", []string{"c"}, []string{"FLOAT8"}, [][]any{{float64(8)}}),
			fixture.Select("SELECT 10 / pow(2, 3) AS d", []string{"d"}, []string{"FLOAT8"}, [][]any{{float64(1.25)}}),
			fixture.Select(mathPowOrder, []string{"x"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}),
			fixture.Select("SELECT pg_catalog.pow(2::float8, 3::float8) AS e", []string{"e"}, []string{"FLOAT8"}, [][]any{{float64(8)}}),
			fixture.Select(mathPowFider, []string{"f"}, []string{"BOOL"}, [][]any{{true}}),
		} {
			c.both(s)
		}
		c.catalog(fixture.Select("SELECT oid, proname, pg_get_function_identity_arguments(oid) AS args, prorettype::regtype FROM pg_proc WHERE proname IN ('pow','power') ORDER BY oid",
			[]string{"oid", "proname", "args", "prorettype"}, []string{"OID", "NAME", "TEXT", "REGTYPE"}, [][]any{
				{int64(1346), "pow", "double precision, double precision", "double precision"},
				{int64(1368), "power", "double precision, double precision", "double precision"},
				{int64(1738), "pow", "numeric, numeric", "numeric"},
				{int64(2169), "power", "numeric, numeric", "numeric"},
			}))
	})
}

func TestReferenceMathLogPowerPowBinding(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		c.run(fixture.Exec("CREATE DOMAIN pow_numeric AS numeric(20,4)", 0))
		c.run(fixture.Exec("CREATE DOMAIN pow_float AS float8", 0))
		for _, tc := range []struct {
			expression, typ string
			want            any
		}{
			{"pow(2,3)", "FLOAT8", float64(8)},
			{"pow(2::numeric,3::numeric)", "NUMERIC", "8.0000000000000000"},
			{"pow(9007199254740993::numeric,2::numeric)", "NUMERIC", "81129638414606699710187514626049"},
			{"pow(2::numeric,3::float8)", "FLOAT8", float64(8)},
			{"pow(2::float8,3::numeric)", "FLOAT8", float64(8)},
			{"pow(2::real,3::real)", "FLOAT8", float64(8)},
			{"pow(2::int2,3::int8)", "FLOAT8", float64(8)},
			{"pow('2','3')", "FLOAT8", float64(8)},
			{"pow('2',3::numeric)", "NUMERIC", "8.0000000000000000"},
			{"pow(2::pow_numeric,3::pow_numeric)", "NUMERIC", "8.0000000000000000"},
			{"pow(2::pow_float,3::pow_float)", "FLOAT8", float64(8)},
			{"pg_catalog.pow(2::float8,3::float8)", "FLOAT8", float64(8)},
			{"\"pow\"(2,3)", "FLOAT8", float64(8)},
			{"POW(2,3)", "FLOAT8", float64(8)},
			{"pow(NULL,NULL)", "FLOAT8", nil},
			{"pow(NULL::numeric,3::numeric)", "NUMERIC", nil},
			{"pow(2::float8,NULL::float8)", "FLOAT8", nil},
		} {
			s := fixture.Select("SELECT "+tc.expression, []string{"pow"}, []string{tc.typ}, [][]any{{tc.want}})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { mathLogPowerMetadata(t, rows, c.backend.PG) }
			c.both(s)
		}
		c.catalog(fixture.Select("SELECT pg_typeof(pow(2.0,1.4)) AS decimal_type,pg_typeof(pow(2,3)) AS integer_type", []string{"decimal_type", "integer_type"}, []string{"REGTYPE", "REGTYPE"}, [][]any{{"numeric", "double precision"}}))
		c.run(fixture.Exec("CREATE TABLE pow_metadata (n numeric,f float8)", 0))
		for _, rows := range [][][]any{nil, {{nil, nil}}} {
			s := fixture.Select("SELECT pow(n,3::numeric),pow(f,3::float8) AS floating FROM pow_metadata", []string{"pow", "floating"}, []string{"NUMERIC", "FLOAT8"}, rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { mathLogPowerMetadata(t, rows, c.backend.PG) }
			c.both(s)
			if rows == nil {
				c.run(fixture.Exec("INSERT INTO pow_metadata VALUES (NULL,NULL)", 1))
			}
		}
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ query, signature, position string }{
			{"SELECT pow(1)", "pow(integer)", "8"},
			{"SELECT pow(1,2,3)", "pow(integer, integer, integer)", "8"},
			{"SELECT pow('x'::text,2)", "pow(text, integer)", "8"},
			{"SELECT \"POW\"(2,3)", "POW(integer, integer)", "8"},
			{"SELECT public.pow(2,3)", "public.pow(integer, integer)", "8"},
			{"/*é*/ SELECT pow(1)", "pow(integer)", "14"},
		} {
			c.diagnostic(tc.query, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.position}, true)
		}
	})
}

func TestReferenceMathLogPowerPowCatalog(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		c.catalog(fixture.Select("SELECT p.oid,p.proname,p.pronamespace,n.nspname,p.pronargs,p.proargtypes,p.prorettype,p.prosrc,p.proisstrict,p.provolatile,p.proparallel,p.proretset,p.prokind,p.prolang,p.provariadic,p.pronargdefaults,p.proargdefaults,p.probin,p.proconfig,p.procost,p.prorows FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname IN ('pow','power') ORDER BY p.oid",
			[]string{"oid", "proname", "pronamespace", "nspname", "pronargs", "proargtypes", "prorettype", "prosrc", "proisstrict", "provolatile", "proparallel", "proretset", "prokind", "prolang", "provariadic", "pronargdefaults", "proargdefaults", "probin", "proconfig", "procost", "prorows"},
			[]string{"OID", "NAME", "OID", "NAME", "INT2", "OIDVECTOR", "OID", "TEXT", "BOOL", "CHAR", "CHAR", "BOOL", "CHAR", "OID", "OID", "INT2", "PG_NODE_TREE", "TEXT", "_TEXT", "FLOAT4", "FLOAT4"}, [][]any{
				{int64(1346), "pow", int64(11), "pg_catalog", int64(2), "701 701", int64(701), "dpow", true, "i", "s", false, "f", int64(12), int64(0), int64(0), nil, nil, nil, float64(1), float64(0)},
				{int64(1368), "power", int64(11), "pg_catalog", int64(2), "701 701", int64(701), "dpow", true, "i", "s", false, "f", int64(12), int64(0), int64(0), nil, nil, nil, float64(1), float64(0)},
				{int64(1738), "pow", int64(11), "pg_catalog", int64(2), "1700 1700", int64(1700), "numeric_power", true, "i", "s", false, "f", int64(12), int64(0), int64(0), nil, nil, nil, float64(1), float64(0)},
				{int64(2169), "power", int64(11), "pg_catalog", int64(2), "1700 1700", int64(1700), "numeric_power", true, "i", "s", false, "f", int64(12), int64(0), int64(0), nil, nil, nil, float64(1), float64(0)},
			}))
		c.catalog(fixture.Select("SELECT p.oid,d.classoid,d.objsubid,d.description,obj_description(p.oid,'pg_proc') AS inquiry FROM pg_proc p JOIN pg_description d ON d.objoid=p.oid AND d.classoid=1255 WHERE p.oid IN (1346,1368,1738,2169) ORDER BY p.oid,d.objsubid",
			[]string{"oid", "classoid", "objsubid", "description", "inquiry"}, []string{"OID", "OID", "INT4", "TEXT", "TEXT"}, [][]any{
				{int64(1346), int64(1255), int64(0), "exponentiation", "exponentiation"},
				{int64(1368), int64(1255), int64(0), "exponentiation", "exponentiation"},
				{int64(1738), int64(1255), int64(0), "exponentiation", "exponentiation"},
				{int64(2169), int64(1255), int64(0), "exponentiation", "exponentiation"},
			}))
		c.both(fixture.Select("SELECT objsubid,description FROM pg_description WHERE classoid=1255 AND objoid IN (1346,1368,1738,2169) AND objsubid<>0 ORDER BY objoid,objsubid", []string{"objsubid", "description"}, []string{"INT4", "TEXT"}, nil))
		c.both(fixture.Select("SELECT count(*) AS unrelated FROM pg_description WHERE classoid=1255 AND objoid=1344 AND description='exponentiation'", []string{"unrelated"}, []string{"INT8"}, [][]any{{int64(0)}}))
		c.both(fixture.Select("SELECT obj_description(1346,'pg_class') AS wrong_class", []string{"wrong_class"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}

// Keep custom floating checks on the same pinned connection and shared body.
func (c *mathPowCase) floating(query string, want float64, ulps uint64) {
	c.t.Helper()
	mathLogPowerFloat(c.t, c.p.Ctx, c.conn, query, want, ulps)
}
