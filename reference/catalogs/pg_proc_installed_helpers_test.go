package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const pgProcCitextSchema = `"PP Citext"`
const pgProcCubeSchema = `"PP Cube"`
const pgProcEarthSchema = `"PP Earth"`

func pgProcIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

type pgProcInstalledBody struct {
	schema, name, args, result string
	calls                      []string // independently released callees, resolved via regprocedure
	caseExpr, subscript        bool
}

// REL_18_6 contrib/citext/citext--1.7--1.8.sql and
// contrib/earthdistance/earthdistance--1.1--1.2.sql. These lists enumerate
// supported declaration recipes; membership is never read from native metadata.
func pgProcInstalledBodies() []pgProcInstalledBody {
	c, e, cube := pgProcCitextSchema, pgProcEarthSchema, pgProcCubeSchema
	ct := c + ".citext"
	var out []pgProcInstalledBody
	for _, name := range []string{"regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table"} {
		result := "text[]"
		if name == "regexp_replace" || name == "regexp_split_to_table" {
			result = "text"
		}
		args := ct + "," + ct
		callee := "pg_catalog." + name + "(text,text,text)"
		if name == "regexp_replace" {
			args += ",text"
			callee = "pg_catalog.regexp_replace(text,text,text,text)"
		}
		out = append(out, pgProcInstalledBody{schema: c, name: name, args: args, result: result, calls: []string{callee}})
		out = append(out, pgProcInstalledBody{schema: c, name: name, args: args + ",text", result: result, calls: []string{callee, "pg_catalog.strpos(text,text)"}, caseExpr: true})
	}
	out = append(out,
		pgProcInstalledBody{schema: c, name: "strpos", args: ct + "," + ct, result: "integer", calls: []string{"pg_catalog.strpos(text,text)", "pg_catalog.lower(text)"}},
		pgProcInstalledBody{schema: c, name: "replace", args: ct + "," + ct + "," + ct, result: "text", calls: []string{"pg_catalog.regexp_replace(text,text,text,text)"}},
		pgProcInstalledBody{schema: c, name: "split_part", args: ct + "," + ct + ",integer", result: "text", calls: []string{"pg_catalog.regexp_split_to_array(text,text,text)", "pg_catalog.regexp_replace(text,text,text,text)"}, subscript: true},
		pgProcInstalledBody{schema: c, name: "translate", args: ct + "," + ct + ",text", result: "text", calls: []string{"pg_catalog.translate(text,text,text)", "pg_catalog.lower(text)", "pg_catalog.upper(text)"}},
		pgProcInstalledBody{schema: e, name: "earth", result: "double precision"},
		pgProcInstalledBody{schema: e, name: "sec_to_gc", args: "double precision", result: "double precision", calls: []string{e + ".earth()", "pg_catalog.asin(double precision)", "pg_catalog.pi()"}, caseExpr: true},
		pgProcInstalledBody{schema: e, name: "gc_to_sec", args: "double precision", result: "double precision", calls: []string{e + ".earth()", "pg_catalog.sin(double precision)", "pg_catalog.pi()"}, caseExpr: true},
		pgProcInstalledBody{schema: e, name: "ll_to_earth", args: "double precision,double precision", result: e + ".earth", calls: []string{cube + ".cube(double precision)", cube + ".cube(" + cube + ".cube,double precision)", e + ".earth()", "pg_catalog.radians(double precision)", "pg_catalog.cos(double precision)", "pg_catalog.sin(double precision)"}},
		pgProcInstalledBody{schema: e, name: "latitude", args: e + ".earth", result: "double precision", calls: []string{cube + ".cube_ll_coord(" + cube + ".cube,integer)", e + ".earth()", "pg_catalog.degrees(double precision)", "pg_catalog.asin(double precision)"}, caseExpr: true},
		pgProcInstalledBody{schema: e, name: "longitude", args: e + ".earth", result: "double precision", calls: []string{cube + ".cube_ll_coord(" + cube + ".cube,integer)", "pg_catalog.degrees(double precision)", "pg_catalog.atan2(double precision,double precision)"}},
	)
	return out
}

func pgProcInstalledSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	pgProcExec(t, p, b, x, "CREATE SCHEMA "+pgProcCitextSchema, "CREATE SCHEMA "+pgProcCubeSchema, "CREATE SCHEMA "+pgProcEarthSchema,
		"CREATE EXTENSION citext SCHEMA "+pgProcCitextSchema,
		"CREATE EXTENSION cube SCHEMA "+pgProcCubeSchema,
		"CREATE EXTENSION earthdistance SCHEMA "+pgProcEarthSchema)
}

func pgProcInstalledCleanup(t *testing.T, x fixture.Executor) {
	t.Helper()
	for _, q := range []string{"RESET search_path", "DROP EXTENSION IF EXISTS earthdistance CASCADE", "DROP EXTENSION IF EXISTS cube CASCADE", "DROP EXTENSION IF EXISTS citext CASCADE", "DROP SCHEMA IF EXISTS " + pgProcCitextSchema + " CASCADE", "DROP SCHEMA IF EXISTS " + pgProcCubeSchema + " CASCADE", "DROP SCHEMA IF EXISTS " + pgProcEarthSchema + " CASCADE", "DROP SCHEMA IF EXISTS pp_inst_moved CASCADE"} {
		if _, err := x.ExecContext(context.Background(), q); err != nil {
			t.Error(q, err)
		}
	}
}

func pgProcInstalledCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, facts []pgProcInstalledBody) {
	t.Helper()
	for _, f := range facts {
		signature := f.schema + "." + pgProcIdent(f.name) + "(" + f.args + ")"
		var vector []string
		if f.args != "" {
			for _, arg := range strings.Split(f.args, ",") {
				oid := pgProcInstalledOID(t, p, b, x, "SELECT $1::regtype::oid", arg)
				vector = append(vector, strconv.FormatInt(oid, 10))
			}
		}
		pgProcBoth(t, p, b, x, "SELECT p.proname,p.proargtypes,p.pronargs,p.prorettype=$2::regtype::oid AS result,l.lanname,p.prosrc,p.probin,p.prosqlbody IS NOT NULL AS body,p.prokind FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::regprocedure::oid", []string{"proname", "proargtypes", "pronargs", "result", "lanname", "prosrc", "probin", "body", "prokind"}, []string{"NAME", "OIDVECTOR", "INT2", "BOOL", "NAME", "TEXT", "TEXT", "BOOL", "CHAR"}, [][]any{{f.name, strings.Join(vector, " "), int64(len(vector)), true, "sql", "", nil, true, "f"}}, signature, f.result)
		for _, prepared := range []bool{false, true} {
			const q = "SELECT prosqlbody FROM pg_proc WHERE oid=$1::regprocedure::oid"
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := x.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.Ctx, signature)
			} else {
				rows, err = x.QueryContext(p.Ctx, q, signature)
			}
			if err != nil {
				t.Fatal(signature, err)
			}
			pgProcInstalledTree(t, p, b, x, rows, f)
		}
	}
}

func pgProcInstalledTree(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, rows *sql.Rows, f pgProcInstalledBody) {
	t.Helper()
	// Drain before resolving independent callee identities on this connection.
	meta, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].DatabaseTypeName() != "PG_NODE_TREE" {
		rows.Close()
		t.Fatal(meta)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing body", f.name, rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	var text string
	if b.PG {
		v, ok := raw.([]byte)
		if !ok {
			rows.Close()
			t.Fatalf("PG raw tree %T", raw)
		}
		text = string(v)
	} else {
		v, ok := raw.(string)
		if !ok {
			rows.Close()
			t.Fatalf("native raw tree %T", raw)
		}
		text = v
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("body multiplicity", f.name, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if b.PG {
		if !strings.Contains(text, "{QUERY") {
			t.Fatal(f.name, text)
		}
		for _, call := range f.calls {
			oid := pgProcInstalledOID(t, p, b, x, "SELECT $1::regprocedure::oid", call)
			if !strings.Contains(text, ":funcid "+strconv.FormatInt(oid, 10)+" ") {
				t.Fatal("missing released callee", call, text)
			}
		}
		if f.caseExpr && !strings.Contains(text, "{CASEEXPR") {
			t.Fatal("missing CASE", f.name, text)
		}
		if f.args != "" {
			for n := 1; n <= strings.Count(f.args, ",")+1; n++ {
				if !strings.Contains(text, ":paramid "+strconv.Itoa(n)+" ") {
					t.Fatal("missing released parameter", n, f.name, text)
				}
			}
		}
		if f.subscript && !strings.Contains(text, "{SUBSCRIPTINGREF") {
			t.Fatal("missing subscript", text)
		}
		if f.name == "earth" && !strings.Contains(text, ":consttype 701") {
			t.Fatal("missing float8 radius", text)
		}
	} else {
		if !strings.HasPrefix(text, "{RoutineDeclarationBody {parser.Select") {
			t.Fatal(f.name, text)
		}
		for _, call := range f.calls {
			name := call[:strings.IndexByte(call, '(')]
			name = name[strings.LastIndexByte(name, '.')+1:]
			if !strings.Contains(text, `Text "`+name+`"`) {
				t.Fatal("missing released syntax callee", call, text)
			}
		}
		if f.name == "ll_to_earth" || f.name == "latitude" || f.name == "longitude" {
			if !strings.Contains(text, `Text "PP Cube"`) {
				t.Fatal("lost quoted dependency schema", text)
			}
		}
		if f.name == "earth" && !strings.Contains(text, "6378168") {
			t.Fatal("lost radius literal", text)
		}
		if len(f.calls) > 0 && !strings.Contains(text, "parser.FunctionExpr") {
			t.Fatal("missing concrete call syntax", text)
		}
		if f.caseExpr && !strings.Contains(text, "parser.CaseExpr") {
			t.Fatal("missing concrete CASE syntax", f.name, text)
		}
		if f.args != "" {
			for n := 1; n <= strings.Count(f.args, ",")+1; n++ {
				if !strings.Contains(text, "{parser.ParameterExpr Number "+strconv.Itoa(n)+"}") {
					t.Fatal("missing released parameter syntax", n, f.name, text)
				}
			}
		}
		if f.subscript && !strings.Contains(text, "parser.SubscriptExpr") {
			t.Fatal("missing concrete subscript syntax", text)
		}
	}
}

// Resolve generated identities through public type/routine aliases. Executor
// deliberately exposes QueryContext, not QueryRowContext. Consume and close the
// cursor before the next query and validate the adapter's actual OID carrier.
func pgProcInstalledOID(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, query, name string) int64 {
	t.Helper()
	rows, err := x.QueryContext(p.Ctx, query, name)
	if err != nil {
		t.Fatal(name, err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "OID" {
		t.Fatal(name, meta, err)
	}
	if !rows.Next() {
		t.Fatal("missing identity", name, rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(name, err)
	}
	value := fixture.OidReferenceValue(t, b.PG, "OID", raw)
	oid, ok := value.(int64)
	if !ok || oid <= 0 {
		t.Fatal("invalid identity", name, value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("identity multiplicity", name, rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(name, err)
	}
	return oid
}
