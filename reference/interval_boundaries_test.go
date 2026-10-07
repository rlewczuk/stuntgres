//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalGenericTypmodNativeRoundTrip(t *testing.T) {
	p := newReferencePair(t)
	cases := []struct {
		mask int
		out  string
	}{{4, "1 year"}, {2, "1 year 1 mon"}, {8, "1 year 1 mon 2 days"}, {1024, "1 year 1 mon 2 days 03:00:00"}, {2048, "1 year 1 mon 2 days 03:04:00"}, {6, "1 year 1 mon"}, {1032, "1 year 1 mon 2 days 03:00:00"}, {3080, "1 year 1 mon 2 days 03:04:00"}, {3072, "1 year 1 mon 2 days 03:04:00"}}
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		for i, tc := range cases {
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec(fmt.Sprintf(`CREATE TABLE g%d(v "interval"(%d,2))`, i, tc.mask), 0))
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec(fmt.Sprintf("INSERT INTO g%d VALUES('13 months 2 days 3:04:05.6789')", i), 1))
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect(fmt.Sprintf("SELECT v FROM g%d", i), []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
		}
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec(`CREATE TABLE ivdefault(id INTEGER,v "interval"(4,2) DEFAULT '13 months')`, 0))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("INSERT INTO ivdefault(id) VALUES(1)", 1))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec(`CREATE VIEW ivgenericview AS SELECT v FROM g0`, 0))
		for _, q := range []string{"SELECT v FROM ivdefault", "SELECT v FROM ivgenericview"} {
			referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 year"}}))
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	connector, err := referenceMemoryDriver.OpenConnector("reference/" + t.Name() + "/restore")
	if err != nil {
		t.Fatal(err)
	}
	dest := sql.OpenDB(connector)
	defer dest.Close()
	if err = stuntgres.Restore(p.ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err, dump.String())
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(p.ctx, dest, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(err, "generic typmods changed")
	}
	for i, tc := range cases {
		referenceRunStep(t, p.ctx, dest, &referenceRecorder{}, referenceSelect(fmt.Sprintf("SELECT v FROM g%d", i), []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
	}
	for _, q := range []string{"SELECT v FROM ivdefault", "SELECT v FROM ivgenericview"} {
		referenceRunStep(t, p.ctx, dest, &referenceRecorder{}, referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 year"}}))
	}
}

func TestReferenceIntervalBoundaryDiagnosticsAndAtomicity(t *testing.T) {
	for _, tc := range []struct{ name, input, usage, code, primary, sentinel string }{
		{"iso_first", "PT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_later", "P1YT1000000000000001S", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_date_first", "P1000000000000001Y", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_date_later", "P1Y1000000000000001M", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_month", "P2-1000000000000001-1", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_day", "P2-1-1000000000000001", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_minute", "PT1:1000000000000001:1", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"iso_alt_second", "PT1:1:1000000000000001", "INTERVAL", "22015", "interval field value out of range", "00:00:01"},
		{"year_month", "1 1-2", "INTERVAL", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"year_month_year", "1 1-2", "INTERVAL YEAR", "22007", "invalid input syntax for type interval", "00:00:00"},
		{"year_month_minute", "1 1-2", "INTERVAL MINUTE TO SECOND(2)", "22007", "invalid input syntax for type interval", "00:00:01"},
		{"precision", "2562047788:00:54.775807", "INTERVAL SECOND(2)", "22008", "interval out of range", "00:00:01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
					run(referenceExec("CREATE TABLE iv(v "+tc.usage+")", 0))
					run(referenceExec("INSERT INTO iv VALUES('1 second')", 1))
					message := tc.primary
					if tc.code != "22008" {
						message += ": \"" + tc.input + "\""
					}
					base := referenceDiagnostic{Code: tc.code, Message: message}
					sentinel := func() {
						run(referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.sentinel}}))
					}
					for _, prepared := range []bool{false, true} {
						for _, literal := range []struct{ q, pos string }{{"INSERT INTO iv VALUES('" + tc.input + "')", "23"}, {"INSERT INTO iv VALUES('1 day'),('" + tc.input + "')", "33"}} {
							want := base
							want.Position = literal.pos
							s := referenceImplicitError(literal.q, want)
							s.prepared = prepared
							s.errorStage = "execute"
							if prepared {
								s.errorStage = "prepare"
							}
							run(s)
							sentinel()
						}
						s := referenceImplicitError("INSERT INTO iv VALUES($1),($2)", base, "1 day", tc.input)
						s.prepared = prepared
						s.errorStage = "execute"
						if tc.code != "22008" {
							pg := *s.diagnostic
							pg.Context = "unnamed portal parameter $2 = '...'"
							s.postgresDiagnostic = &pg
						}
						run(s)
						sentinel()
					}
					if tc.code == "22008" {
						for _, prepared := range []bool{false, true} {
							want := base
							want.Position = "17"
							s := referenceImplicitError("SELECT INTERVAL '"+tc.input+"' SECOND(2) FROM iv", want)
							s.prepared = prepared
							s.errorStage = "execute"
							if prepared {
								s.errorStage = "prepare"
							}
							run(s)
							sentinel()
						}
					}
				})
			}
		})
	}
}

func TestReferenceIntervalISOAlternativeExponentWidth(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		var populated bool
		for _, tc := range []struct{ input, out string }{{"P00021015e0", "2 years 10 mons 15 days"}, {"P00021015.0e0", "2 years 10 mons 15 days"}, {"PT001020e0", "00:10:20"}, {"PT001020.0e0", "00:10:20"}} {
			for _, prepared := range []bool{false, true} {
				for _, parameterized := range []bool{false, true} {
					if populated {
						referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("DELETE FROM iv", 1))
					}
					s := referenceExec("INSERT INTO iv VALUES('"+tc.input+"')", 1)
					if parameterized {
						s = referenceExec("INSERT INTO iv VALUES($1)", 1, tc.input)
					}
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.record, s)
					populated = true
					referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.out}}))
				}
			}
		}
	}
}

func TestReferenceIntervalPrecisionOverflowSessionIsolation(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("CREATE TABLE iv(v INTERVAL SECOND(2))", 0))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceExec("INSERT INTO iv VALUES('1 second')", 1))
		a, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		referenceRunStep(t, p.ctx, a, backend.record, referenceExec("SET IntervalStyle=sql_standard", 0))
		referenceRunStep(t, p.ctx, b, backend.record, referenceExec("SET IntervalStyle=iso_8601", 0))
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("INSERT INTO iv VALUES($1),($2)", referenceDiagnostic{Code: "22008", Message: "interval out of range"}, "1 day", "2562047788:00:54.775807")
			s.prepared = prepared
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, a, backend.record, s)
			referenceRunStep(t, p.ctx, a, backend.record, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"sql_standard"}}))
			referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
			referenceRunStep(t, p.ctx, a, backend.record, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"0:00:01"}}))
			referenceRunStep(t, p.ctx, b, backend.record, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"PT1S"}}))
		}
	}
}
