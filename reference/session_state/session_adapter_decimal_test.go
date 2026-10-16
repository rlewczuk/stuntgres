package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionDecimalIntegerBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		a, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		owner, peer := fixture.SettingsPhysical(t, a), fixture.SettingsPhysical(t, b)
		run := func(c fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
		run(a, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
		run(b, fixture.Exec("SET application_name TO peer", 0))
		assertState := func(want string, prepared bool) {
			t.Helper()
			for _, value := range []struct {
				conn *sql.Conn
				want string
			}{{a, want}, {b, "peer"}} {
				show := localReferenceSelect(backend.PG, "SHOW application_name", "application_name", value.want)
				show.Prepared = prepared
				run(value.conn, show)
				var sentinel int64
				if err := value.conn.QueryRowContext(p.Ctx, "SELECT v FROM sentinel").Scan(&sentinel); err != nil || sentinel != 7 {
					t.Fatal("sentinel changed", sentinel, err)
				}
				run(value.conn, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			if fixture.SettingsPhysical(t, a) != owner || fixture.SettingsPhysical(t, b) != peer {
				t.Fatal("numeric boundary replaced physical session")
			}
		}
		for _, prepared := range []bool{false, true} {
			run(a, fixture.Exec("SET application_name TO retained", 0))
			if prepared {
				run(a, fixture.Prepared(fixture.Exec("SET application_name TO retained", 0)))
			}
			for _, scalar := range []struct{ text, value string }{{"1_000", "1000"}, {"0x2a", "42"}, {"0O_1_2", "10"}, {"0b0", "0"}, {"0x7FFF_FFFF", "2147483647"}} {
				for _, sign := range []string{"", "+", "-"} {
					query := "SET application_name TO " + sign + scalar.text
					want := "retained"
					var step fixture.Step
					if backend.PG {
						step = fixture.Exec(query, 0)
						want = scalar.value
						if sign == "-" && want != "0" {
							want = "-" + want
						}
					} else {
						position := "25"
						if sign != "" {
							position = "26"
						}
						step = fixture.ErrorDiagnostic(query, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "non-decimal setting integers are unsupported", Position: position})
						step.ErrorStage = "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
					}
					step.Prepared = prepared
					run(a, step)
					assertState(want, prepared)
				}
			}
			for _, text := range []string{"1_000", "0x2a"} {
				step := fixture.Exec("SET application_name TO '"+text+"'", 0)
				step.Prepared = prepared
				run(a, step)
				assertState(text, prepared)
			}
		}
	}
}
