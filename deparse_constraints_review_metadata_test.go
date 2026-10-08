package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsReviewMetadataReplay(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "SET standard_conforming_strings TO on")
	execSQL(t, db, `CREATE TABLE original(v TIME(3) DEFAULT TIME(3) '12:34:56.1234',n INT DEFAULT -2147483648,q TEXT DEFAULT 'a''b\c',CONSTRAINT c CHECK(num_nonnulls(v,NULL)>0))`)
	observe := func(off bool, pretty bool) []string {
		t.Helper()
		text := `'a''b\c'::text`
		if off {
			text = `'a''b\\c'::text`
		}
		wants := []string{"'12:34:56.1234'::time(3) without time zone", "'-2147483648'::integer", text}
		actual := make([]string, len(wants))
		for i, want := range wants {
			query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
			for _, prepared := range []bool{false, true} {
				deparseText(t, db, prepared, query, want)
			}
			if err := db.QueryRow(query).Scan(&actual[i]); err != nil {
				t.Fatal(err)
			}
		}
		return actual
	}
	for settingIndex, setting := range []string{"on", "off"} {
		execSQL(t, db, "SET standard_conforming_strings TO "+setting)
		for mode, pretty := range []bool{false, true} {
			expressions := observe(setting == "off", pretty)
			query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
			want := "CHECK ((num_nonnulls(v, NULL::unknown) > 0))"
			if pretty {
				want = "CHECK (num_nonnulls(v, NULL::unknown) > 0)"
			}
			deparseText(t, db, false, query, want)
			deparseText(t, db, true, query, want)
			var suffix string
			if err := db.QueryRow(query).Scan(&suffix); err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("copy_%d_%d", settingIndex, mode)
			execSQL(t, db, fmt.Sprintf("CREATE TABLE %s(v TIME(3) DEFAULT %s,n INT DEFAULT %s,q TEXT DEFAULT %s,CONSTRAINT copied_%d_%d %s)", name, expressions[0], expressions[1], expressions[2], settingIndex, mode, suffix))
			execSQL(t, db, "INSERT INTO "+name+" DEFAULT VALUES")
			deparseReviewTypedValue(t, db, "SELECT v FROM "+name, "TIME(3)", "12:34:56.123")
			deparseReviewTypedValue(t, db, "SELECT n FROM "+name, "INT", "-2147483648")
			deparseReviewTypedValue(t, db, "SELECT q FROM "+name, "TEXT", `a'b\c`)
		}
	}
	execSQL(t, db, "SET standard_conforming_strings TO on")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	observe(false, false)
	observe(false, true)
	deparseText(t, db, true, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", "CHECK (num_nonnulls(v, NULL::unknown) > 0)")
	execSQL(t, db, "INSERT INTO original DEFAULT VALUES")
	deparseReviewTypedValue(t, db, "SELECT v FROM original", "TIME(3)", "12:34:56.123")
	deparseReviewTypedValue(t, db, "SELECT n FROM original", "INT", "-2147483648")
	deparseReviewTypedValue(t, db, "SELECT q FROM original", "TEXT", `a'b\c`)
	_, err := db.Exec("INSERT INTO original(v) VALUES(NULL)")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "original" violates check constraint "c"`, Detail: `Failing row contains (null, -2147483648, a'b\c).`, Schema: "public", Table: "original", Constraint: "c"})
}

func TestDeparseConstraintsReviewNumericSizingCopies(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE original(v NUMERIC(2,-1) DEFAULT '149',w NUMERIC(2,-1) DEFAULT '995',a INT,CONSTRAINT c CHECK(num_nulls(a,NULL::integer)=1))")
	for mode, pretty := range []bool{false, true} {
		var expressions [2]string
		for i, want := range []string{"'149'::numeric", "'995'::numeric"} {
			query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
			for _, prepared := range []bool{false, true} {
				deparseText(t, db, prepared, query, want)
			}
			if err := db.QueryRow(query).Scan(&expressions[i]); err != nil {
				t.Fatal(err)
			}
		}
		query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
		want := "CHECK ((num_nulls(a, NULL::integer) = 1))"
		if pretty {
			want = "CHECK (num_nulls(a, NULL::integer) = 1)"
		}
		for _, prepared := range []bool{false, true} {
			deparseText(t, db, prepared, query, want)
		}
		var suffix string
		if err := db.QueryRow(query).Scan(&suffix); err != nil {
			t.Fatal(err)
		}
		execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(v NUMERIC(2,-1) DEFAULT %s,w NUMERIC(2,-1) DEFAULT %s,a INT,CONSTRAINT copied%d %s)", mode, expressions[0], expressions[1], mode, suffix))
	}
	for i, name := range []string{"original", "copy0", "copy1"} {
		execSQL(t, db, "INSERT INTO "+name+"(w,a) VALUES(0,1)")
		deparseReviewTypedValue(t, db, "SELECT v FROM "+name, "NUMERIC(2,-1)", "150")
		deparseReviewTypedValue(t, db, "SELECT w FROM "+name, "NUMERIC(2,-1)", "0")
		constraint := "c"
		if i > 0 {
			constraint = fmt.Sprintf("copied%d", i-1)
		}
		_, err := db.Exec("INSERT INTO " + name + "(w,a) VALUES(0,NULL)")
		notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "%s"`, name, constraint), Detail: "Failing row contains (150, 0, null).", Schema: "public", Table: name, Constraint: constraint})
		for _, prepared := range []bool{false, true} {
			for _, query := range []string{"INSERT INTO " + name + "(a) VALUES(1)", "INSERT INTO " + name + "(w,a) VALUES(0,1),(DEFAULT,1)"} {
				var err error
				if prepared {
					stmt, prepareErr := db.Prepare(query)
					if prepareErr != nil {
						t.Fatal("conversion must remain execution-owned", prepareErr)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(query)
				}
				notNullDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 2, scale -1 must round to an absolute value less than 10^3."})
				wantTuples(t, db, "SELECT COUNT(*) FROM "+name, [][]any{{int64(1)}})
			}
		}
		rows, err := db.Query("SELECT v,w FROM " + name)
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for _, column := range types {
			precision, scale, ok := column.DecimalSize()
			if !ok || precision != 2 || scale != -1 {
				rows.Close()
				t.Fatal(column, precision, scale, ok)
			}
		}
		rows.Close()
	}
}

func TestDeparseConstraintsReviewIntervalZoneExecution(t *testing.T) {
	for _, tc := range []struct{ input, message string }{
		{"02:00", ""},
		{"1 month", `interval time zone "1 mon" must not include months or days`},
		{"1 day", `interval time zone "1 day" must not include months or days`},
		{"infinity", `interval time zone "infinity" must be finite`},
	} {
		t.Run(tc.input, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(1)
			execSQL(t, db, "SET TIME ZONE 'UTC'")
			execSQL(t, db, "SET IntervalStyle TO 'postgres'")
			expression := "TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '" + tc.input + "'"
			execSQL(t, db, "CREATE TABLE original(a INT,CONSTRAINT c CHECK("+expression+" IS NOT NULL))")
			for mode, pretty := range []bool{false, true} {
				var suffix string
				if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)).Scan(&suffix); err != nil {
					t.Fatal(err)
				}
				execSQL(t, db, fmt.Sprintf("CREATE TABLE copy%d(a INT,CONSTRAINT copied%d %s)", mode, mode, suffix))
			}
			for _, name := range []string{"original", "copy0", "copy1"} {
				for _, prepared := range []bool{false, true} {
					query := "INSERT INTO " + name + " VALUES(1)"
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(query)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						_, err = stmt.Exec()
						stmt.Close()
					} else {
						_, err = db.Exec(query)
					}
					if tc.message == "" {
						if err != nil {
							t.Fatal(err)
						}
					} else {
						notNullDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: tc.message})
					}
				}
				retained := [][]any{}
				if tc.message == "" {
					retained = [][]any{{int64(1)}, {int64(1)}}
				}
				wantTuples(t, db, "SELECT a FROM "+name+" ORDER BY a", retained)
			}
			if tc.message == "" {
				deparseReviewTypedValue(t, db, "SELECT "+expression+" FROM original WHERE a=1 LIMIT 1", "TIMESTAMPTZ", "2026-10-07 22:00:00+00")
			}
		})
	}
}
