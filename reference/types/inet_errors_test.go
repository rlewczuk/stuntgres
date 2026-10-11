package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func inetDiagnostic(typ, input string, host bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22P02", fmt.Sprintf("invalid input syntax for type %s: %q", typ, input))
	if host {
		d.Message = fmt.Sprintf("invalid cidr value: %q", input)
		d.Detail = "Value has bits set to right of mask."
	}
	return d
}

func TestReferenceINETMalformedInputs(t *testing.T) {
	inputs := []string{"256.1.2.3", "1.2.3.4.5", "1::2::3", "10000::", "1.2.3.4/-1", "1.2.3.4/33", "::/129", "1.2.3.4/", "::/abc", "1.2.3.4/255.255.255.0", " 1.2.3.4", "1.2.3.4 ", "[::1]", "fe80::1%eth0", "::ffff:001.2.3.4", "::/0128"}
	for _, typ := range []string{"inet", "cidr"} {
		for i, input := range inputs {
			t.Run(fmt.Sprintf("%s_%02d", typ, i), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "SELECT '" + input + "'::" + typ
					d := inetDiagnostic(typ, input, false)
					d.Position = fixture.Position(8 + utf8.RuneCountInString(prefix))
					p.Step(t, fixture.ErrorDiagnostic(q, d))
					s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
					s.ErrorStage = "prepare"
					p.Step(t, s)
				}
			})
		}
	}
	for _, input := range []string{"192.168.0.1/24", "2001:db8::1/113"} {
		t.Run("host_"+input, func(t *testing.T) {
			p := fixture.NewScenario(t)
			d := inetDiagnostic("cidr", input, true)
			d.Position = "8"
			p.Step(t, fixture.ErrorDiagnostic("SELECT '"+input+"'::cidr", d))
		})
	}
	p := fixture.NewScenario(t)
	for _, typ := range []string{"inet", "cidr"} {
		d := inetDiagnostic(typ, "bad", false)
		d.Position = "13"
		p.Step(t, fixture.ErrorDiagnostic("SELECT "+strings.ToUpper(typ)+" 'bad'", d))
	}
	d := inetDiagnostic("inet", "192.168/24", false)
	d.Position = "8"
	p.Step(t, fixture.ErrorDiagnostic("SELECT '192.168/24'::inet", d))
}

func TestReferenceINETErrorStagesAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(id INTEGER,h INET,c CIDR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO n VALUES(1,'::1','10')", 1))
	saved := fixture.Select("SELECT id,h,c FROM n ORDER BY id", []string{"id", "h", "c"}, []string{"INT4", "INET", "CIDR"}, [][]any{{int64(1), "::1", "10.0.0.0/8"}})
	for _, tc := range []struct {
		q, typ, input string
		host          bool
	}{
		{"INSERT INTO n VALUES(2,'1.2.3.4','10'),(3,'bad','10')", "inet", "bad", false},
		{"INSERT INTO n VALUES(2,'1.2.3.4','10'),(3,'::1','192.168.0.1/24')", "cidr", "192.168.0.1/24", true},
	} {
		d := inetDiagnostic(tc.typ, tc.input, tc.host)
		d.Position = fixture.Position(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, "'"+tc.input+"'")]) + 1)
		p.Step(t, fixture.ErrorDiagnostic(tc.q, d))
		p.Step(t, saved)
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			q := "INSERT INTO n VALUES($1,$2,$3)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			d := inetDiagnostic("inet", "bad", false)
			d.Context = "unnamed portal parameter $2 = '...'"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.ErrorDiagnostic(q, d, 2, "bad", "10"))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, saved)
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 2, "1.2.3.4", "10"))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DELETE FROM n WHERE id=2", 1))
			d = inetDiagnostic("inet", "bad", false)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.ErrorDiagnostic("SELECT CAST('bad'::text AS inet)", d))
			runtime := fixture.Prepared(fixture.ErrorDiagnostic("SELECT CAST('bad'::text AS inet)", d))
			runtime.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, runtime)
			conflict := fixture.Prepared(fixture.ImplicitError("SELECT $1 FROM n WHERE h=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "inet versus text", Position: "8"}))
			conflict.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, conflict)
		})
	}
	p.Step(t, fixture.Exec("CREATE TABLE required(h INET NOT NULL)", 0))
	p.Step(t, fixture.ImplicitError("INSERT INTO required VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "h" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "required", Column: "h"}))
	p.Step(t, fixture.Exec("CREATE TABLE empty_text(v TEXT)", 0))
	p.Step(t, fixture.Select("SELECT v::inet AS h FROM empty_text", []string{"h"}, []string{"INET"}, nil))
	d := inetDiagnostic("inet", "bad", false)
	d.Position = "8"
	eager := fixture.Prepared(fixture.ErrorDiagnostic("SELECT 'bad'::inet FROM empty_text", d))
	eager.ErrorStage = "prepare"
	p.Step(t, eager)
}
