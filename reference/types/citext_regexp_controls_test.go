package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func citextRegexpStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	citextRegexpCorpus(t, p, b, c)
	for _, q := range []string{
		"CREATE VIEW cr_scalar AS SELECT id,public.regexp_match(s,p) AS m,public.regexp_match(s,p,'c') AS c,public.regexp_replace(s,p,'X') AS r,public.regexp_replace(s,p,'X','g') AS g,public.regexp_split_to_array(s,p) AS a,public.regexp_split_to_array(s,p,'c') AS b FROM cr_source WHERE public.regexp_match(s,p) IS NOT NULL",
		"CREATE VIEW cr_sets AS SELECT id,public.regexp_matches(s,p) AS m,public.regexp_matches(s,p,'g') AS g,public.regexp_split_to_table(s,p) AS a,public.regexp_split_to_table(s,p,'c') AS b FROM cr_source",
		"CREATE TABLE cr_guard(s citext,p citext,CHECK(public.regexp_match(s,p) IS NOT NULL),r text DEFAULT public.regexp_replace('AbA'::citext,'a','X'))",
		"INSERT INTO cr_guard(s,p) VALUES('AbA','a')",
	} {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, n))
	}
}
func citextRegexpStoredValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id,m,c,r,g,a,b FROM cr_scalar ORDER BY id", []string{"id", "m", "c", "r", "g", "a", "b"}, []string{"INT4", "_TEXT", "_TEXT", "TEXT", "TEXT", "_TEXT", "_TEXT"}, [][]any{{int64(1), "{A}", nil, "XbA", "XbX", `{"",b,""}`, "{AbA}"}, {int64(4), `{""}`, `{""}`, "X", "X", `{""}`, `{""}`}}))
	s := fixture.Select("SELECT id,m,g,a,b FROM cr_sets", []string{"id", "m", "g", "a", "b"}, []string{"INT4", "_TEXT", "_TEXT", "TEXT", "TEXT"}, [][]any{
		{int64(1), "{A}", "{A}", "", "AbA"}, {int64(1), nil, "{A}", "b", nil}, {int64(1), nil, nil, "", nil},
		{int64(2), nil, nil, "x", "x"}, {int64(4), `{""}`, `{""}`, "", ""},
	})
	s.Bag = true
	citextPatternRoutes(t, p, b, c, s)
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT r FROM cr_guard", []string{"r"}, []string{"TEXT"}, [][]any{{"XbA"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cr_guard(s,p) VALUES('B','b')", 1))
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT s::text AS s,r FROM cr_guard ORDER BY s::text", []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"AbA", "XbA"}, {"B", "XbA"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM cr_guard WHERE s='B'", 1))
	for _, prepared := range []bool{false, true} {
		citextError(t, p, b, c, "INSERT INTO cr_guard(s,p) VALUES('C','c'),('x','[')", prepared, false, false, fixture.ControlsReferenceDiagnostic("2201B", "invalid regular expression: brackets [] not balanced"))
	}
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT s::text AS s,p::text AS p,r FROM cr_guard", []string{"s", "p", "r"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"AbA", "a", "XbA"}}))
}
func TestReferenceCitextRegexpControls(t *testing.T) {
	for _, phase := range []string{"same_engine_and_emitted_replay", "independent_driver", "failed_restore", "owner_corruption"} {
		t.Run(phase, func(t *testing.T) {
			citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextRegexpStoredSetup(t, p, b, c)
				citextRegexpStoredValues(t, p, b, c)
				citextRegexpCatalogChecks(t, p, b, c)
				if b.PG {
					// SQL recreation is the oracle lifecycle witness; native control APIs have no SQL equivalent.
					for _, q := range []string{"DROP VIEW cr_scalar,cr_sets", "DROP TABLE cr_guard,cr_source", "DROP EXTENSION citext"} {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					}
					citextRegexpStoredSetup(t, p, b, c)
					citextRegexpStoredValues(t, p, b, c)
					citextRegexpCatalogChecks(t, p, b, c)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				old, err := c.QueryContext(p.Ctx, "SELECT id,regexp_match(s,p) AS m FROM cr_source ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				metadata, err := old.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if phase == "independent_driver" {
					target := fixture.NewFixture(t, false, true)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE cr_spacer(v integer)", 0))
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					dest := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
					citextRegexpStoredValues(t, target, dest, target.Memory)
					citextRegexpCatalogChecks(t, target, dest, target.Memory)
					var first, second bytes.Buffer
					if err := stuntgres.Dump(target.Ctx, target.Native, &first); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(first.Bytes(), second.Bytes()) {
						t.Fatal("nondeterministic dump")
					}
				} else if phase == "same_engine_and_emitted_replay" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				} else {
					bad := "CREATE EXTENSION citext;CREATE TABLE cr_late(s citext,p citext,CHECK(regexp_match(s,p) IS NOT NULL));INSERT INTO cr_late VALUES('A','a'),('x','[');"
					if phase == "owner_corruption" {
						const marker = "-- stuntgres-extension-owners-v3 "
						start := strings.Index(dump.String(), marker)
						if start < 0 {
							t.Fatal("missing owner directive")
						}
						end := strings.IndexByte(dump.String()[start:], '\n')
						if end < 0 {
							t.Fatal("unterminated directive")
						}
						end += start
						bad = dump.String()[:start] + dump.String()[end+1:] + marker + "!invalid-base64!\n"
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
						var state interface{ SQLState() string }
						if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
							t.Fatal("owner corruption", err)
						}
					} else {
						d := fixture.ControlsReferenceDiagnostic("2201B", "invalid regular expression: brackets [] not balanced")
						fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
					}
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(after.Bytes(), dump.Bytes()) {
						t.Fatal("failed Restore changed catalog")
					}
				}
				var got [][]any
				for old.Next() {
					var id int64
					var m any
					if err := old.Scan(&id, &m); err != nil {
						t.Fatal(err)
					}
					got = append(got, []any{id, fixture.Normalize(m)})
				}
				if err := old.Err(); err != nil {
					t.Fatal(err)
				}
				old.Close()
				if !reflect.DeepEqual(got, [][]any{{int64(1), "{A}"}, {int64(2), nil}, {int64(3), nil}, {int64(4), `{""}`}}) || metadata[0].DatabaseTypeName() != "INT4" || metadata[1].DatabaseTypeName() != "_TEXT" {
					t.Fatal("retained reader", got, metadata)
				}
				citextRegexpStoredValues(t, p, b, c)
				citextRegexpCatalogChecks(t, p, b, c)
				if phase == "same_engine_and_emitted_replay" && p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual emitted SQL replay", err)
					}
					pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
					citextRegexpStoredValues(t, p, pg, p.Postgres)
					citextRegexpCatalogChecks(t, p, pg, p.Postgres)
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
func TestReferenceCitextRegexpFeatureSmoke(t *testing.T) {
	citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextRegexpStoredSetup(t, p, b, c)
		s := fixture.Select("SELECT regexp_match('AbA'::citext,'a') AS m,regexp_replace('AbA'::citext,'a','X','g') AS r,regexp_split_to_array('helloTworld'::citext,'t') AS s", []string{"m", "r", "s"}, []string{"_TEXT", "TEXT", "_TEXT"}, [][]any{{"{A}", "XbX", "{hello,world}"}})
		citextPatternRoutes(t, p, b, c, s)
		citextPatternRoutes(t, p, b, c, citextRegexpValue("SELECT regexp_match('AbA'::citext,'a','c') AS v", "_TEXT", nil))
		citextPatternRoutes(t, p, b, c, fixture.Select("SELECT v,ord FROM regexp_matches('AbA'::citext,'a','g') WITH ORDINALITY AS x(v,ord) ORDER BY ord", []string{"v", "ord"}, []string{"_TEXT", "INT8"}, [][]any{{"{A}", int64(1)}, {"{A}", int64(2)}}))
		citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id FROM cr_source WHERE regexp_match(s,p) IS NOT NULL ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}))
		q := "SELECT regexp_match($1::citext,$2::citext) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		bad := fixture.ErrorDiagnostic(q, citextRegexpDiagnostic("2201B", "invalid regular expression: brackets [] not balanced", "regexp_match"), "AbA", "[")
		bad.SelectRows = true
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, citextRegexpValue(q, "_TEXT", nil, "AbA", nil))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, citextRegexpValue(q, "_TEXT", "{A}", "AbA", "a"))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			citextRegexpStoredValues(t, p, b, c)
			citextPatternRoutes(t, p, b, c, s)
		}
	})
}
