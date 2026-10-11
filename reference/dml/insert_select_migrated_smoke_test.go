package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeInsertSelectSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE s(i INT,v TEXT,b BOOL)", 0))
			run(fixture.Exec("INSERT INTO s VALUES(1,'a',TRUE),(1,'a',TRUE),(2,NULL,FALSE)", 3))
			run(fixture.Exec("CREATE TABLE t(v TEXT,i BIGINT,b BOOL)", 0))
			query := "INSERT INTO t(i,v) SELECT i,v FROM s WHERE b=$1 ORDER BY i"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: query}
			for _, tc := range []struct {
				arg   any
				count int64
			}{{nil, 0}, {false, 1}, {true, 2}} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(query, tc.count, tc.arg))
			}
			run(fixture.Select("SELECT * FROM t ORDER BY i", []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, [][]any{{"a", int64(1), nil}, {"a", int64(1), nil}, {nil, int64(2), nil}}))
			run(fixture.Exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE FALSE", 0))
			run(fixture.Exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE i=2", 1))
			run(fixture.Exec("INSERT INTO t(i,v) SELECT i,v FROM s WHERE b", 2))
			run(fixture.Exec("CREATE TABLE contextual(i INT)", 0))
			run(fixture.Exec("INSERT INTO contextual (VALUES ('1'))", 1))
			run(fixture.Exec("INSERT INTO contextual SELECT $1", 1, "7"))
			run(fixture.Select("SELECT i FROM contextual ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}}))
			// The command goes through QueryContext and publishes an empty cursor,
			// including its zero-column metadata before iteration.
			run(fixture.Select("INSERT INTO contextual SELECT 8", []string{}, []string{}, [][]any{}))
			run(fixture.Exec("CREATE TABLE self(i INT)", 0))
			run(fixture.Exec("INSERT INTO self VALUES(1),(1),(2)", 3))
			run(fixture.Exec("INSERT INTO self SELECT * FROM self", 3))
			self := fixture.Select("SELECT i FROM self ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}})
			run(self)
			run(fixture.Exec("CREATE TABLE narrowing(i INT)", 0))
			run(fixture.Exec("INSERT INTO narrowing VALUES(9)", 1))
			run(fixture.Exec("CREATE TABLE wide(i BIGINT)", 0))
			run(fixture.Exec("INSERT INTO wide VALUES(1),(2147483648)", 2))
			for _, tc := range []struct {
				query string
				want  fixture.Diagnostic
			}{
				{"INSERT INTO missing SELECT 1", fixture.Diagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "13"}},
				{"INSERT INTO t(missing) SELECT 1", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"}},
				{"INSERT INTO t(i) SELECT 1,2", fixture.Diagnostic{Code: "42601", Message: "INSERT has more expressions than target columns", Position: "27"}},
				{"INSERT INTO t(i) SELECT 'bad'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type bigint: "bad"`, Position: "25"}},
				{"INSERT INTO narrowing SELECT i FROM wide ORDER BY i", fixture.Diagnostic{Code: "22003", Message: "integer out of range"}},
			} {
				failure := fixture.ImplicitError(tc.query, tc.want)
				failure.ErrorStage = "execute"
				run(failure)
			}
			run(fixture.Select("SELECT i FROM narrowing", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
			if backend.PG {
				return
			}
			// Dump/Restore and the replay query-source exclusion are native APIs.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			d := stuntgres.NewDriver()
			connector, err := d.OpenConnector(t.Name() + "/clone")
			if err != nil {
				t.Fatal(err)
			}
			clone := sql.OpenDB(connector)
			defer clone.Close()
			if err := stuntgres.Restore(p.Ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			wrapped, recorder := fixture.InsertSelectRecordedPool(t, connector)
			defer wrapped.Close()
			fixture.RunStep(t, p.Ctx, wrapped, recorder, self)
			fixture.RunStep(t, p.Ctx, wrapped, recorder, fixture.Select("SELECT * FROM t ORDER BY i", []string{"v", "i", "b"}, []string{"TEXT", "INT8", "BOOL"}, [][]any{{"a", int64(1), nil}, {"a", int64(1), nil}, {"a", int64(1), nil}, {"a", int64(1), nil}, {nil, int64(2), nil}, {nil, int64(2), nil}}))
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, clone, strings.NewReader("CREATE TABLE self(i INT); INSERT INTO self SELECT $1")), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "restore INSERT query sources are unsupported"})
			fixture.RunStep(t, p.Ctx, wrapped, recorder, self)
		})
	}
}
