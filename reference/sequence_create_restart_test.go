//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

// Expected records come from REL_18_0 sequence.c init_params, not backend echo.
// The Postgres selector remains independently runnable before the native fix.
func TestReferenceSequenceCreateRestart(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "Native", true: "Postgres"}[postgres], func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
					p := newReferencePair(t)
					f := newControlsReferenceFixture(t, p, postgres)
					c := f.a
					execute := func(q string) error {
						var r sql.Result
						var err error
						if prepared {
							s, e := c.PrepareContext(p.ctx, q)
							if e != nil {
								t.Fatal("utility Prepare", q, e)
							}
							defer s.Close()
							r, err = s.ExecContext(p.ctx)
						} else {
							r, err = c.ExecContext(p.ctx, q)
						}
						if err == nil {
							if n, e := r.RowsAffected(); e != nil || n != 0 {
								t.Fatal(q, n, e)
							}
						}
						return err
					}
					exec := func(q string) {
						if err := execute(q); err != nil {
							t.Fatal(q, err)
						}
					}
					value := func(q string, want int64) {
						referenceRunStep(t, p.ctx, c, f.recorder, referenceSelect(q, []string{strings.Split(strings.TrimPrefix(q, "SELECT "), "(")[0]}, []string{"INT8"}, [][]any{{want}}))
					}
					state := func(name string, last int64, called bool) {
						referenceRunStep(t, p.ctx, c, f.recorder, referenceSelect("SELECT last_value,is_called FROM "+name, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, called}}))
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
						var n int64
						want := controlsReferenceDiagnostic("55000", `currval of sequence "`+tc.name+`" is not yet defined in this session`)
						referenceAssertDiagnostic(t, c.QueryRowContext(p.ctx, "SELECT currval('"+tc.name+"')").Scan(&n), &want)
						if postgres {
							var start int64
							if err := c.QueryRowContext(p.ctx, "SELECT seqstart FROM pg_sequence WHERE seqrelid=$1::regclass", tc.name).Scan(&start); err != nil || start != 5 {
								t.Fatal("configured START", start, err)
							}
						}
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
						want := controlsReferenceDiagnostic(tc.code, tc.message)
						if tc.code == "42601" {
							want.Position = strconv.Itoa(strings.LastIndex(q, "RESTART") + 1)
						}
						referenceAssertDiagnostic(t, execute(q), &want)
						var n int64
						missing := controlsReferenceDiagnostic("42P01", `relation "`+tc.name+`" does not exist`)
						missing.Position = "24"
						referenceAssertDiagnostic(t, c.QueryRowContext(p.ctx, "SELECT last_value FROM "+tc.name).Scan(&n), &missing)
						state("sentinel", 41, true)
						value("SELECT currval('sentinel')", 41)
						value("SELECT lastval()", 41)
					}
					exec("CREATE SEQUENCE IF NOT EXISTS sentinel RESTART 0")
					wantNotice := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "sentinel" already exists, skipping`}
					if len(*f.notices) != 1 || (*f.notices)[0] != wantNotice {
						t.Fatalf("notices %#v want %#v", *f.notices, wantNotice)
					}
					state("sentinel", 41, true)
					value("SELECT currval('sentinel')", 41)
					value("SELECT lastval()", 41)
					value("SELECT nextval('sentinel')", 42)
					exec("CREATE SEQUENCE cached START 5 RESTART WITH 9 CACHE 3")
					state("cached", 9, false)
					value("SELECT nextval('cached')", 9)
					state("cached", 11, true)
					sequenceProbeValue(t, p.ctx, f.b, "SELECT nextval('cached')", 12)
					state("cached", 14, true)
					value("SELECT currval('cached')", 9)
					value("SELECT lastval()", 9)
					value("SELECT nextval('cached')", 10)
					if prepared {
						s, err := c.PrepareContext(p.ctx, "CREATE SEQUENCE retained START 5 RESTART WITH 9")
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						for range 2 {
							r, err := s.ExecContext(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							if n, err := r.RowsAffected(); err != nil || n != 0 {
								t.Fatal(n, err)
							}
							state("retained", 9, false)
							value("SELECT nextval('retained')", 9)
							exec("ALTER SEQUENCE retained RESTART")
							value("SELECT nextval('retained')", 5)
							exec("DROP SEQUENCE retained")
						}
					}
				})
			}
		})
	}
}
