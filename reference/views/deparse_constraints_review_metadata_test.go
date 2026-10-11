package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseConstraintsReviewMetadataCopies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			postgres := backend.PG
			run := func(s fixture.Step) fixture.Observed {
				if s.SelectRows && s.Diagnostic == nil {
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.DeparseReviewMetadata(t, postgres, rows) }
				}
				return fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			run(fixture.Exec("SET standard_conforming_strings TO on", 0))
			run(fixture.Exec(`CREATE TABLE original(v TIME(3) DEFAULT TIME(3) '12:34:56.1234',n INT DEFAULT -2147483648,q TEXT DEFAULT 'a''b\c',CONSTRAINT c CHECK(num_nonnulls(v,NULL)>0))`, 0))
			for settingIndex, setting := range []string{"on", "off"} {
				run(fixture.Exec("SET standard_conforming_strings TO "+setting, 0))
				text := `'a''b\c'::text`
				if setting == "off" {
					text = `'a''b\\c'::text`
				}
				wants := []string{"'12:34:56.1234'::time(3) without time zone", "'-2147483648'::integer", text}
				for mode, pretty := range []bool{false, true} {
					actual := make([]string, len(wants))
					for i, want := range wants {
						query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
						step := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
						actual[i] = run(step).Rows[0][0].(string)
						run(fixture.Prepared(step))
					}
					query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
					want := "CHECK ((num_nonnulls(v, NULL::unknown) > 0))"
					if pretty {
						want = "CHECK (num_nonnulls(v, NULL::unknown) > 0)"
					}
					step := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
					suffix := run(step).Rows[0][0].(string)
					run(fixture.Prepared(step))
					name := fmt.Sprintf("copy_%d_%d", settingIndex, mode)
					constraint := fmt.Sprintf("copied_%d_%d", settingIndex, mode)
					run(fixture.Exec(fmt.Sprintf("CREATE TABLE %s(v TIME(3) DEFAULT %s,n INT DEFAULT %s,q TEXT DEFAULT %s,CONSTRAINT %s %s)", name, actual[0], actual[1], actual[2], constraint, suffix), 0))
					run(fixture.Exec("INSERT INTO "+name+" DEFAULT VALUES", 1))
					deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name, "TIME(3)", "12:34:56.123")
					deparseReviewTypedValue(t, conn, postgres, "SELECT n FROM "+name, "INT", "-2147483648")
					deparseReviewTypedValue(t, conn, postgres, "SELECT q FROM "+name, "TEXT", `a'b\c`)
					failure := fixture.ImplicitError("INSERT INTO "+name+"(v) VALUES(NULL)", fixture.CheckViolation(name, constraint, `null, -2147483648, a'b\c`))
					run(failure)
					run(fixture.Prepared(failure))
				}
			}
			run(fixture.Exec("SET standard_conforming_strings TO on", 0))
			if !postgres {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			for _, pretty := range []bool{false, true} {
				for i, want := range []string{"'12:34:56.1234'::time(3) without time zone", "'-2147483648'::integer", `'a''b\c'::text`} {
					step := fixture.Select(fmt.Sprintf("SELECT pg_get_expr(adbin,0,%t) AS d FROM pg_catalog.pg_attrdef WHERE adnum=%d", pretty, i+1), []string{"d"}, []string{"TEXT"}, nil)
					step.Query = fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
					step.Rows = [][]any{{want}}
					run(step)
					run(fixture.Prepared(step))
				}
			}
			run(fixture.Prepared(fixture.Select("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (num_nonnulls(v, NULL::unknown) > 0)"}})))
			run(fixture.Exec("INSERT INTO original DEFAULT VALUES", 1))
			deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM original", "TIME(3)", "12:34:56.123")
			deparseReviewTypedValue(t, conn, postgres, "SELECT n FROM original", "INT", "-2147483648")
			deparseReviewTypedValue(t, conn, postgres, "SELECT q FROM original", "TEXT", `a'b\c`)
			run(fixture.ImplicitError("INSERT INTO original(v) VALUES(NULL)", fixture.CheckViolation("original", "c", `null, -2147483648, a'b\c`)))
		})
	}
}

func TestReferenceDeparseConstraintsReviewNumericSizingCopies(t *testing.T) {
	fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(fixture.Step) fixture.Observed) {
		run(fixture.Exec("CREATE TABLE original(v NUMERIC(2,-1) DEFAULT '149',w NUMERIC(2,-1) DEFAULT '995',a INT,CONSTRAINT c CHECK(num_nulls(a,NULL::integer)=1))", 0))
		for mode, pretty := range []bool{false, true} {
			var expressions [2]string
			for i, want := range []string{"'149'::numeric", "'995'::numeric"} {
				query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
				step := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
				expressions[i] = run(step).Rows[0][0].(string)
				run(fixture.Prepared(step))
			}
			query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
			want := "CHECK ((num_nulls(a, NULL::integer) = 1))"
			if pretty {
				want = "CHECK (num_nulls(a, NULL::integer) = 1)"
			}
			step := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
			suffix := run(step).Rows[0][0].(string)
			run(fixture.Prepared(step))
			run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(v NUMERIC(2,-1) DEFAULT %s,w NUMERIC(2,-1) DEFAULT %s,a INT,CONSTRAINT copied%d %s)", mode, expressions[0], expressions[1], mode, suffix), 0))
		}
		for i, name := range []string{"original", "copy0", "copy1"} {
			run(fixture.Exec("INSERT INTO "+name+"(w,a) VALUES(0,1)", 1))
			deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name, "NUMERIC(2,-1)", "150")
			deparseReviewTypedValue(t, conn, postgres, "SELECT w FROM "+name, "NUMERIC(2,-1)", "0")
			constraint := "c"
			if i > 0 {
				constraint = fmt.Sprintf("copied%d", i-1)
			}
			run(fixture.ImplicitError("INSERT INTO "+name+"(w,a) VALUES(0,NULL)", fixture.CheckViolation(name, constraint, "150, 0, null")))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"INSERT INTO " + name + "(a) VALUES(1)", "INSERT INTO " + name + "(w,a) VALUES(0,1),(DEFAULT,1)"} {
					step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 2, scale -1 must round to an absolute value less than 10^3."})
					step.Prepared, step.ErrorStage = prepared, "execute"
					run(step)
					run(fixture.Select("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
			}
			shape := fixture.Select("SELECT v,w FROM "+name, []string{"v", "w"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"150", "0"}})
			shape.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil || len(columns) != 2 {
					t.Fatal(columns, err)
				}
				scale := int64(-1)
				if postgres {
					scale = 2047 // pq exposes the unsigned encoded 11-bit scale.
				}
				for _, column := range columns {
					if precision, actualScale, known := column.DecimalSize(); !known || precision != 2 || actualScale != scale {
						t.Fatal(column.Name(), precision, actualScale, known)
					}
				}
			}
			run(shape)
		}
	})
}

func TestReferenceDeparseConstraintsReviewIntervalZoneExecution(t *testing.T) {
	for _, tc := range []struct{ input, message string }{
		{"02:00", ""},
		{"1 month", `interval time zone "1 mon" must not include months or days`},
		{"1 day", `interval time zone "1 day" must not include months or days`},
		{"infinity", `interval time zone "infinity" must be finite`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			fixture.DeparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(fixture.Step) fixture.Observed) {
				run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
				run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
				expression := "TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '" + tc.input + "'"
				run(fixture.Exec("CREATE TABLE original(a INT,CONSTRAINT c CHECK("+expression+" IS NOT NULL))", 0))
				for mode, pretty := range []bool{false, true} {
					// Exact successful golden coverage lives in cov_interval_zone.
					// These negative providers are serialized without evaluation and
					// copied from actual driver-returned suffixes, not input text.
					var suffix string
					if err := conn.QueryRowContext(t.Context(), fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)).Scan(&suffix); err != nil {
						t.Fatal(err)
					}
					run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(a INT,CONSTRAINT copied%d %s)", mode, mode, suffix), 0))
				}
				for _, name := range []string{"original", "copy0", "copy1"} {
					for _, prepared := range []bool{false, true} {
						step := fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1)
						if tc.message != "" {
							step = fixture.ImplicitError(step.Query, fixture.Diagnostic{Code: "22023", Message: tc.message})
							step.ErrorStage = "execute"
						}
						step.Prepared = prepared
						run(step)
					}
					retained := [][]any{}
					if tc.message == "" {
						retained = [][]any{{int64(1)}, {int64(1)}}
					}
					run(fixture.Select("SELECT a FROM "+name+" ORDER BY a", []string{"a"}, []string{"INT4"}, retained))
				}
				if tc.message == "" {
					deparseReviewTypedValue(t, conn, postgres, "SELECT "+expression+" FROM original WHERE a=1 LIMIT 1", "TIMESTAMPTZ", "2026-10-07 22:00:00+00")
				}
			})
		})
	}
}
