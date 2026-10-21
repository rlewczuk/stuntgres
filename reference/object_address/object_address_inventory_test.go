package reference_test

import (
	"bufio"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func objectAddressTSV(t *testing.T, path string, count int) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var rows [][]string
	for scanner.Scan() {
		rows = append(rows, strings.Split(scanner.Text(), "\t"))
	}
	if err := scanner.Err(); err != nil || len(rows) != count+1 {
		t.Fatal(path, len(rows), err)
	}
	return rows[1:]
}
func objectAddressQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func objectAddressIdent(s string) string {
	// Test-owned released fixture spelling. PostgreSQL keywords need quotes.
	if strings.Contains(objectAddressQuotedKeywords, " "+s+" ") {
		return objectAddressQuote(s)
	}
	for i, r := range s {
		if !(r == '_' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9') {
			return objectAddressQuote(s)
		}
	}
	return s
}
func objectAddressTypeFacts(t *testing.T) ([][]string, map[string]string, map[string]string) {
	t.Helper()
	rows := objectAddressTSV(t, "../../docs/design/regtype-pg18-inventory.tsv", 621)
	byID, bySymbol := map[string]string{}, map[string]string{}
	byRaw := map[string][]string{}
	for _, r := range rows {
		byRaw[r[0]+"."+r[1]] = r
	}
	aliases := map[string]string{"bool": "boolean", "int2": "smallint", "int4": "integer", "int8": "bigint", "float4": "real", "float8": "double precision", "numeric": "numeric", "bpchar": "character", "varchar": "character varying", "bit": "bit", "varbit": "bit varying", "time": "time without time zone", "timetz": "time with time zone", "timestamp": "timestamp without time zone", "timestamptz": "timestamp with time zone", "interval": "interval"}
	// JSON is a SQL type spelling even though quote_identifier("json")
	// quotes its catalog name. Pinned PG18 keeps those fields distinct.
	aliases["json"] = "json"
	var spell func([]string) string
	spell = func(r []string) string {
		if r[5] != "0" && strings.HasPrefix(r[1], "_") {
			for _, e := range rows {
				if e[2] == r[5] {
					return spell(e) + "[]"
				}
			}
		}
		if r[0] == "pg_catalog" {
			if a, ok := aliases[r[1]]; ok {
				return a
			}
		}
		return objectAddressIdent(r[0]) + "." + objectAddressIdent(r[1])
	}
	for _, r := range rows {
		s := spell(r)
		byID[r[2]] = s
		if r[0] == "pg_catalog" {
			bySymbol[r[1]] = s
		}
	}
	for _, r := range rows {
		if r[0] == "pg_catalog" && r[6] != "0" {
			if a, ok := byRaw["pg_catalog._"+r[1]]; ok {
				bySymbol[r[1]+"[]"] = spell(a)
			}
		}
	}
	return rows, byID, bySymbol
}

type objectAddressInventoryRow struct {
	class, id             int64
	kind                  string
	schema, name          any
	identity, description string
	names, args           []string
}

// Batch public SQL to keep exhaustive fixed inventories inside the fixture's
// request deadline. Expected rows come from released fixtures, never SQL output.
func objectAddressInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, rows []objectAddressInventoryRow, inverse bool) {
	t.Helper()
	// Amortize statement admission and direct/prepared parsing without changing
	// the exhaustive row set or renewing the fixture's original context.
	const batchSize = 128
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		t.Run(fmt.Sprintf("rows_%d_%d", start, end), func(t *testing.T) {
			var values []string
			var want [][]any
			for _, r := range rows[start:end] {
				values = append(values, fmt.Sprintf("(%d::oid,%d::oid,%s,%s,%s)", r.class, r.id, objectAddressLiteral(r.kind), objectAddressArray(r.names), objectAddressArray(r.args)))
				row := []any{r.id, r.kind, r.schema, r.name, r.identity, objectAddressArrayText(t, r.names), objectAddressArrayText(t, r.args), r.description}
				if inverse {
					row = append(row, true)
				}
				want = append(want, row)
			}
			columns := []string{"id", "type", "schema", "name", "identity", "object_names", "object_args", "description"}
			types := []string{"INT8", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "_TEXT", "TEXT"}
			selectInverse, fromInverse := "", ""
			if inverse {
				columns = append(columns, "roundtrip")
				types = append(types, "BOOL")
				selectInverse = ",(g.classid=v.class AND g.objid=v.id AND g.objsubid=0) AS roundtrip"
				fromInverse = ",LATERAL pg_get_object_address(v.kind,v.names,v.args) g"
			}
			q := "SELECT v.id::bigint AS id,i.type,i.schema,i.name,i.identity,a.object_names,a.object_args,pg_describe_object(v.class,v.id,0) AS description" + selectInverse + " FROM (VALUES " + strings.Join(values, ",") + ") v(class,id,kind,names,args),LATERAL pg_identify_object(v.class,v.id,0) i,LATERAL pg_identify_object_as_address(v.class,v.id,0) a" + fromInverse + " ORDER BY v.id"
			// Stable numeric order is explicitly requested, not catalog order.
			for i := 1; i < len(want); i++ {
				for j := i; j > 0 && want[j][0].(int64) < want[j-1][0].(int64); j-- {
					want[j], want[j-1] = want[j-1], want[j]
				}
			}
			objectAddressRun(t, p, b, c, fixture.Select(q, columns, types, want))
		})
	}
}

func TestReferenceObjectAddressAuthorityTypes(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		types, byID, _ := objectAddressTypeFacts(t)
		var facts []objectAddressInventoryRow
		for _, r := range types {
			id, err := strconv.ParseInt(r[2], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			s := byID[r[2]]
			facts = append(facts, objectAddressInventoryRow{1247, id, "type", objectAddressIdent(r[0]), objectAddressIdent(r[1]), s, "type " + r[17], []string{s}, nil})
		}
		objectAddressInventory(t, p, b, c, facts, true)
		for _, q := range []string{"CREATE DOMAIN addr_type_domain AS INT CHECK(VALUE>0)", "CREATE TYPE addr_type_enum AS ENUM('a','b')", "CREATE TYPE addr_type_composite AS(a INT)", "CREATE TABLE addr_type_table(a INT)"} {
			objectAddressRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, name := range []string{"addr_type_domain", "addr_type_enum", "addr_type_composite", "addr_type_table"} {
			for _, array := range []bool{false, true} {
				suffix := ""
				raw := name
				if array {
					suffix = "[]"
					raw = "_" + name
				}
				objectAddressWitness(t, p, b, c, 1247, "type", []string{"public." + name + suffix}, nil, "public", raw, "public."+name+suffix, "type "+name+suffix, "SELECT '"+name+suffix+"'::regtype::oid")
			}
		}
	})
}

type objectAddressRoutineFact struct {
	id               int64
	kind, name, args string
}

func objectAddressAllRoutines() []objectAddressRoutineFact {
	var rows []objectAddressRoutineFact
	for _, batch := range [][]objectAddressRoutineFact{objectAddressRoutineFacts0(), objectAddressRoutineFacts1(), objectAddressRoutineFacts2(), objectAddressRoutineFacts3(), objectAddressRoutineFacts4(), objectAddressRoutineFacts5()} {
		rows = append(rows, batch...)
	}
	return rows
}
func TestReferenceObjectAddressAuthorityRoutines(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		_, _, types := objectAddressTypeFacts(t)
		var facts []objectAddressInventoryRow
		rows := objectAddressAllRoutines()
		if len(rows) != 3397 {
			t.Fatal("fixed routine inventory", len(rows))
		}
		for _, r := range rows {
			args := []string{}
			for _, symbol := range strings.Fields(r.args) {
				s, ok := types[symbol]
				if !ok {
					t.Fatal("missing released type", symbol)
				}
				args = append(args, s)
			}
			kind := "function"
			if r.kind == "a" {
				kind = "aggregate"
			}
			if r.kind == "p" {
				kind = "procedure"
			}
			identity := "pg_catalog." + objectAddressIdent(r.name) + "(" + strings.Join(args, ",") + ")"
			// Human routine descriptions use visible type names from the independent
			// type fixture; pg_catalog qualification is not forced there.
			humanArgs := make([]string, len(args))
			for i, s := range args {
				humanArgs[i] = strings.TrimPrefix(s, "pg_catalog.")
			}
			description := "function " + objectAddressIdent(r.name) + "(" + strings.Join(humanArgs, ",") + ")"
			facts = append(facts, objectAddressInventoryRow{1255, r.id, kind, "pg_catalog", nil, identity, description, []string{"pg_catalog", r.name}, args})
		}
		objectAddressInventory(t, p, b, c, facts, true)
	})
}
func TestReferenceObjectAddressAuthorityOperators(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		_, types, _ := objectAddressTypeFacts(t)
		rows := objectAddressTSV(t, "../../docs/design/regoperator-pg18-inventory.tsv", 799)
		var facts, unary []objectAddressInventoryRow
		for _, r := range rows {
			id, err := strconv.ParseInt(r[0], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			left, right := types[r[3]], types[r[4]]
			if r[3] == "0" {
				left = "NONE"
			}
			if r[4] == "0" {
				right = "NONE"
			}
			identity := "pg_catalog." + r[2] + "(" + left + "," + right + ")"
			fact := objectAddressInventoryRow{2617, id, "operator", "pg_catalog", nil, identity, "operator " + r[7], []string{"pg_catalog", r[2]}, []string{left, right}}
			if r[3] == "0" || r[4] == "0" {
				unary = append(unary, fact)
			} else {
				facts = append(facts, fact)
			}
		}
		if len(facts)+len(unary) != 799 {
			t.Fatal("complete operator address coverage", len(facts), len(unary))
		}
		t.Run("binary", func(t *testing.T) { objectAddressInventory(t, p, b, c, facts, true) })
		t.Run("unary_numeric", func(t *testing.T) {
			numeric := append([]objectAddressInventoryRow(nil), unary...)
			for i := range numeric {
				if numeric[i].args[0] == "NONE" {
					numeric[i].args = numeric[i].args[1:]
				} else {
					numeric[i].args = numeric[i].args[:1]
				}
			}
			objectAddressInventory(t, p, b, c, numeric, false)
		})
		var cases []objectAddressDiagnosticCase
		for _, r := range unary {
			q := "SELECT pg_get_object_address('operator'," + objectAddressArray(r.names) + "," + objectAddressArray(r.args) + ")"
			cases = append(cases, objectAddressDiagnosticCase{strconv.FormatInt(r.id, 10), q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "NONE"`, Position: "1", Context: `invalid type name "NONE"`}, true})
		}
		t.Run("unary_inverse", func(t *testing.T) { objectAddressDiagnostics(t, p, b, c, cases) })
	})
}
