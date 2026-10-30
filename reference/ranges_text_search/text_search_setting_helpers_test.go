package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The inventory's values are independently specified by the setting contract,
// not obtained by comparing the two backends.
type textSearchSettingForm struct {
	name, typ string
	json *ftsJSONForm
	debug, match bool
}

func textSearchSettingForms() []textSearchSettingForm {
	var forms []textSearchSettingForm
	for _, name := range ftsConvertNames {
		typ := "TSQUERY"
		if name == "to_tsvector" { typ = "TSVECTOR" }
		forms = append(forms, textSearchSettingForm{name: name, typ: typ})
	}
	for _, f := range ftsJSONForms() {
		if f.explicit { continue }
		form := f
		forms = append(forms, textSearchSettingForm{name: f.name+"_"+f.typ, typ: "TSVECTOR", json: &form})
	}
	forms = append(forms, textSearchSettingForm{name: "ts_debug", debug: true})
	return forms
}

func textSearchSettingMatches() []textSearchSettingForm {
	var forms []textSearchSettingForm
	for _, name := range []string{"ts_match_tt", "ts_match_tq", "operator_tt", "operator_tq"} {
		forms = append(forms, textSearchSettingForm{name: name, typ: "BOOL", match: true})
	}
	return forms
}

func (f textSearchSettingForm) query(parameter, explicit bool, config, document, filter any) (string, []any) {
	if f.json != nil {
		form := *f.json
		form.explicit = explicit
		return form.query(!parameter, config, document, filter)
	}
	var operands []string
	var args []any
	add := func(v any, typ string) {
		s := "NULL"
		if parameter {
			args = append(args, v)
			s = fmt.Sprintf("$%d", len(args))
		} else if v != nil { s = "'"+strings.ReplaceAll(fmt.Sprint(v), "'", "''")+"'" }
		operands = append(operands, s+"::"+typ)
	}
	if explicit { add(config, "regconfig") }
	add(document, "text")
	if f.match {
		typ := "text"
		if strings.HasSuffix(f.name, "tq") { typ = "tsquery" }
		add(filter, typ)
		if strings.HasPrefix(f.name, "operator") { return "SELECT "+operands[0]+" @@ "+operands[1]+" AS normalized", args }
	}
	if f.debug { return "SELECT * FROM ts_debug("+strings.Join(operands, ",")+")", args }
	return "SELECT "+f.name+"("+strings.Join(operands, ",")+") AS normalized", args
}

func (f textSearchSettingForm) input() any {
	if f.json != nil { return `"The Rats"` }
	if f.debug || f.match { return "Rats" }
	return ftsConvertInput(f.name)
}

func (f textSearchSettingForm) step(q string, english, null bool, args ...any) fixture.Step {
	if f.debug {
		var rows [][]any
		if !null {
			dict, lexeme := "simple", "rats"
			if english { dict, lexeme = "english_stem", "rat" }
			rows = [][]any{{"asciiword", "Word, all ASCII", "Rats", "{"+dict+"}", dict, "{"+lexeme+"}"}}
		}
		return fixture.Select(q, ftsDebugColumns, ftsDebugTypes, rows, args...)
	}
	var want any
	if !null {
		switch {
		case f.match: want = english
		case f.json != nil: want = ftsConvertExpected("to_tsvector", english)
		default: want = ftsConvertExpected(f.name, english)
		}
	}
	return fixture.Select(q, []string{"normalized"}, []string{f.typ}, [][]any{{want}}, args...)
}

func textSearchSettingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			defer ftsConvertRestore(t, p, c)()
			body(t, p, b, c)
		})
	}
}

func textSearchSettingHold(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func textSearchSettingRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	for i, x := range []fixture.Executor{c, c, held} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			step := s
			if len(step.Types) == 6 {
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ftsInspectionMetadata(t, b, rows, false) }
			} else if len(step.Types) == 1 && step.Types[0] != "BOOL" {
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ftsConvertMetadata(t, rows, b.PG, step.Columns[0], step.Types[0]) }
			} else if len(step.Types) == 1 {
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { fixture.DriverBasicMetadata(t, rows, b.PG, step.Types, []bool{false}, 0) }
			}
			if i == 1 { step = fixture.Prepared(step) }
			fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
		})
	}
}

func textSearchSettingShow(value string) fixture.Step {
	return fixture.Select("SHOW default_text_search_config", []string{"default_text_search_config"}, []string{"TEXT"}, [][]any{{value}})
}

func textSearchSettingMissingDiagnostic(schema bool, debug bool) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("42704", `text search configuration "ts_setting_missing.cfg" does not exist`)
	if schema { d = fixture.ControlsReferenceDiagnostic("3F000", `schema "ts_setting_missing" does not exist`) }
	if debug { d.Context = `SQL function "ts_debug" statement 1` }
	return d
}
