package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// macOperationsScenario keeps the five public scenarios on the existing fixture.
func macOperationsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func macOperationsValue(t *testing.T, p *fixture.Pair, b fixture.Backend, expression, typ string, want any, args ...any) {
	t.Helper()
	s := macaddr8Select(b.PG, "SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{want}}, args...)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func TestReferenceMACOperationsValues(t *testing.T) {
	macOperationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ expression, typ, want string }{
			{"~macaddr '12:34:56:78:90:ab'", "MACADDR", "ed:cb:a9:87:6f:54"},
			{"macaddr '12:34:56:78:90:ab' & macaddr '0f:f0:aa:55:ff:00'", "MACADDR", "02:30:02:50:90:00"},
			{"macaddr '12:34:56:78:90:ab' | macaddr '0f:f0:aa:55:ff:00'", "MACADDR", "1f:f4:fe:7d:ff:ab"},
			{"~macaddr8 '12:34:56:78:90:ab:cd:ef'", "MACADDR8", "ed:cb:a9:87:6f:54:32:10"},
			{"macaddr8 '12:34:56:78:90:ab:cd:ef' & macaddr8 '0f:f0:aa:55:ff:00:33:cc'", "MACADDR8", "02:30:02:50:90:00:01:cc"},
			{"macaddr8 '12:34:56:78:90:ab:cd:ef' | macaddr8 '0f:f0:aa:55:ff:00:33:cc'", "MACADDR8", "1f:f4:fe:7d:ff:ab:ff:ef"},
			{"trunc(macaddr '12:34:56:78:90:ab')", "MACADDR", "12:34:56:00:00:00"},
			{"trunc(macaddr8 '12:34:56:78:90:ab:cd:ef')", "MACADDR8", "12:34:56:00:00:00:00:00"},
			{"trunc(macaddr8 '12:34:56:78:90:ab')", "MACADDR8", "12:34:56:00:00:00:00:00"},
			{"macaddr8_set7bit('00:34:56:ab:cd:ef')", "MACADDR8", "02:34:56:ff:fe:ab:cd:ef"},
			{"macaddr8_set7bit(macaddr8 '00:34:56:ab:cd:ef')", "MACADDR8", "02:34:56:ff:fe:ab:cd:ef"},
			{"macaddr8_set7bit(macaddr '00:34:56:ab:cd:ef')", "MACADDR8", "02:34:56:ff:fe:ab:cd:ef"},
			{"macaddr8_set7bit(macaddr8 '80:34:56:78:90:ab:cd:ef')", "MACADDR8", "82:34:56:78:90:ab:cd:ef"},
			{"macaddr8_set7bit(macaddr8 '02:34:56:78:90:ab:cd:ef')", "MACADDR8", "02:34:56:78:90:ab:cd:ef"},
			{"macaddr8_set7bit(macaddr8 '01:34:56:78:90:ab:cd:ef')", "MACADDR8", "03:34:56:78:90:ab:cd:ef"},
			{"~macaddr8 '00:00:00:00:00:00'", "MACADDR8", "ff:ff:ff:00:01:ff:ff:ff"},
			{"macaddr8 '80:00:00:00:00:00:00:00' | macaddr8 '00:00:00:00:00:00:00:01'", "MACADDR8", "80:00:00:00:00:00:00:01"},
			{"macaddr8 '80:00:00:00:00:00:00:01' & macaddr8 'ff:00:00:00:00:00:00:00'", "MACADDR8", "80:00:00:00:00:00:00:00"},
			{"macaddr '12:34:56:78:90:ab' & '0f:f0:aa:55:ff:00'", "MACADDR", "02:30:02:50:90:00"},
			{"'00:00:00:00:00:00' | macaddr8 '80:00:00:00:00:00:00:01'", "MACADDR8", "80:00:00:ff:fe:00:00:01"},
			{"pg_catalog.trunc(macaddr '12:34:56:78:90:ab')", "MACADDR", "12:34:56:00:00:00"},
			{"pg_catalog.macaddr8_set7bit(macaddr '00:34:56:ab:cd:ef')", "MACADDR8", "02:34:56:ff:fe:ab:cd:ef"},
			{"macaddr '12:34:56:78:90:ab' OPERATOR(pg_catalog.&) macaddr '0f:f0:aa:55:ff:00'", "MACADDR", "02:30:02:50:90:00"},
		} {
			t.Run(tc.expression, func(t *testing.T) { macOperationsValue(t, p, b, tc.expression, tc.typ, tc.want) })
		}
		for _, width := range []struct{ name, typ, zero, full, value, prefix string }{
			{"macaddr", "MACADDR", "00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "12:34:56:78:90:ab", "12:34:56:00:00:00"},
			{"macaddr8", "MACADDR8", "00:00:00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff:ff:ff", "12:34:56:78:90:ab:cd:ef", "12:34:56:00:00:00:00:00"},
		} {
			t.Run(width.name, func(t *testing.T) {
				lit := func(s string) string { return width.name + " '" + s + "'" }
				v, z, f := lit(width.value), lit(width.zero), lit(width.full)
				for _, tc := range []struct{ expression, want string }{
					{"~" + z, width.full}, {"~" + f, width.zero},
					{v + " & " + z, width.zero}, {v + " & " + v, width.value}, {v + " & " + f, width.value},
					{v + " | " + z, width.value}, {v + " | " + v, width.value}, {v + " | " + f, width.full},
					{"trunc(trunc(" + v + "))", width.prefix},
				} {
					macOperationsValue(t, p, b, tc.expression, width.typ, tc.want)
				}
				n := "NULL::" + width.name
				for _, e := range []string{"~" + n, "trunc(" + n + ")", n + " & " + v, v + " & " + n, n + " | " + v, v + " | " + n} {
					macOperationsValue(t, p, b, e, width.typ, nil)
				}
				macOperationsValue(t, p, b, "macaddr8_set7bit("+n+")", "MACADDR8", nil)
			})
		}
		macOperationsValue(t, p, b, "macaddr8_set7bit(macaddr8_set7bit(macaddr8 '80:34:56:78:90:ab:cd:ef'))", "MACADDR8", "82:34:56:78:90:ab:cd:ef")
		// Keep the existing numeric owner honest, without broadening its admission.
		macOperationsValue(t, p, b, "trunc(4)", "FLOAT8", float64(4))
		macOperationsValue(t, p, b, "trunc(4.9)", "NUMERIC", "4")
		macOperationsValue(t, p, b, "trunc(4.987,2)", "NUMERIC", "4.98")
	})
}
