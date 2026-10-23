package proc

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func cursorLedgerDefinition(t *testing.T, source string) (*Definition, PLTypeResolver) {
	t.Helper()
	types := make(map[string]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.Name()] = typ
	}
	integer := plUnitInteger(t)
	resolve := func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "record" {
			return sqltype.Usage{}, true, nil
		}
		name := raw.Type.Name
		if name == "integer" {
			name = "int4"
		}
		if name == "boolean" {
			name = "bool"
		}
		if typ := types[name]; typ != nil {
			return typ.Usage(), false, nil
		}
		return sqltype.Usage{}, false, fmt.Errorf("unexpected type %s", name)
	}
	d := &Definition{Kind: PLpgSQLBody, Name: "cursor_ledger_unit", Source: source, Result: integer.Usage()}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax = raw
	d.PL, err = CompilePL(d, raw, false, plUnitCollation(t), resolve)
	if err != nil {
		t.Fatal(err)
	}
	return d, resolve
}

func cursorLedgerCase(p *PLProgram, loop bool) *PLConditional {
	body := p.Root.Statements[0].Block
	if loop {
		return body.Statements[0].Cursor.Loop.Statements[0].Block.Statements[0].Case
	}
	return body.Statements[len(body.Statements)-1].Case
}

func TestPLpgSQLCursorDiagnosticLedger(t *testing.T) {
	for _, tc := range []struct {
		name, declarations, statements, body string
		variables, datum, forged             int
		loop                                 bool
	}{
		{name: "bound_none", declarations: "c CURSOR FOR SELECT 7;", variables: 2, datum: 2, forged: 3},
		{name: "bound_one", declarations: "c CURSOR(a integer) FOR SELECT a;", variables: 3, datum: 4, forged: 3},
		{name: "bound_two", declarations: "c CURSOR(a integer,b integer) FOR SELECT a+b;", variables: 4, datum: 5, forged: 4},
		{name: "fetch_scalar", declarations: "c refcursor; x integer;", statements: "FETCH c INTO x;", variables: 3, datum: 4, forged: 3},
		{name: "fetch_record", declarations: "c refcursor; r record;", statements: "FETCH c INTO r;", variables: 3, datum: 3, forged: 4},
		{name: "fetch_field", declarations: "c refcursor; r record;", statements: "FETCH c INTO r.f1;", variables: 3, datum: 5, forged: 4},
		{name: "fetch_field_twice", declarations: "c refcursor; r record;", statements: "FETCH c INTO r.f1; FETCH c INTO r.f1;", variables: 3, datum: 6, forged: 5},
		{name: "query_duplicate", declarations: "c refcursor; r record;", statements: "OPEN c FOR SELECT r.f1+r.f1;", variables: 3, datum: 4, forged: 5},
		{name: "bound_query", declarations: "r record; c CURSOR(a integer) FOR SELECT r.f1+a;", variables: 4, datum: 6, forged: 5},
		{name: "count_then_target", declarations: "c refcursor; r record;", statements: "FETCH ABSOLUTE r.f1 FROM c INTO r.f2;", variables: 3, datum: 6, forged: 5},
		{name: "named_actuals", declarations: "r record; c CURSOR(a integer,b integer) FOR SELECT a+b;", statements: "OPEN c(b=>r.f2,a=>r.f1);", variables: 5, datum: 8, forged: 7},
		{name: "for_outer_and_loop_parents", body: "DECLARE r record; c CURSOR(a integer) FOR SELECT a AS f1; BEGIN FOR r IN c(r.f1) LOOP CASE 2 WHEN true THEN RETURN r.f1; ELSE RETURN 2; END CASE; END LOOP; RETURN 0; END", variables: 5, datum: 8, forged: 7, loop: true},
		{name: "width_change", declarations: "c refcursor; r record;", statements: "FETCH c INTO r.f1,r.f2,r.f3,r.f4,r.f5,r.f6;", variables: 3, datum: 10, forged: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.body
			if source == "" {
				source = "DECLARE " + tc.declarations + " BEGIN " + tc.statements + " CASE 2 WHEN true THEN RETURN 1; ELSE RETURN 2; END CASE; END"
			}
			d, resolve := cursorLedgerDefinition(t, source)
			if err := d.PL.ValidateDefinition(d); err != nil {
				t.Fatal(err)
			}
			conditional := cursorLedgerCase(d.PL, tc.loop)
			want := fmt.Sprintf("__Case__Variable_%d__", tc.datum)
			if conditional.SearchName != want || len(d.PL.Variables) != tc.variables || d.PL.SearchCount != 1 || conditional.SearchSlot != 0 {
				t.Fatal("PG-only ledger changed executable slots or got wrong display identity", conditional.SearchName)
			}
			variables := slices.Clone(d.PL.Variables)
			// Recompile checked payload against the SAME immutable syntax and
			// canonical resolver, not a copied expected-name oracle.
			candidate := *d
			var err error
			candidate.PL, err = CompilePL(&candidate, d.PLSyntax, false, plUnitCollation(t), resolve)
			if err != nil {
				t.Fatal(err)
			}
			forged := cursorLedgerCase(candidate.PL, tc.loop)
			old := forged.SearchName
			forged.SearchName = fmt.Sprintf("__Case__Variable_%d__", tc.forged)
			delta := len(forged.SearchName) - len(old)
			for i := range forged.Arms {
				branch := &forged.Arms[i]
				branch.DiagnosticQuery = strings.Replace(branch.DiagnosticQuery, old, forged.SearchName, 1)
				branch.DiagnosticItems += delta
				branch.DiagnosticOperator += delta
			}
			if err := candidate.PL.ValidateDefinition(&candidate); err == nil {
				t.Fatal("coherently forged display metadata bypassed reconstructed ledger")
			}
			if candidate.PL.SearchCount != 1 || forged.SearchSlot != 0 || !reflect.DeepEqual(candidate.PL.Variables, variables) {
				t.Fatal("forgery altered executable state instead of only diagnostic metadata")
			}
			if d.PL.SameLayout(candidate.PL) || conditional.SearchName != want || !reflect.DeepEqual(d.PL.Variables, variables) {
				t.Fatal("forged private payload mutated published source")
			}
			if err := d.PL.ValidateDefinition(d); err != nil {
				t.Fatal("candidate corrupted original immutable program", err)
			}
		})
	}
}
