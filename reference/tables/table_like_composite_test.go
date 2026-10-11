package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeCompositeSources(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE SCHEMA composite_sources", 0))
		run(fixture.Exec("CREATE TYPE composite_sources.source_type AS (a INT,b TEXT,flag BOOL)", 0))
		run(fixture.Exec("CREATE TYPE composite_sources.limited_type AS (v VARCHAR(3))", 0))
		run(fixture.Exec("CREATE TYPE composite_sources.empty_type AS ()", 0))
		options := []string{"", "INCLUDING ALL", "INCLUDING COMMENTS", "INCLUDING COMPRESSION", "INCLUDING CONSTRAINTS", "INCLUDING DEFAULTS", "INCLUDING GENERATED", "INCLUDING IDENTITY", "INCLUDING INDEXES", "INCLUDING STATISTICS", "INCLUDING STORAGE", "INCLUDING ALL EXCLUDING ALL"}
		for i, option := range options {
			name := fmt.Sprintf("shape%d", i)
			run(fixture.Exec("CREATE TABLE "+name+"(LIKE composite_sources.source_type "+option+")", 0))
			run(fixture.Select("SELECT * FROM "+name, []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, nil))
			fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM "+name, []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, []bool{true, true, true}, 0)
			run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
			run(fixture.Select("SELECT * FROM "+name, []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_indexes WHERE schemaname='public' AND tablename='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		run(fixture.Exec("CREATE TABLE interleaved(lead BIGINT,LIKE composite_sources.source_type INCLUDING ALL,middle BOOL,LIKE composite_sources.limited_type,tail TEXT)", 0))
		columns, types := []string{"lead", "a", "b", "flag", "middle", "v", "tail"}, []string{"INT8", "INT4", "TEXT", "BOOL", "BOOL", "VARCHAR", "TEXT"}
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM interleaved", columns, types, []bool{true, true, true, true, true, true, true}, 0)
		run(fixture.Exec("INSERT INTO interleaved VALUES(9,7,'O''Brien',true,false,'abc','tail')", 1))
		witness := fixture.Select("SELECT * FROM interleaved", columns, types, [][]any{{int64(9), int64(7), "O'Brien", true, false, "abc", "tail"}})
		run(witness)
		run(fixture.Exec("CREATE TABLE empty_copy(LIKE composite_sources.empty_type INCLUDING ALL)", 0))
		run(fixture.Select("SELECT * FROM empty_copy", []string{}, []string{}, nil))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM empty_copy", []string{}, []string{}, []bool{}, 0)
		for _, query := range []string{"CREATE TABLE failed(LIKE composite_sources.source_type INCLUDING ALL,a INT)", "CREATE TABLE failed(LIKE composite_sources.source_type,LIKE composite_sources.source_type INCLUDING ALL)"} {
			run(fixture.TableLikeError(query, fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`}))
			fixture.TableLikeAbsent(run, "failed", prepared)
		}
		run(fixture.Exec("CREATE TABLE failed(LIKE composite_sources.source_type INCLUDING ALL)", 0))
		run(fixture.Exec("INSERT INTO failed VALUES(11,'fresh',false)", 1))
		// Native composite removal uses the existing schema-cascade provider,
		// not unsupported DROP TYPE syntax. Public copies must remain independent.
		run(fixture.Exec("DROP SCHEMA composite_sources CASCADE", 0))
		for i := range options {
			run(fixture.Select("SELECT * FROM "+fmt.Sprintf("shape%d", i), []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}}))
		}
		run(fixture.Select("SELECT * FROM empty_copy", []string{}, []string{}, nil))
		run(fixture.Exec("INSERT INTO shape0 VALUES(12,'independent',true)", 1))
		run(fixture.Exec("INSERT INTO shape1 VALUES(13,'all',false)", 1))
		run(fixture.Select("SELECT * FROM failed", []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(11), "fresh", false}}))
		run(witness)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(witness)
			fixture.TableDDLMetadata(t, p.Ctx, c, false, prepared, "SELECT * FROM interleaved", columns, types, []bool{true, true, true, true, true, true, true}, 1)
			run(fixture.Exec("INSERT INTO shape1 VALUES(14,'restored',true)", 1))
			run(fixture.Select("SELECT * FROM shape1 ORDER BY a NULLS FIRST", []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}, {int64(13), "all", false}, {int64(14), "restored", true}}))
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("actual composite-copy SQL replay", err)
				}
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, witness)
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO shape1 VALUES(14,'restored',true)", 1))
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT * FROM shape1 ORDER BY a NULLS FIRST", []string{"a", "b", "flag"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{nil, nil, nil}, {int64(13), "all", false}, {int64(14), "restored", true}}))
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
