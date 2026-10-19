package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDiagnostics(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.dv AS varchar(3)", "CREATE DOMAIN domain_s.uuid AS integer", "CREATE TABLE domain_s.t (id integer,i domain_s.di,v domain_s.dv)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO domain_s.t VALUES (1,7,'abc'),(2,NULL,NULL)", 2))
		run(fixture.Select("SELECT '7'::domain_s.uuid AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// Complete records/stages are pinned expectations. The builtin-spelled
		// qualified domain witness is independently specified for root's next run.
		cases := []struct {
			query, state, message string
			position              int
			context               string
			args                  []any
			rows, failsPrepare    bool
		}{
			{"CREATE DOMAIN domain_s.di AS no_such_type", "42710", `type "di" already exists`, 0, "", nil, false, false},
			{"CREATE DOMAIN domain_s.bad AS no_such_type", "42704", `type "no_such_type" does not exist`, 31, "", nil, false, false},
			{"CREATE DOMAIN absent.di AS integer", "3F000", `schema "absent" does not exist`, 0, "", nil, false, false},
			{"CREATE DOMAIN domain_s.bad AS void", "42804", `"void" is not a valid base type for a domain`, 31, "", nil, false, false},
			{"CREATE DOMAIN domain_s.bad AS unknown", "42804", `"unknown" is not a valid base type for a domain`, 31, "", nil, false, false},
			{"CREATE DOMAIN domain_s.bad AS record", "42804", `"record" is not a valid base type for a domain`, 31, "", nil, false, false},
			{"CREATE DOMAIN domain_s.bad AS integer(3)", "42601", `syntax error at or near "("`, 38, "", nil, false, true},
			{"CREATE DOMAIN domain_s.bad AS domain_s.di(3)", "42601", `type modifier is not allowed for type "domain_s.di"`, 31, "", nil, false, false},
			{"INSERT INTO domain_s.t(id,i) VALUES (3,9),(4,'bad')", "22P02", `invalid input syntax for type integer: "bad"`, 46, "", nil, false, true},
			{"INSERT INTO domain_s.t(id,i) VALUES (3,2147483648)", "22003", "integer out of range", 0, "", nil, false, false},
			{"INSERT INTO domain_s.t(id,v) VALUES (3,'abcd')", "22001", "value too long for type character varying(3)", 0, "", nil, false, false},
			{"INSERT INTO domain_s.t(id,i) VALUES (3,$1)", "22P02", `invalid input syntax for type integer: "bad"`, 0, "unnamed portal parameter $1 = '...'", []any{"bad"}, false, false},
			{"INSERT INTO domain_s.t(id,i) VALUES (3,$1)", "22003", `value "2147483648" is out of range for type integer`, 0, "unnamed portal parameter $1 = '...'", []any{"2147483648"}, false, false},
			{"INSERT INTO domain_s.t(id,v) VALUES (3,$1)", "22001", "value too long for type character varying(3)", 0, "unnamed portal parameter $1 = '...'", []any{"abcd"}, false, false},
			{"SELECT 'bad'::domain_s.di AS v FROM public.one", "22P02", `invalid input syntax for type integer: "bad"`, 8, "", nil, true, true},
			{"SELECT /* é */ 'bad'::domain_s.di AS v FROM public.one", "22P02", `invalid input syntax for type integer: "bad"`, 16, "", nil, true, true},
			{"SELECT $1::domain_s.di AS v FROM public.one", "22P02", `invalid input syntax for type integer: "bad"`, 0, "unnamed portal parameter $1 = '...'", []any{"bad"}, true, false},
			{"SELECT 1::domain_s.di(3) FROM public.one", "42601", `type modifier is not allowed for type "domain_s.di"`, 11, "", nil, true, true},
			{"SELECT 1::domain_s.uuid(3) FROM public.one", "42601", `type modifier is not allowed for type "domain_s.uuid"`, 11, "", nil, true, true},
		}
		for _, tc := range cases {
			t.Run(tc.state+"_"+tc.query, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
					want.Position = fixture.Position(tc.position)
					want.Context = tc.context
					s := fixture.ErrorDiagnostic(tc.query, want, tc.args...)
					s.Prepared = prepared
					s.SelectRows = tc.rows
					s.ErrorStage = "execute"
					if prepared && tc.failsPrepare {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
				}
			})
		}
		run(fixture.Select("SELECT id,i,v FROM domain_s.t ORDER BY id", []string{"id", "i", "v"}, []string{"INT4", "INT4", "VARCHAR"}, [][]any{{int64(1), int64(7), "abc"}, {int64(2), nil, nil}}))
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT 'abcd'::domain_s.dv AS colon,CAST('abcd' AS domain_s.dv) AS cast FROM public.one", []string{"colon", "cast"}, []string{"VARCHAR", "VARCHAR"}, [][]any{{"abc", "abc"}})
			if prepared {
				s = fixture.Prepared(s)
			}
			run(s)
		}
		for _, q := range []string{
			"CREATE DOMAIN domain_s.checked AS integer CHECK (VALUE>0)",
			"CREATE DOMAIN domain_s.namedcheck AS integer CONSTRAINT positive CHECK (VALUE>0)",
		} {
			run(fixture.Exec(q, 0))
		}
		// DEFAULT/NOT NULL are shared positive contracts, not native boundaries.
		run(fixture.Exec("CREATE DOMAIN domain_s.defaulted AS integer DEFAULT 7", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.required AS integer CONSTRAINT required_name NOT NULL", 0))
		run(fixture.Exec("CREATE TABLE domain_s.positive (v domain_s.defaulted,r domain_s.required)", 0))
		run(fixture.Exec("INSERT INTO domain_s.positive(r) VALUES(9)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO domain_s.positive(v,r) VALUES(DEFAULT,$1)", 1, int64(10))))
		run(fixture.Select("SELECT v,r FROM domain_s.positive ORDER BY r", []string{"v", "r"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(7), int64(10)}}))
		for _, prepared := range []bool{false, true} {
			run(domainDefaultFailure("INSERT INTO domain_s.positive(r) VALUES(NULL)", domainNotNullDiagnostic("domain_s", "required"), prepared, false))
		}
		if backend.PG {
			run(fixture.Exec("CREATE DOMAIN domain_s.altered AS integer", 0))
			run(fixture.Exec("ALTER DOMAIN domain_s.altered SET DEFAULT 7", 0))
			run(fixture.Select("SELECT 9::domain_s.altered AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}}))
		} else {
			run(fixture.ImplicitError("ALTER DOMAIN domain_s.di SET DEFAULT 7", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "SET"`, Position: "26"}))
			run(fixture.Select("SELECT NULL::domain_s.di AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{nil}}))
		}
		// The parser must not publish a valid prefix when a suffix is malformed.
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "nonsense"`)
		want.Position = "44"
		s := fixture.ErrorDiagnostic("CREATE DOMAIN domain_s.trailing AS integer nonsense", want)
		s.ErrorStage = "execute"
		run(s)
		want = fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.trailing" does not exist`)
		want.Position = "32"
		s = fixture.ErrorDiagnostic("CREATE TABLE domain_s.probe (v domain_s.trailing)", want)
		s.ErrorStage = "execute"
		run(s)
		// Preserve the existing native utility-in-explicit-transaction boundary.
		run(fixture.Exec("BEGIN", 0))
		if backend.PG {
			run(fixture.Exec("CREATE DOMAIN domain_s.in_transaction AS integer", 0))
		} else {
			run(fixture.Error("CREATE DOMAIN domain_s.in_transaction AS integer", "0A000"))
		}
		run(fixture.Exec("ROLLBACK", 0))
		want = fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.in_transaction" does not exist`)
		want.Position = "32"
		s = fixture.ErrorDiagnostic("CREATE TABLE domain_s.probe (v domain_s.in_transaction)", want)
		s.ErrorStage = "execute"
		run(s)
	})
}
