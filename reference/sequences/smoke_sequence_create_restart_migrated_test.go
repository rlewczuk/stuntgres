package reference_test

import (
	"bytes"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeSequenceCreateRestartSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					// Controls target this notice connector's engine, not the parent pair.
					f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
					c := f.A
					exec := func(q string) {
						fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: q, Prepared: prepared})
					}
					value := func(q string, want int64) {
						label := strings.TrimPrefix(q, "SELECT ")
						if i := strings.Index(label, "("); i >= 0 {
							label = label[:i]
						}
						sequenceRestartValue(t, p, c, f.Recorder, q, label, want)
					}
					state := func(name string, last int64, called bool) {
						sequenceRestartState(t, p, c, f.Recorder, backend.PG, name, last, called)
					}
					exec("CREATE SEQUENCE sentinel START 41")
					value("SELECT nextval('sentinel')", 41)
					for _, tc := range []struct {
						name, ddl string
						initial   int64
					}{
						{"bare", "CREATE SEQUENCE bare START 5 RESTART", 5},
						{"explicit", "CREATE SEQUENCE explicit START 5 RESTART 9", 9},
						{"with_value", "CREATE SEQUENCE with_value START 5 RESTART WITH 9", 9},
						{"temporary", "CREATE TEMP SEQUENCE temporary START 5 RESTART 9", 9},
						{"unlogged", "CREATE UNLOGGED SEQUENCE unlogged START 5 RESTART WITH 9", 9},
						{"absent", "CREATE SEQUENCE IF NOT EXISTS absent START 5 RESTART WITH 9", 9},
					} {
						exec(tc.ddl)
						state(tc.name, tc.initial, false)
						value("SELECT currval('sentinel')", 41)
						value("SELECT lastval()", 41)
						d := fixture.TempReferenceDiagnostic("55000", `currval of sequence "`+tc.name+`" is not yet defined in this session`, "", "")
						fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: "SELECT currval('" + tc.name + "')", SelectRows: true, State: d.Code, Diagnostic: d, ErrorStage: "execute"})
						value("SELECT nextval('"+tc.name+"')", tc.initial)
						exec("ALTER SEQUENCE " + tc.name + " RESTART")
						state(tc.name, 5, false)
						value("SELECT nextval('"+tc.name+"')", 5)
						value("SELECT nextval('sentinel')", 42)
						value("SELECT setval('sentinel',41,true)", 41)
					}
					for _, tc := range []struct{ name, options, code, message string }{
						{"low", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 0", "22023", "RESTART value (0) cannot be less than MINVALUE (1)"},
						{"high", "MINVALUE 1 MAXVALUE 10 START 5 RESTART 11", "22023", "RESTART value (11) cannot be greater than MAXVALUE (10)"},
						{"duplicate", "START 5 RESTART 9 RESTART WITH 8", "42601", "conflicting or redundant options"},
					} {
						q := "CREATE SEQUENCE " + tc.name + " " + tc.options
						d := fixture.TempReferenceDiagnostic(tc.code, tc.message, "", "")
						if tc.code == "42601" {
							d.Position = strconv.Itoa(strings.LastIndex(q, "RESTART") + 1)
						}
						fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: q, Prepared: prepared, State: tc.code, Diagnostic: d, ErrorStage: "execute"})
						missing := fixture.TempReferenceDiagnostic("42P01", `relation "`+tc.name+`" does not exist`, "", "24")
						fixture.RunStep(t, p.Ctx, c, f.Recorder, fixture.Step{Query: "SELECT last_value FROM " + tc.name, SelectRows: true, State: missing.Code, Diagnostic: missing, ErrorStage: "execute"})
						state("sentinel", 41, true)
						value("SELECT currval('sentinel')", 41)
						value("SELECT lastval()", 41)
					}
					exec("CREATE SEQUENCE IF NOT EXISTS sentinel RESTART 0")
					wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sentinel" already exists, skipping`}
					if len(*f.Notices) != 1 || (*f.Notices)[0] != wantNotice {
						t.Fatalf("notices %#v want %#v", *f.Notices, wantNotice)
					}
					state("sentinel", 41, true)
					value("SELECT currval('sentinel')", 41)
					value("SELECT lastval()", 41)
					exec("CREATE SEQUENCE cached START 5 RESTART WITH 9 CACHE 3")
					state("cached", 9, false)
					value("SELECT nextval('cached')", 9)
					state("cached", 11, true)
					value("SELECT nextval('cached')", 10)
					state("cached", 11, true)
					if prepared {
						q := "CREATE SEQUENCE retained START 5 RESTART WITH 9"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						for range 2 {
							fixture.RunStep(t, p.Ctx, h, f.Recorder, fixture.Exec(q, 0))
							state("retained", 9, false)
							value("SELECT nextval('retained')", 9)
							exec("ALTER SEQUENCE retained RESTART")
							value("SELECT nextval('retained')", 5)
							exec("DROP SEQUENCE retained")
						}
					}
					exec("CREATE SEQUENCE roundtrip START 5 RESTART WITH 9")
					state("roundtrip", 9, false)
					if !backend.PG {
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
						state("roundtrip", 9, false)
						var redump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
							t.Fatal("roundtrip dump", err, redump.String())
						}
					}
					value("SELECT nextval('roundtrip')", 9)
					exec("ALTER SEQUENCE roundtrip RESTART")
					state("roundtrip", 5, false)
					value("SELECT nextval('roundtrip')", 5)
				})
			}
		})
	}
}
