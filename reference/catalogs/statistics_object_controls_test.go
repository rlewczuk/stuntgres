package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsObjectTypes(t *testing.T) {
	for _, name := range []string{"pg_ndistinct", "pg_dependencies", "pg_mcv_list"} {
		t.Run(name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					typ := statisticsDatabaseTypeName(b.PG, name)
					metadata := func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						scan := reflect.TypeFor[string]()
						if b.PG {
							scan = reflect.TypeFor[any]()
						}
						if len(columns) != 1 || columns[0].DatabaseTypeName() != typ || columns[0].ScanType() != scan {
							t.Fatal("real statistics metadata", columns)
						}
					}
					for _, prepared := range []bool{false, true} {
						strict := fixture.Select("SELECT "+name+"_in(NULL) AS i,"+name+"_out(NULL::"+name+") AS o,"+name+"_send(NULL::"+name+") AS s", []string{"i", "o", "s"}, []string{typ, "CSTRING", "BYTEA"}, [][]any{{nil, nil, nil}})
						if prepared {
							strict = fixture.Prepared(strict)
						}
						run(strict)
						for _, q := range []string{"SELECT NULL::" + name + " AS v", "SELECT $1::" + name + " AS v"} {
							var args []any
							if strings.Contains(q, "$1") {
								args = []any{nil}
							}
							s := fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{nil}}, args...)
							s.CheckMetadata = metadata
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
						}
						for _, value := range []string{"", "x"} {
							q := "SELECT '" + value + "'::" + name
							want := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type "+name)
							want.Position = "8"
							s := fixture.ErrorDiagnostic(q, want)
							s.SelectRows = true
							s.ErrorStage = "execute"
							if prepared {
								s = fixture.Prepared(s)
								s.ErrorStage = "prepare"
							}
							run(s)
						}
						diagnostic := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type "+name)
						diagnostic.Context = "unnamed portal parameter $1 = '...'"
						parameter := fixture.ErrorDiagnostic("SELECT $1::"+name, diagnostic, "x")
						parameter.SelectRows = true
						parameter.ErrorStage = "execute"
						if prepared {
							parameter = fixture.Prepared(parameter)
						}
						run(parameter)
					}
					run(fixture.Exec("CREATE TABLE stats_null(v "+name+")", 0))
					run(fixture.Exec("INSERT INTO stats_null VALUES(NULL)", 1))
					run(fixture.Prepared(fixture.Exec("INSERT INTO stats_null VALUES($1)", 1, nil)))
					run(fixture.Select("SELECT v FROM stats_null", []string{"v"}, []string{typ}, [][]any{{nil}, {nil}}))
					run(fixture.Select("SELECT typname,typtype,typcategory,typlen,typbyval,typalign,typstorage,typcollation::bigint AS typcollation,typarray::bigint AS typarray FROM pg_type WHERE typname=$1", []string{"typname", "typtype", "typcategory", "typlen", "typbyval", "typalign", "typstorage", "typcollation", "typarray"}, []string{"NAME", "CHAR", "CHAR", "INT2", "BOOL", "CHAR", "CHAR", "INT8", "INT8"}, [][]any{{name, "b", "Z", int64(-1), false, "i", "x", int64(100), int64(0)}}, name))
				})
			}
		})
	}
}

func TestReferenceStatisticsObjectControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, s) }
	for _, q := range []string{"CREATE TABLE stats_t(a INT,b INT,c INT)", "ALTER TABLE stats_t DROP COLUMN b", "ALTER TABLE stats_t RENAME COLUMN c TO renamed_c", "CREATE STATISTICS ON renamed_c,a FROM stats_t", "CREATE TABLE stats_null(n pg_ndistinct,d pg_dependencies,m pg_mcv_list)"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO stats_t VALUES(1,3)", 1))
	run(fixture.Exec("INSERT INTO stats_null VALUES(NULL,NULL,NULL)", 1))
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Len() == 0 || !strings.Contains(saved.String(), "CREATE STATISTICS") {
		t.Fatal("missing genuine statistics dump")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			check := func(db fixture.Executor, plain bool) {
				s := statisticsRows([]any{"stats_t_renamed_c_a_stat", "1 3", "{d,f,m}", nil, nil})
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
				s = fixture.Select("SELECT * FROM stats_t", []string{"a", "renamed_c"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(3)}})
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
				s = fixture.Select("SELECT * FROM stats_null", []string{"n", "d", "m"}, []string{statisticsDatabaseTypeName(b.PG, "pg_ndistinct"), statisticsDatabaseTypeName(b.PG, "pg_dependencies"), statisticsDatabaseTypeName(b.PG, "pg_mcv_list")}, [][]any{{nil, nil, nil}})
				s.NonContext = plain
				fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
			}
			if b.PG {
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("actual native statistics dump rejected by PostgreSQL", err)
				}
				check(conn, false)
				return
			}
			check(b.DB, false)
			old, err := p.Native.QueryContext(p.Ctx, "SELECT * FROM pg_statistic_ext WHERE stxname='stats_t_renamed_c_a_stat'")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			columns, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			statisticExtMetadata(t, false, columns)
			for _, suffix := range []string{"CREATE STATISTICS invalid ON missing,a FROM stats_t;", "CREATE STATISTICS invalid (bogus) ON a,renamed_c FROM stats_t;", "SET SESSION AUTHORIZATION unknown_statistics_owner;"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+suffix)); err == nil {
					t.Fatal("invalid staged Restore published", suffix)
				}
				check(b.DB, false)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='stats_t_renamed_c_a_stat'", []string{"stxname"}, []string{"NAME"}, nil))
			if !old.Next() {
				t.Fatal("held definition lost after Reset", old.Err())
			}
			vals := make([]any, 9)
			dest := make([]any, 9)
			for i := range vals {
				dest[i] = &vals[i]
			}
			if err := old.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			if fixture.Normalize(vals[2]) != "stats_t_renamed_c_a_stat" || fixture.Normalize(vals[5]) != "1 3" {
				t.Fatal(vals)
			}
			old.Close()
			statisticExtMetadata(t, false, columns)
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.DB, false)
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Bytes(), again.Bytes()) {
				t.Fatal("statistics dump roundtrip changed", saved.String(), again.String())
			}
			d := stuntgres.NewDriver()
			connector, err := d.OpenConnector(t.Name() + "-collision")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(connector)
			defer other.Close()
			if _, err := other.ExecContext(p.Ctx, "CREATE TABLE collision(v INT)"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(other, true)
		})
	}
}
