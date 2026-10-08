//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseConstraintsReviewMetadataCopies(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("SET standard_conforming_strings TO on", 0))
		run(referenceExec(`CREATE TABLE original(v TIME(3) DEFAULT TIME(3) '12:34:56.1234',n INT DEFAULT -2147483648,q TEXT DEFAULT 'a''b\c',CONSTRAINT c CHECK(num_nonnulls(v,NULL)>0))`, 0))
		for settingIndex, setting := range []string{"on", "off"} {
			run(referenceExec("SET standard_conforming_strings TO "+setting, 0))
			text := `'a''b\c'::text`
			if setting == "off" {
				text = `'a''b\\c'::text`
			}
			wants := []string{"'12:34:56.1234'::time(3) without time zone", "'-2147483648'::integer", text}
			for mode, pretty := range []bool{false, true} {
				actual := make([]string, len(wants))
				for i, want := range wants {
					query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
					step := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
					actual[i] = run(step).rows[0][0].(string)
					run(referencePrepared(step))
				}
				query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
				want := "CHECK ((num_nonnulls(v, NULL::unknown) > 0))"
				if pretty {
					want = "CHECK (num_nonnulls(v, NULL::unknown) > 0)"
				}
				step := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
				suffix := run(step).rows[0][0].(string)
				run(referencePrepared(step))
				name := fmt.Sprintf("copy_%d_%d", settingIndex, mode)
				constraint := fmt.Sprintf("copied_%d_%d", settingIndex, mode)
				run(referenceExec(fmt.Sprintf("CREATE TABLE %s(v TIME(3) DEFAULT %s,n INT DEFAULT %s,q TEXT DEFAULT %s,CONSTRAINT %s %s)", name, actual[0], actual[1], actual[2], constraint, suffix), 0))
				run(referenceExec("INSERT INTO "+name+" DEFAULT VALUES", 1))
				deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name, "TIME(3)", "12:34:56.123")
				deparseReviewTypedValue(t, conn, postgres, "SELECT n FROM "+name, "INT", "-2147483648")
				deparseReviewTypedValue(t, conn, postgres, "SELECT q FROM "+name, "TEXT", `a'b\c`)
				failure := referenceImplicitError("INSERT INTO "+name+"(v) VALUES(NULL)", referenceCheckViolation(name, constraint, `null, -2147483648, a'b\c`))
				run(failure)
				run(referencePrepared(failure))
			}
		}
	})
}

func TestReferenceDeparseConstraintsReviewNumericSizingCopies(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE TABLE original(v NUMERIC(2,-1) DEFAULT '149',w NUMERIC(2,-1) DEFAULT '995',a INT,CONSTRAINT c CHECK(num_nulls(a,NULL::integer)=1))", 0))
		for mode, pretty := range []bool{false, true} {
			var expressions [2]string
			for i, want := range []string{"'149'::numeric", "'995'::numeric"} {
				query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original' AND d.adnum=%d", pretty, i+1)
				step := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
				expressions[i] = run(step).rows[0][0].(string)
				run(referencePrepared(step))
			}
			query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)
			want := "CHECK ((num_nulls(a, NULL::integer) = 1))"
			if pretty {
				want = "CHECK (num_nulls(a, NULL::integer) = 1)"
			}
			step := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
			suffix := run(step).rows[0][0].(string)
			run(referencePrepared(step))
			run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(v NUMERIC(2,-1) DEFAULT %s,w NUMERIC(2,-1) DEFAULT %s,a INT,CONSTRAINT copied%d %s)", mode, expressions[0], expressions[1], mode, suffix), 0))
		}
		for i, name := range []string{"original", "copy0", "copy1"} {
			run(referenceExec("INSERT INTO "+name+"(w,a) VALUES(0,1)", 1))
			deparseReviewTypedValue(t, conn, postgres, "SELECT v FROM "+name, "NUMERIC(2,-1)", "150")
			deparseReviewTypedValue(t, conn, postgres, "SELECT w FROM "+name, "NUMERIC(2,-1)", "0")
			constraint := "c"
			if i > 0 {
				constraint = fmt.Sprintf("copied%d", i-1)
			}
			run(referenceImplicitError("INSERT INTO "+name+"(w,a) VALUES(0,NULL)", referenceCheckViolation(name, constraint, "150, 0, null")))
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"INSERT INTO " + name + "(a) VALUES(1)", "INSERT INTO " + name + "(w,a) VALUES(0,1),(DEFAULT,1)"} {
					step := referenceImplicitError(query, referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 2, scale -1 must round to an absolute value less than 10^3."})
					step.prepared, step.errorStage = prepared, "execute"
					run(step)
					run(referenceSelect("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
			}
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
			deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, postgres bool, run func(referenceStep) referenceObserved) {
				run(referenceExec("SET TIME ZONE 'UTC'", 0))
				run(referenceExec("SET IntervalStyle TO 'postgres'", 0))
				expression := "TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '" + tc.input + "'"
				run(referenceExec("CREATE TABLE original(a INT,CONSTRAINT c CHECK("+expression+" IS NOT NULL))", 0))
				for mode, pretty := range []bool{false, true} {
					// Exact successful golden coverage lives in cov_interval_zone.
					// These negative providers are serialized without evaluation and
					// copied from actual driver-returned suffixes, not input text.
					var suffix string
					if err := conn.QueryRowContext(t.Context(), fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", pretty)).Scan(&suffix); err != nil {
						t.Fatal(err)
					}
					run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(a INT,CONSTRAINT copied%d %s)", mode, mode, suffix), 0))
				}
				for _, name := range []string{"original", "copy0", "copy1"} {
					for _, prepared := range []bool{false, true} {
						step := referenceExec("INSERT INTO "+name+" VALUES(1)", 1)
						if tc.message != "" {
							step = referenceImplicitError(step.query, referenceDiagnostic{Code: "22023", Message: tc.message})
							step.errorStage = "execute"
						}
						step.prepared = prepared
						run(step)
					}
					count := int64(0)
					if tc.message == "" {
						count = 2
					}
					run(referenceSelect("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				}
				if tc.message == "" {
					deparseReviewTypedValue(t, conn, postgres, "SELECT "+expression+" FROM original WHERE a=1 LIMIT 1", "TIMESTAMPTZ", "2026-10-07 22:00:00+00")
				}
			})
		})
	}
}
