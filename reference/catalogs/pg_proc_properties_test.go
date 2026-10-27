package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGProcProperties(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pgProcBoth(t, p, b, b.DB, "SELECT oid,prosupport::oid,proleakproof,prosrc,probin,protrftypes FROM pg_proc WHERE oid IN (65,1396,1397,2331,3696) ORDER BY oid", []string{"oid", "prosupport", "proleakproof", "prosrc", "probin", "protrftypes"}, []string{"OID", "OID", "BOOL", "TEXT", "TEXT", "_OID"}, [][]any{
				{int64(65), int64(0), true, "int4eq", nil, nil},
				{int64(1396), int64(0), false, "int8abs", nil, nil},
				{int64(1397), int64(0), false, "int4abs", nil, nil},
				{int64(2331), int64(3996), false, "array_unnest", nil, nil},
				{int64(3696), int64(6242), true, "text_starts_with", nil, nil},
			})
			pgProcExec(t, p, b, b.DB,
				"CREATE FUNCTION pp_prop_quote(a integer DEFAULT 7,b text DEFAULT 'x') RETURNS integer LANGUAGE SQL AS 'SELECT a'",
				"CREATE FUNCTION pp_prop_standard(a integer DEFAULT 7,b text DEFAULT 'x') RETURNS integer LANGUAGE SQL RETURN a",
				"CREATE FUNCTION pp_prop_null(a integer DEFAULT NULL) RETURNS integer LANGUAGE SQL RETURN a",
				"CREATE FUNCTION pp_prop_pl() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",
				"CREATE FUNCTION pp_prop_set(integer) RETURNS SETOF integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION pp_prop_explicit(integer) RETURNS SETOF integer LANGUAGE SQL STABLE STRICT SECURITY DEFINER PARALLEL RESTRICTED COST 7 ROWS 12 SET work_mem='64kB' SET search_path=public,pg_catalog AS 'SELECT $1'")
			pgProcBoth(t, p, b, b.DB, "SELECT p.proname,l.lanname,p.prosrc,p.prosqlbody IS NOT NULL AS body,p.proargdefaults IS NOT NULL AS defaults,p.probin,p.protrftypes FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.proname IN ('pp_prop_quote','pp_prop_standard','pp_prop_pl') ORDER BY p.proname", []string{"proname", "lanname", "prosrc", "body", "defaults", "probin", "protrftypes"}, []string{"NAME", "NAME", "TEXT", "BOOL", "BOOL", "TEXT", "_OID"}, [][]any{{"pp_prop_pl", "plpgsql", "BEGIN RETURN 7; END", false, false, nil, nil}, {"pp_prop_quote", "sql", "SELECT a", false, true, nil, nil}, {"pp_prop_standard", "sql", "", true, true, nil, nil}})
			pgProcBoth(t, p, b, b.DB, "SELECT proname,provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE proname IN ('pp_prop_explicit','pp_prop_quote','pp_prop_set') ORDER BY proname", []string{"proname", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{"pp_prop_explicit", "s", true, true, "r", float64(7), float64(12), "{work_mem=64kB,\"search_path=public, pg_catalog\"}"}, {"pp_prop_quote", "v", false, false, "u", float64(100), float64(0), nil}, {"pp_prop_set", "v", false, false, "u", float64(100), float64(1000), nil}})
			pgProcBoth(t, p, b, b.DB, "SELECT pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE proname='pp_prop_standard'", []string{"defaults"}, []string{"TEXT"}, [][]any{{"7, 'x'::text"}})
			pgProcBoth(t, p, b, b.DB, "SELECT pronargdefaults,proargdefaults IS NOT NULL AS present,pg_get_expr(proargdefaults,0) AS defaults FROM pg_proc WHERE proname='pp_prop_null'", []string{"pronargdefaults", "present", "defaults"}, []string{"INT2", "BOOL", "TEXT"}, [][]any{{int64(1), true, "NULL::integer"}})
			pgProcBoth(t, p, b, b.DB, "SELECT pp_prop_standard() AS standard,pp_prop_quote() AS quoted,pp_prop_pl() AS pl", []string{"standard", "quoted", "pl"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(7)}})
			pgProcBoth(t, p, b, b.DB, "SELECT l.lanname,p.prosrc,p.probin FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=(SELECT lanplcallfoid FROM pg_language WHERE lanname='plpgsql')", []string{"lanname", "prosrc", "probin"}, []string{"NAME", "TEXT", "TEXT"}, [][]any{{"c", "plpgsql_call_handler", "$libdir/plpgsql"}})
			pgProcLibraries(t, p, b)
			var releasedBody any
			if err := b.DB.QueryRowContext(p.Ctx, "SELECT prosqlbody FROM pg_proc WHERE oid=5023").Scan(&releasedBody); err != nil {
				t.Fatal(err)
			}
			if b.PG {
				raw, ok := releasedBody.([]byte)
				if !ok || !strings.Contains(string(raw), "{QUERY") || !strings.Contains(string(raw), "{PARAM") || !strings.Contains(string(raw), "{OPEXPR") {
					t.Fatalf("released builtin body %T %q", releasedBody, releasedBody)
				}
			} else {
				raw, ok := releasedBody.(string)
				if !ok || !strings.HasPrefix(raw, "{RoutineDeclarationBody ") || !strings.Contains(raw, "parser.OperatorExpr") || !strings.Contains(raw, "parser.ParameterExpr") {
					t.Fatalf("native builtin declaration body %T %q", releasedBody, releasedBody)
				}
			}
			for _, prepared := range []bool{false, true} {
				const q = "SELECT proargdefaults,prosqlbody FROM pg_proc WHERE proname='pp_prop_standard'"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := b.DB.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = b.DB.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				pgProcTrees(t, b, rows)
			}
		})
	}
}

// Raw structural carriers deliberately differ. Inspect each actual raw value
// and semantic content independently; never equate or erase the tree payload.
func pgProcTrees(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].DatabaseTypeName() != "PG_NODE_TREE" || types[1].DatabaseTypeName() != "PG_NODE_TREE" {
		t.Fatal("tree descriptor", types)
	}
	if !rows.Next() {
		t.Fatal("missing genuine tree row", rows.Err())
	}
	var defaults, body any
	if err := rows.Scan(&defaults, &body); err != nil {
		t.Fatal(err)
	}
	text := func(v any) string {
		t.Helper()
		if b.PG {
			bytes, ok := v.([]byte)
			if !ok {
				t.Fatalf("PG raw tree %T", v)
			}
			return string(bytes)
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("native raw tree %T", v)
		}
		return s
	}
	d, program := text(defaults), text(body)
	if b.PG {
		if !strings.HasPrefix(d, "({CONST") || !strings.Contains(d, ":consttype 23") || !strings.Contains(d, ":consttype 25") || !strings.Contains(program, "{QUERY") || !strings.Contains(program, "{PARAM") {
			t.Fatalf("released trees %q / %q", d, program)
		}
	} else {
		// Checked action syntax resolves the named input a to formal slot 1.
		// Require that actual SELECT target, not just a nonempty program wrapper.
		if !strings.HasPrefix(d, "{ExpressionList ") || !strings.Contains(d, `Const value "7"`) || !strings.Contains(d, `Const value "x"`) || strings.Index(d, `Const value "7"`) >= strings.Index(d, `Const value "x"`) || !strings.Contains(d, "type 23") || !strings.Contains(d, "type 25") || !strings.HasPrefix(program, "{RoutineProgram {parser.Select") || !strings.Contains(program, "Targets [{parser.ParameterExpr Number 1}]") {
			t.Fatalf("native structural trees %q / %q", d, program)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("tree row multiplicity", rows.Err())
	}
}
