package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegProcValuesMetadata(t *testing.T) {
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, fact := range []struct{ inputR, inputP, outputR, outputP string }{
			{"boolin", "boolin(cstring)", "boolin", "boolin(cstring)"},
			{"1242", "1242", "boolin", "boolin(cstring)"},
			{"now", "now()", "now", "now()"},
			{"-", "0", "-", "-"},
			{"0", "-", "-", "-"},
			{"4294967295", "4294967295", "4294967295", "4294967295"},
			{`"RI_FKey_check_ins"`, `"RI_FKey_check_ins"()`, `"RI_FKey_check_ins"`, `"RI_FKey_check_ins"()`},
		} {
			q := "SELECT " + fixture.RegtypeLiteral(fact.inputR) + "::regproc AS r," + fixture.RegtypeLiteral(fact.inputP) + "::regprocedure AS p"
			regprocBoth(t, p, c, r, fixture.Select(q, []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{fact.outputR, fact.outputP}}))
		}
		const q = "SELECT $1::regproc AS r,$2::regprocedure AS p"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, args := range [][]any{{"boolin", "abs(integer)"}, {[]byte("boolin"), []byte("abs(integer)")}, {nil, nil}, {[]byte(nil), []byte(nil)}} {
			want := [][]any{{"boolin", "abs(integer)"}}
			if args[0] == nil {
				want = [][]any{{nil, nil}}
			} else if bytes, ok := args[0].([]byte); ok && bytes == nil {
				want = [][]any{{nil, nil}}
			}
			s := fixture.Select(q, []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, want, args...)
			regprocBoth(t, p, c, r, s)
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, s)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.regproc_values(id integer,r regproc,p regprocedure)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.regproc_values VALUES(1,'boolin','abs(integer)'),(2,NULL,NULL)", 2))
		regprocBoth(t, p, c, r, fixture.Select("SELECT r,p FROM public.regproc_values WHERE id=99", []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, nil))
		regprocBoth(t, p, c, r, fixture.Select("SELECT r,p FROM public.regproc_values ORDER BY id", []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"boolin", "abs(integer)"}, {nil, nil}}))
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'regproc'::regtype::oid::bigint AS r,'regprocedure'::regtype::oid::bigint AS p,'regproc[]'::regtype::oid::bigint AS ra,'regprocedure[]'::regtype::oid::bigint AS pa", []string{"r", "p", "ra", "pa"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(24), int64(2202), int64(1008), int64(2207)}}))
	})
}

// IDs/signatures selected from the complete 654436-byte REL_18_0 pg_proc.dat.
// No execution provider or locally allocated identity is implied by lookup.
func TestReferenceRegProcSignatures(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "regproc_types")
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, fact := range []struct {
			input, output string
			oid           int64
		}{
			{"abs(int)", "abs(integer)", 1397}, {" abs ( pg_catalog.int4 ) ", "abs(integer)", 1397},
			{"PG_CATALOG.ABS(bigint)", "abs(bigint)", 1396}, {"abs(numeric(8,2))", "abs(numeric)", 1705},
			{"abs(double precision)", "abs(double precision)", 1395}, {"now( )", "now()", 1299},
			{"sum(pg_catalog.int4)", "sum(integer)", 2108}, {`"RI_FKey_check_ins"()`, `"RI_FKey_check_ins"()`, 1644},
			{"array_fill(anyelement,pg_catalog.int4[])", "array_fill(anyelement,integer[])", 1193},
			{"json_object(text[2][3])", `"json_object"(text[])`, 3202},
		} {
			t.Run(fact.input, func(t *testing.T) {
				q := "SELECT " + fixture.RegtypeLiteral(fact.input) + "::regprocedure AS p," + fixture.RegtypeLiteral(fact.input) + "::regprocedure::oid::bigint AS id"
				regprocBoth(t, p, c, r, fixture.Select(q, []string{"p", "id"}, []string{"REGPROCEDURE", "INT8"}, [][]any{{fact.output, fact.oid}}))
			})
		}
		for _, q := range []string{"CREATE SCHEMA regproc_types", `CREATE DOMAIN regproc_types."a,b" AS integer`} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		input := `abs(regproc_types."a,b")`
		_, err := c.ExecContext(p.Ctx, "SELECT $1::regprocedure", input)
		regprocError(t, err, "42883", `function "`+input+`" does not exist`, "SELECT $1::regprocedure", "direct-frame", input)
	})
}

func TestReferenceRegProcCasts(t *testing.T) {
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, q := range []string{
			"SELECT 1242::integer::regproc AS r,1397::bigint::regprocedure AS p",
			"SELECT 1242::oid::regproc AS r,1397::oid::regprocedure AS p",
			"SELECT 'boolin(cstring)'::regprocedure::regproc AS r,'1397'::regproc::regprocedure AS p",
			"SELECT 'boolin'::text::regproc AS r,'abs(integer)'::varchar::regprocedure AS p",
		} {
			regprocBoth(t, p, c, r, fixture.Select(q, []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"boolin", "abs(integer)"}}))
		}
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'boolin'::regproc::integer AS r,'abs(integer)'::regprocedure::oid::bigint AS p,'4294967295'::regproc::integer AS n", []string{"r", "p", "n"}, []string{"INT4", "INT8", "INT4"}, [][]any{{int64(1242), int64(1397), int64(-1)}}))
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'boolin'::regproc::text AS r,'abs(integer)'::regprocedure::varchar AS p", []string{"r", "p"}, []string{"TEXT", "VARCHAR"}, [][]any{{"boolin", "abs(integer)"}}))
		regprocBoth(t, p, c, r, fixture.Select("SELECT ('4294967295'::regproc>'1242'::regproc) AS r,('1397'::regprocedure='abs(integer)'::regprocedure) AS p", []string{"r", "p"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		regprocBoth(t, p, c, r, fixture.Select("SELECT NULL::regproc AS r,NULL::regprocedure AS p", []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{nil, nil}}))
		for _, fact := range []struct{ query, code, message string }{
			{"SELECT TRUE::regproc", "42846", "cannot cast type boolean to regproc"},
			{"SELECT TRUE::regprocedure", "42846", "cannot cast type boolean to regprocedure"},
			{"SELECT '-1'::bigint::regproc", "22003", "OID out of range"},
		} {
			t.Run(fact.query, func(t *testing.T) {
				_, err := c.ExecContext(p.Ctx, fact.query)
				regprocError(t, err, fact.code, fact.message, fact.query, "cast", " ")
			})
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.regproc_casts(r regproc,p regprocedure)", 0))
		_, err := c.ExecContext(p.Ctx, "INSERT INTO public.regproc_casts VALUES('boolin'::text,'abs(integer)'::text)")
		regprocError(t, err, "42804", `column "r" is of type regproc but expression is of type text`, "INSERT INTO public.regproc_casts VALUES('boolin'::text,'abs(integer)'::text)", "assignment", "")
	})
}
