package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceSessionDecimalIntegerBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
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
		owner, peer := settingsPhysical(t, a), settingsPhysical(t, b)
		run := func(c referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
		run(a, referenceExec("CREATE TABLE sentinel(v INT)", 0))
		run(a, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
		run(b, referenceExec("SET application_name TO peer", 0))
		assertState := func(want string, prepared bool) {
			t.Helper()
			for _, value := range []struct {
				conn *sql.Conn
				want string
			}{{a, want}, {b, "peer"}} {
				show := localReferenceSelect(backend.pg, "SHOW application_name", "application_name", value.want)
				show.prepared = prepared
				run(value.conn, show)
				var sentinel int64
				if err := value.conn.QueryRowContext(p.ctx, "SELECT v FROM sentinel").Scan(&sentinel); err != nil || sentinel != 7 {
					t.Fatal("sentinel changed", sentinel, err)
				}
				run(value.conn, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			if settingsPhysical(t, a) != owner || settingsPhysical(t, b) != peer {
				t.Fatal("numeric boundary replaced physical session")
			}
		}
		for _, prepared := range []bool{false, true} {
			run(a, referenceExec("SET application_name TO retained", 0))
			if prepared {
				run(a, referencePrepared(referenceExec("SET application_name TO retained", 0)))
			}
			for _, scalar := range []struct{ text, value string }{{"1_000", "1000"}, {"0x2a", "42"}, {"0O_1_2", "10"}, {"0b0", "0"}, {"0x7FFF_FFFF", "2147483647"}} {
				for _, sign := range []string{"", "+", "-"} {
					query := "SET application_name TO " + sign + scalar.text
					want := "retained"
					var step referenceStep
					if backend.pg {
						step = referenceExec(query, 0)
						want = scalar.value
						if sign == "-" && want != "0" {
							want = "-" + want
						}
					} else {
						position := "25"
						if sign != "" {
							position = "26"
						}
						step = referenceErrorDiagnostic(query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "non-decimal setting integers are unsupported", Position: position})
						step.errorStage = "execute"
						if prepared {
							step.errorStage = "prepare"
						}
					}
					step.prepared = prepared
					run(a, step)
					assertState(want, prepared)
				}
			}
			for _, text := range []string{"1_000", "0x2a"} {
				step := referenceExec("SET application_name TO '"+text+"'", 0)
				step.prepared = prepared
				run(a, step)
				assertState(text, prepared)
			}
		}
	}
}
