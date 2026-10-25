package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
	"unicode/utf8"
)

func infoTimeStampScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			for _, q := range []string{
				"SET TIME ZONE 'UTC'", "SET DateStyle='ISO, MDY'", "SET search_path TO public",
				"CREATE TABLE public.info_time_stamp_witness(id integer,v information_schema.time_stamp)",
			} {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec(q, 0))
			}
			body(t, p, backend, conn, recorder)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %#v", notices)
			}
		})
	}
}

func infoTimeStampMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	infoTimeStampColumnTypes(t, columns, pg)
}

func infoTimeStampColumnTypes(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	if c.Name() != "v" || c.DatabaseTypeName() != "TIMESTAMPTZ" || c.ScanType() != reflect.TypeOf(time.Time{}) || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
		t.Fatalf("metadata name=%s type=%s scan=%v length=%d,%v decimal=%d,%d,%v", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK)
	}
	if nullable != !pg || nullableOK != !pg {
		t.Fatalf("nullable=%v,%v want %v,%v", nullable, nullableOK, !pg, !pg)
	}
}

func infoTimeStampRows(pg bool, query string, want [][]any) fixture.Step {
	s := fixture.Select(query, []string{"v"}, []string{"TIMESTAMPTZ"}, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { infoTimeStampMetadata(t, rows, pg) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		t.Helper()
		if want[row][column] == nil {
			if value != nil {
				t.Fatalf("raw NULL=%T(%v)", value, value)
			}
			return true
		}
		got, ok := value.(time.Time)
		expected := want[row][column].(time.Time)
		if !ok || !got.Equal(expected) {
			t.Fatalf("raw instant=%T(%v) want %v", value, value, expected)
		}
		return true
	}
	return s
}

func infoTimeStampBoth(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, s)
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
}

func infoTimeStampSeed(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (1,'2004-10-19 10:23:54.126+02'),(2,NULL)", 2))
}

func infoTimeStampStored(pg bool) fixture.Step {
	return infoTimeStampRows(pg, "SELECT v FROM public.info_time_stamp_witness ORDER BY id", [][]any{{time.Date(2004, 10, 19, 8, 23, 54, 130000000, time.UTC)}, {nil}})
}

func infoTimeStampIdentityStep() fixture.Step {
	return fixture.Select("SELECT 'information_schema.time_stamp'::regtype::text AS domain,'pg_catalog.timestamptz'::regtype::text AS base", []string{"domain", "base"}, []string{"TEXT", "TEXT"}, [][]any{{"information_schema.time_stamp", "timestamp with time zone"}})
}

func TestReferenceInfoTimeStampValues(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		for _, prepared := range []bool{false, true} {
			run := func(s fixture.Step) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
			}
			run(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (1,'2004-10-19 10:23:54.126+02'),(2,NULL)", 2))
			run(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (3,$1)", 1, "2004-10-19 10:23:54.123456+02"))
			run(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (4,$1)", 1, time.Date(2004, 10, 19, 10, 23, 54, 126000000, time.FixedZone("supplied", 7200))))
			run(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (5,$1)", 1, nil))
			run(fixture.Exec("INSERT INTO public.info_time_stamp_witness VALUES (6,'2004-10-19 23:59:59.999+00')", 1))
			want := [][]any{
				{time.Date(2004, 10, 19, 8, 23, 54, 130000000, time.UTC)}, {nil},
				{time.Date(2004, 10, 19, 8, 23, 54, 120000000, time.UTC)},
				{time.Date(2004, 10, 19, 8, 23, 54, 130000000, time.UTC)}, {nil},
				{time.Date(2004, 10, 20, 0, 0, 0, 0, time.UTC)},
			}
			run(infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness ORDER BY id", want))
			run(fixture.Select("SELECT v::text AS v FROM public.info_time_stamp_witness ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-10-19 08:23:54.13+00"}, {nil}, {"2004-10-19 08:23:54.12+00"}, {"2004-10-19 08:23:54.13+00"}, {nil}, {"2004-10-20 00:00:00+00"}}))
			run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
			run(infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness ORDER BY id", want))
			run(fixture.Select("SELECT v::text AS v FROM public.info_time_stamp_witness WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"2004-10-19 04:23:54.13-04"}}))
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			run(fixture.Exec("DELETE FROM public.info_time_stamp_witness", 6))
		}
	})
}

func TestReferenceInfoTimeStampMetadata(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoTimeStampSeed(t, p, conn, recorder)
		for _, tc := range []fixture.Step{
			infoTimeStampStored(backend.PG),
			infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness WHERE id=99", [][]any{}),
			infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness WHERE id=2", [][]any{{nil}}),
		} {
			infoTimeStampBoth(t, p, conn, recorder, tc)
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := fixture.TransactionQuery(p.Ctx, conn, tc.Query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					infoTimeStampMetadata(t, rows, backend.PG)
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
						var value any
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					infoTimeStampColumnTypes(t, columns, backend.PG)
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					infoTimeStampColumnTypes(t, columns, backend.PG)
				}()
			}
		}
	})
}

func TestReferenceInfoTimeStampDiagnostics(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoTimeStampSeed(t, p, conn, recorder)
		for _, tc := range []struct{ input, code, message string }{
			{"nonsense", "22007", `invalid input syntax for type timestamp with time zone: "nonsense"`},
			{"2024-02-30 10:00+00", "22008", `date/time field value out of range: "2024-02-30 10:00+00"`},
			{"2004-10-19 10:00+16", "22009", `time zone displacement out of range: "2004-10-19 10:00+16"`},
			{"2004-10-19 10:00 No/Such_Zone", "22023", `time zone "no/such_zone" not recognized`},
		} {
			t.Run(tc.input, func(t *testing.T) {
				q := "/*é*/ INSERT INTO public.info_time_stamp_witness VALUES (90,'2004-10-19 10:00+00'),(91,'" + tc.input + "')"
				position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'"+tc.input)]) + 1)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
				}
				q = "/*é*/ INSERT INTO public.info_time_stamp_witness VALUES (90,'2004-10-19 10:00+00'),(91,$1)"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.input)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
				}
			})
		}
		q := "INSERT INTO public.info_time_stamp_witness VALUES (3,$1)"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`, Context: "unnamed portal parameter $1 = '...'"}, "nonsense")
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, recorder, s)
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(q, 1, "2004-10-19 10:23:54.126+02"))
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DELETE FROM public.info_time_stamp_witness WHERE id=3", 1))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		// pq can discard a physical connection on pre-cancellation. Use separate
		// borrowers so that the admitted scenario connection remains an observer.
		for _, read := range []bool{false, true} {
			canceledConn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer canceledConn.Close()
				var err error
				if read {
					var rows *sql.Rows
					rows, err = canceledConn.QueryContext(ctx, infoTimeStampStored(backend.PG).Query)
					if rows != nil {
						defer rows.Close()
						if !backend.PG || err != nil {
							t.Fatalf("canceled read exposed cursor: %v", err)
						}
						// Query admission and iteration race database/sql's context
						// watcher. Validate metadata and every emitted prefix value;
						// only a clean EOF requires the complete ordered result.
						columns, metadataErr := rows.ColumnTypes()
						if metadataErr != nil {
							if terminal := rows.Err(); errors.Is(terminal, context.Canceled) || errors.Is(terminal, driver.ErrBadConn) {
								return
							}
							t.Fatal(metadataErr)
						}
						infoTimeStampColumnTypes(t, columns, true)
						var got [][]any
						want := infoTimeStampStored(true)
						for rows.Next() {
							var value any
							if err := rows.Scan(&value); err != nil {
								if errors.Is(err, context.Canceled) || errors.Is(err, driver.ErrBadConn) {
									return
								}
								t.Fatal(err)
							}
							if len(got) >= len(want.Rows) {
								t.Fatal("canceled read returned extra row")
							}
							want.CheckRaw(t, 0, len(got), value)
							got = append(got, []any{fixture.Normalize(value)})
						}
						if terminal := rows.Err(); terminal != nil {
							if !errors.Is(terminal, context.Canceled) && !errors.Is(terminal, driver.ErrBadConn) {
								t.Fatal(terminal)
							}
						} else if !reflect.DeepEqual(got, want.Rows) {
							t.Fatalf("completed canceled read=%#v want %#v", got, want.Rows)
						}
						return
					}
				} else {
					var result sql.Result
					result, err = canceledConn.ExecContext(ctx, q, "2004-10-19 10:00+00")
					if result != nil {
						t.Fatal("canceled INSERT exposed result")
					}
				}
				if !errors.Is(err, context.Canceled) && !(backend.PG && errors.Is(err, driver.ErrBadConn)) {
					t.Fatalf("canceled operation read=%v: %v", read, err)
				}
			}()
		}
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampStored(backend.PG))
	})
}

func TestReferenceInfoTimeStampIdentity(t *testing.T) {
	infoTimeStampScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder) {
		infoTimeStampSeed(t, p, conn, recorder)
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampIdentityStep())
		infoTimeStampBoth(t, p, conn, recorder, fixture.Select("SELECT t.typtype AS kind,n.nspname AS domain_schema,t.typname AS domain_name,b.oid::integer AS base_oid,bn.nspname AS base_schema,b.typname AS base_name,t.typtypmod AS precision,t.typnotnull AS required,t.typndims AS dimensions,(t.oid>0 AND t.oid<>b.oid AND t.typarray>0 AND t.typarray<>t.oid AND a.typelem=t.oid AND a.typnamespace=t.typnamespace) AS identity FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace JOIN pg_type a ON a.oid=t.typarray WHERE n.nspname='information_schema' AND t.typname='time_stamp'", []string{"kind", "domain_schema", "domain_name", "base_oid", "base_schema", "base_name", "precision", "required", "dimensions", "identity"}, []string{"CHAR", "NAME", "NAME", "INT4", "NAME", "NAME", "INT4", "BOOL", "INT4", "BOOL"}, [][]any{{"d", "information_schema", "time_stamp", int64(1184), "pg_catalog", "timestamptz", int64(2), false, int64(0), true}}))
		tx, err := conn.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var inherited time.Time
		if err := tx.QueryRowContext(p.Ctx, "SELECT CURRENT_TIMESTAMP(2)").Scan(&inherited); err != nil {
			t.Fatal(err)
		}
		inherited = inherited.UTC()
		for i, prepared := range []bool{false, true} {
			s := fixture.Exec("INSERT INTO public.info_time_stamp_witness(id) VALUES ($1)", 1, int64(i+3))
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, tx, recorder, s)
			s = infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness WHERE id=$1", [][]any{{inherited}})
			s.Args = []any{int64(i + 3)}
			infoTimeStampBoth(t, p, tx, recorder, s)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		infoTimeStampBoth(t, p, conn, recorder, infoTimeStampRows(backend.PG, "SELECT v FROM public.info_time_stamp_witness WHERE id=2", [][]any{{nil}}))
		if !backend.PG {
			return
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT data_type::text,domain_schema::text,domain_name::text,udt_schema::text,udt_name::text,datetime_precision::integer,is_nullable::text FROM information_schema.columns WHERE table_schema='public' AND table_name='info_time_stamp_witness' AND column_name='v'", []string{"data_type", "domain_schema", "domain_name", "udt_schema", "udt_name", "datetime_precision", "is_nullable"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "TEXT"}, [][]any{{"timestamp with time zone", "information_schema", "time_stamp", "pg_catalog", "timestamptz", int64(2), "YES"}}),
			fixture.Select("SELECT domain_schema::text,domain_name::text,data_type::text,udt_schema::text,udt_name::text,datetime_precision::integer,domain_default::text FROM information_schema.domains WHERE domain_schema='information_schema' AND domain_name='time_stamp'", []string{"domain_schema", "domain_name", "data_type", "udt_schema", "udt_name", "datetime_precision", "domain_default"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "TEXT"}, [][]any{{"information_schema", "time_stamp", "timestamp with time zone", "pg_catalog", "timestamptz", int64(2), "CURRENT_TIMESTAMP(2)"}}),
		} {
			infoTimeStampBoth(t, p, conn, recorder, s)
		}
	})
}
