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

func citextPatternStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	citextInstall(t, p, b, c)
	projection, _, _ := citextPatternProjection("t")
	for _, op := range citextPatternOps {
		projection = strings.ReplaceAll(projection, "s "+op.sql+" ", "s OPERATOR(public."+op.catalog+") ")
	}
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE citext_pattern_source(id integer,s citext,p citext,t text)", 0),
		fixture.Exec("INSERT INTO citext_pattern_source VALUES (1,'ABC','abc','abc'),(2,'ABC','abd','abd'),(3,NULL,'abc','abc')", 3),
		fixture.Exec("CREATE VIEW citext_pattern_view AS SELECT id,"+projection+",s ~ '(?c)^abc$'::citext AS sensitive,'A%B'::citext LIKE 'a!%b'::citext ESCAPE '!'::citext AS escaped FROM citext_pattern_source", 0),
		fixture.Exec("CREATE VIEW citext_pattern_routines AS SELECT id,public.texticlike(s,p) AS a,public.texticnlike(s,p) AS b,public.texticregexeq(s,p) AS c,public.texticregexne(s,p) AS d,public.texticlike(s,t) AS e,public.texticnlike(s,t) AS f,public.texticregexeq(s,t) AS g,public.texticregexne(s,t) AS h FROM citext_pattern_source", 0),
		fixture.Exec("CREATE TABLE citext_pattern_guard(id integer,s citext,p citext,CONSTRAINT citext_pattern_match CHECK(s ~ p))", 0),
		fixture.Exec("INSERT INTO citext_pattern_guard VALUES (1,'ABC','abc'),(4,NULL,'abc')", 2),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func citextPatternViewStep() fixture.Step {
	_, names, types := citextPatternProjection("t")
	names = append([]string{"id"}, names...)
	names = append(names, "sensitive", "escaped")
	types = append([]string{"INT4"}, types...)
	types = append(types, "BOOL", "BOOL")
	return fixture.Select("SELECT * FROM citext_pattern_view ORDER BY id", names, types, citextPatternRows())
}

func citextPatternStoredValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	citextPatternRoutes(t, p, b, c, citextPatternViewStep())
	citextPatternRoutes(t, p, b, c, fixture.Select("SELECT * FROM citext_pattern_routines ORDER BY id", []string{"id", "a", "b", "c", "d", "e", "f", "g", "h"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
		{int64(1), true, false, true, false, true, false, true, false},
		{int64(2), false, true, false, true, false, true, false, true},
		{int64(3), nil, nil, nil, nil, nil, nil, nil, nil},
	}))
}

func citextPatternStoredChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	citextPatternStoredValues(t, p, b, c)
	for _, prepared := range []bool{false, true} {
		citextError(t, p, b, c, "INSERT INTO citext_pattern_guard VALUES (2,'ABC','abc'),(3,'ABC','[')", prepared, false, false, citextPatternRegexDiagnostic("brackets [] not balanced"))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "citext_pattern_guard" violates check constraint "citext_pattern_match"`)
		d.Detail = "Failing row contains (3, ABC, abd)."
		d.Schema, d.Table, d.Constraint = "public", "citext_pattern_guard", "citext_pattern_match"
		citextError(t, p, b, c, "INSERT INTO citext_pattern_guard VALUES (2,'ABC','abc'),(3,'ABC','abd')", prepared, false, false, d)
		citextPatternRoutes(t, p, b, c, fixture.Select("SELECT id,s::text AS s,p::text AS p FROM citext_pattern_guard ORDER BY id", []string{"id", "s", "p"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "ABC", "abc"}, {int64(4), nil, "abc"}}))
	}
}

func citextPatternRetained(t *testing.T, rows *sql.Rows, metadata []*sql.ColumnType) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		values := make([]any, len(metadata))
		dst := make([]any, len(metadata))
		for i := range values {
			dst[i] = &values[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, citextPatternRows()) {
		t.Fatalf("retained pattern rows %#v", got)
	}
	for i, col := range metadata {
		want := "BOOL"
		if i == 0 {
			want = "INT4"
		}
		if col.DatabaseTypeName() != want {
			t.Fatalf("retained type %d: %q want %q", i, col.DatabaseTypeName(), want)
		}
	}
}

func citextPatternRoundTrip(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, citextPatternViewStep().Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	citextPatternStoredValues(t, p, b, c)
	citextPatternRetained(t, old, metadata)
}

func TestReferenceCitextPatternControls(t *testing.T) {
	for _, phase := range []string{"same_engine_roundtrip", "emitted_replay", "independent_driver", "failed_restore", "owner_corruption"} {
		t.Run(phase, func(t *testing.T) {
			citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextPatternStoredSetup(t, p, b, c)
				citextPatternStoredChecks(t, p, b, c)
				// The emitted-stream witness checks the original catalog inventory;
				// the same-engine witness checks it after the genuine Restore.
				if phase != "same_engine_roundtrip" {
					citextPatternCatalogChecks(t, p, b, c)
				}
				if b.PG {
					return
				} // Native APIs are not PostgreSQL SQL behavior.
				if phase == "same_engine_roundtrip" {
					citextPatternRoundTrip(t, p, b, c)
					citextPatternStoredChecks(t, p, b, c)
					citextPatternCatalogChecks(t, p, b, c)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if phase == "emitted_replay" {
					if p.Postgres != nil {
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
						if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatal("actual emitted pattern SQL replay", err)
						}
						pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
						citextPatternStoredChecks(t, p, pg, p.Postgres)
						citextPatternCatalogChecks(t, p, pg, p.Postgres)
						if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}
					return
				}
				if phase == "independent_driver" {
					target := fixture.NewFixture(t, false, true)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE citext_pattern_allocator_spacer(v integer)", 0))
					if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					destination := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
					citextPatternStoredChecks(t, target, destination, target.Memory)
					citextPatternCatalogChecks(t, target, destination, target.Memory)
					var first, second bytes.Buffer
					if err := stuntgres.Dump(target.Ctx, target.Native, &first); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(first.Bytes(), second.Bytes()) {
						t.Fatal("remapped dump is nondeterministic")
					}
					return
				}
				{
					old, err := c.QueryContext(p.Ctx, citextPatternViewStep().Query)
					if err != nil {
						t.Fatal(err)
					}
					defer old.Close()
					metadata, err := old.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					var identity int64
					if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE oid='citext'::regtype::oid").Scan(&identity); err != nil {
						t.Fatal(err)
					}
					bad := "CREATE EXTENSION citext; CREATE TABLE citext_pattern_late(s citext,p citext,CHECK(s ~ p)); INSERT INTO citext_pattern_late VALUES ('ABC','abc'),('ABC','[');"
					if phase == "owner_corruption" {
						const marker = "-- stuntgres-extension-owners-v3 "
						start := strings.Index(dump.String(), marker)
						if start < 0 {
							t.Fatal("missing genuine owner directive")
						}
						end := strings.IndexByte(dump.String()[start:], '\n')
						if end < 0 {
							t.Fatal("unterminated owner directive")
						}
						end += start
						bad = dump.String()[:start] + dump.String()[end+1:] + marker + "!invalid-base64!\n"
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
						var state interface{ SQLState() string }
						if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
							t.Fatal("owner corruption must reject as control error", err)
						}
					} else {
						d := citextPatternRegexDiagnostic("brackets [] not balanced")
						fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
					}
					citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT oid::bigint=$1 AS v FROM pg_type WHERE oid='citext'::regtype::oid", true, identity))
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(after.Bytes(), dump.Bytes()) {
						t.Fatal("failed Restore changed original dump")
					}
					citextPatternRetained(t, old, metadata)
				}
				citextPatternStoredChecks(t, p, b, c)
				citextPatternCatalogChecks(t, p, b, c)
			})
		})
	}
}
