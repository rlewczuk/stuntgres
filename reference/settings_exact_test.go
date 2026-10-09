package reference_test

import (
	"context"
	"math/big"
	"stuntgres"
	"testing"
)

func TestReferenceSettingsExactDecimalSubnormal(t *testing.T) {
	p := newReferenceScenario(t)
	if p.postgres != nil {
		c, err := p.postgres.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		// Independent integer identity: 5^1023 / 10^1023 = 2^-1023.
		digits := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil)
		exact := digits.String() + "e-1023"
		inexact := new(big.Int).Add(digits, big.NewInt(1)).String() + "e-1023"
		for _, prepared := range []bool{false, true} {
			run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
			run(referenceExec("SET random_page_cost TO '"+exact+"'", 0))
			run(referenceSelect("SHOW random_page_cost", []string{"random_page_cost"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}))
			run(referenceSelect("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}, "random_page_cost", exact))
			for _, input := range []string{inexact, "1e-320", "1e-999"} {
				run(settingsReferenceError("SET random_page_cost TO '"+input+"'", "22023", `invalid value for parameter "random_page_cost": "`+input+`"`, "", ""))
				run(referenceSelect("SHOW random_page_cost", []string{"random_page_cost"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}))
			}
			run(referenceExec("RESET random_page_cost", 0))
		}
	}
	// 5^1023 * 10^-1023 is exactly 2^-1023, independently of float formatting.
	digits := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil)
	exact := digits.String() + "e-1023"
	inexact := new(big.Int).Add(digits, big.NewInt(1)).String() + "e-1023"
	d := settingsTestDriver(t)
	if err := d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.exact", Type: stuntgres.SettingFloating, Default: exact, FloatMax: 10}); err != nil {
		t.Fatal(err)
	}
	db := sessionPool(t, d, "exact-decimal")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		settingsRows(t, c, "SHOW demo.exact", prepared, "demo.exact", "1.11254e-308")
		for _, name := range []string{"demo.ratio", "demo.exact"} {
			for _, input := range []string{exact, "+" + exact, " " + exact + " "} {
				sessionSet(t, c, "SET "+name+" TO '"+input+"'", prepared)
				settingsRows(t, c, "SHOW "+name, prepared, name, "1.11254e-308")
				settingsRows(t, c, "SELECT set_config($1,$2,false)", prepared, "set_config", "1.11254e-308", name, input)
			}
			for _, input := range []string{inexact, "1e-320", "1e-999"} {
				for _, q := range []string{"SET " + name + " TO '" + input + "'", "SELECT set_config('" + name + "','" + input + "',false)"} {
					if prepared {
						s, e := c.PrepareContext(ctx, q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.ExecContext(ctx)
						s.Close()
					} else {
						_, err = c.ExecContext(ctx, q)
					}
					settingsDiagnostic(t, err, "22023", `invalid value for parameter "`+name+`": "`+input+`"`, "", "", "")
					settingsRows(t, c, "SHOW "+name, prepared, name, "1.11254e-308")
				}
			}
		}
		sessionSet(t, c, "SET demo.exact TO '1'", prepared)
		sessionSet(t, c, "RESET demo.exact", prepared)
		settingsRows(t, c, "SHOW demo.exact", prepared, "demo.exact", "1.11254e-308")
		sessionSet(t, c, "SET demo.exact TO '2'", prepared)
		for _, q := range []string{"SET demo.exact TO DEFAULT", "RESET demo.exact", "RESET ALL"} {
			sessionSet(t, c, q, prepared)
			settingsRows(t, c, "SHOW demo.exact", prepared, "demo.exact", "1.11254e-308")
		}
		settingsRows(t, c, "SELECT set_config('demo.exact',NULL,false)", prepared, "set_config", "1.11254e-308")
	}
}
