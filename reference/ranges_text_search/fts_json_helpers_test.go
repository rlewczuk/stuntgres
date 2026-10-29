package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type ftsJSONForm struct {
	name, typ          string
	explicit, filtered bool
}

func ftsJSONForms() []ftsJSONForm {
	var forms []ftsJSONForm
	for _, typ := range []string{"json", "jsonb"} {
		for _, filtered := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				name := "to_tsvector"
				if filtered {
					name = typ + "_to_tsvector"
				}
				forms = append(forms, ftsJSONForm{name, typ, explicit, filtered})
			}
		}
	}
	return forms
}

func (f ftsJSONForm) query(literal bool, config, document, filter any) (string, []any) {
	var operands []string
	var args []any
	add := func(value any, typ string) {
		operand := "NULL"
		if literal {
			if value != nil {
				operand = "'" + strings.ReplaceAll(fmt.Sprint(value), "'", "''") + "'"
			}
		} else {
			args = append(args, value)
			operand = fmt.Sprintf("$%d", len(args))
		}
		operands = append(operands, operand+"::"+typ)
	}
	if f.explicit {
		add(config, "regconfig")
	}
	add(document, f.typ)
	if f.filtered {
		add(filter, "jsonb")
	}
	return "SELECT " + f.name + "(" + strings.Join(operands, ",") + ") AS normalized", args
}

func ftsJSONValue(query string, want any, args ...any) fixture.Step {
	return fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{want}}, args...)
}

type ftsJSONCase struct {
	config, document, filter any
	want                     any
}

// Every family shares identical literal, fresh typed prepared and repeatedly held
// typed parameter routes. Goldens are supplied by each test, never another backend.
func ftsJSONCases(t *testing.T, forms []ftsJSONForm, cases []ftsJSONCase) {
	t.Helper()
	for _, form := range forms {
		t.Run(fmt.Sprintf("%s_%s_explicit_%t", form.name, form.typ, form.explicit), func(t *testing.T) {
			ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer ftsConvertSetting(t, p, c)()
				query, _ := form.query(false, "english", `"cat"`, `"string"`)
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for _, tc := range cases {
					literal, _ := form.query(true, tc.config, tc.document, tc.filter)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, ftsJSONValue(literal, tc.want))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(ftsJSONValue(literal, tc.want)))
					_, args := form.query(false, tc.config, tc.document, tc.filter)
					s := ftsJSONValue(query, tc.want, args...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			})
		})
	}
}
