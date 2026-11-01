package largeobject

import (
	"stuntgres/internal/parser"
	"testing"
	"strings"
)

func TestLargeObjectReplayShapes(t *testing.T) {
	for _, tc := range []struct {
		sql        string
		bytes, acl bool
	}{
		{"SELECT pg_catalog.lo_create(910001::pg_catalog.oid)", true, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,4294967296::pg_catalog.int8,pg_catalog.decode('00ff00','hex'))", true, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl=NULL::pg_catalog.aclitem[] WHERE oid=910001::pg_catalog.oid", false, true},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[] WHERE oid=910001::pg_catalog.oid", false, true},
		{"SELECT pg_catalog.lo_create($1::pg_catalog.oid)", false, false},
		{"SELECT pg_catalog.lo_create(0::pg_catalog.oid)", false, false},
		{"SELECT pg_catalog.lo_create(910001::pg_catalog.oid) AS oid", false, false},
		{"SELECT pg_catalog.lo_create(910001::pg_catalog.oid) FROM pg_catalog.pg_roles", false, false},
		{"SELECT pg_catalog.lo_create(pg_catalog.lo_create(910001::pg_catalog.oid))", false, false},
		{"SELECT pg_catalog.lo_get(910001::pg_catalog.oid)", false, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,$1::pg_catalog.int8,pg_catalog.decode('00','hex'))", false, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode($1,'hex'))", false, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('0g','hex'))", false, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('00','base64'))", false, false},
		{"SELECT pg_catalog.lo_put(910001::pg_catalog.oid,0::pg_catalog.int8,pg_catalog.decode('','hex'))", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl=$1::pg_catalog.aclitem[] WHERE oid=910001::pg_catalog.oid", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[]", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[] WHERE oid=$1::pg_catalog.oid", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[],lomowner=10 WHERE oid=910001::pg_catalog.oid", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[] WHERE oid=910001::pg_catalog.oid OR oid=910002::pg_catalog.oid", false, false},
		{"UPDATE pg_catalog.pg_largeobject_metadata SET lomacl='{}'::pg_catalog.aclitem[] WHERE oid=910001::pg_catalog.oid RETURNING oid", false, false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := parser.Parse(tc.sql)
			if err != nil || len(statements) != 1 {
				t.Fatal("fixture syntax", err)
			}
			var gotBytes, gotACL bool
			switch s := statements[0].(type) {
			case *parser.Select:
				gotBytes = RestoresBytes(s)
			case *parser.Update:
				_, _, _, gotACL = ACLRepair(s)
			}
			if gotBytes != tc.bytes || gotACL != tc.acl {
				t.Fatalf("admission bytes=%t ACL=%t; want%t,%t", gotBytes, gotACL, tc.bytes, tc.acl)
			}
		})
	}
}

func TestLargeObjectTruncateReplay(t *testing.T) {
	const generated = "SELECT pg_catalog.lo_truncate64(pg_catalog.lo_open(910001::pg_catalog.oid,131072::pg_catalog.int4),2048::pg_catalog.int8)"
	parse := func(t *testing.T, query string) *parser.Select {
		t.Helper()
		statements, err := parser.Parse(query)
		if err != nil || len(statements) != 1 { t.Fatal("fixture syntax", query, err) }
		s, ok := statements[0].(*parser.Select)
		if !ok { t.Fatal("expected SELECT", query) }
		return s
	}
	for _, query := range []string{
		generated,
		strings.Replace(generated, "2048::", "0::", 1),
		strings.Replace(generated, "2048::", "4398046509056::", 1),
		strings.Replace(generated, "910001::", "4294967295::", 1),
	} {
		if !RestoresBytes(parse(t, query)) { t.Fatal("rejected generated endpoint", query) }
	}
	for _, replacement := range [][2]string{
		{"pg_catalog.lo_truncate64", "lo_truncate64"},
		{"pg_catalog.lo_truncate64", "pg_catalog.lo_truncate"},
		{"pg_catalog.lo_open", "lo_open"},
		{"910001::pg_catalog.oid", "0::pg_catalog.oid"},
		{"910001::pg_catalog.oid", "-1::pg_catalog.oid"},
		{"910001::pg_catalog.oid", "4294967296::pg_catalog.oid"},
		{"910001::pg_catalog.oid", "$1::pg_catalog.oid"},
		{"910001::pg_catalog.oid", "910001::oid"},
		{"910001::pg_catalog.oid", "(910000+1)::pg_catalog.oid"},
		{"131072::pg_catalog.int4", "262144::pg_catalog.int4"},
		{"131072::pg_catalog.int4", "393216::pg_catalog.int4"},
		{"131072::pg_catalog.int4", "131073::pg_catalog.int4"},
		{"131072::pg_catalog.int4", "0::pg_catalog.int4"},
		{"131072::pg_catalog.int4", "131072::pg_catalog.int8"},
		{"131072::pg_catalog.int4", "$1::pg_catalog.int4"},
		{"131072::pg_catalog.int4", "'131072'::pg_catalog.int4"},
		{"2048::pg_catalog.int8", "1::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "-2048::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "4398046509057::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "4398046511104::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "$1::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "2048::pg_catalog.int4"},
		{"2048::pg_catalog.int8", "'2048'::pg_catalog.int8"},
		{"2048::pg_catalog.int8", "(1024+1024)::pg_catalog.int8"},
		{"pg_catalog.lo_open(910001::pg_catalog.oid,131072::pg_catalog.int4)", "(pg_catalog.lo_open(910001::pg_catalog.oid,131072::pg_catalog.int4))::pg_catalog.int4"},
		{"2048::pg_catalog.int8)", "2048::pg_catalog.int8,0)"},
		{"131072::pg_catalog.int4)", "131072::pg_catalog.int4,0)"},
		{"pg_catalog.lo_open(910001::pg_catalog.oid,131072::pg_catalog.int4)", "pg_catalog.lo_open(obj => 910001::pg_catalog.oid,mode => 131072::pg_catalog.int4)"},
	} {
		query := strings.Replace(generated, replacement[0], replacement[1], 1)
		if RestoresBytes(parse(t, query)) { t.Fatal("admitted widened endpoint", query) }
	}
	for _, query := range []string{
		generated+" AS result", generated+" WHERE true", generated+" FROM pg_catalog.pg_roles",
		generated+" ORDER BY 1", generated+" LIMIT 1", generated+",1",
		strings.Replace(generated, "SELECT ", "SELECT DISTINCT ", 1),
		"WITH x AS (SELECT 1) "+generated,
		"("+generated+")",
	} {
		if RestoresBytes(parse(t, query)) { t.Fatal("admitted widened SELECT", query) }
	}
	// Check every decoration and non-generated provenance independently on both
	// nodes. Ordinary parsed calls carry CompositeCandidate=true.
	for _, decorate := range []func(*parser.FunctionExpr){
		func(c *parser.FunctionExpr) { c.ArgNames = []parser.Identifier{{Text:"x"}} },
		func(c *parser.FunctionExpr) { c.Variadic = true },
		func(c *parser.FunctionExpr) { c.Window = &parser.WindowSpec{} },
		func(c *parser.FunctionExpr) { c.Star = true },
		func(c *parser.FunctionExpr) { c.Distinct = true },
		func(c *parser.FunctionExpr) { c.All = true },
		func(c *parser.FunctionExpr) { c.Order = []parser.OrderKey{{}} },
		func(c *parser.FunctionExpr) { c.Filter = parser.LiteralExpr{} },
		func(c *parser.FunctionExpr) { c.Within = true },
		func(c *parser.FunctionExpr) { c.ColumnDefinitions = []parser.FunctionColumnDefinition{{}} },
		func(c *parser.FunctionExpr) { c.ProcedureCall = true },
		func(c *parser.FunctionExpr) { c.ResolutionOnly = true },
		func(c *parser.FunctionExpr) { c.CompositeCandidate = false },
		func(c *parser.FunctionExpr) { c.StoredSQLRoutine = true },
		func(c *parser.FunctionExpr) { c.StoredProviderRoutine = true },
		func(c *parser.FunctionExpr) { c.StoredProviderSymbol = "not-generated" },
		func(c *parser.FunctionExpr) { c.Syntax = parser.SQLFunctionSyntax },
		func(c *parser.FunctionExpr) { c.StringSyntax = parser.TrimStringSyntax },
	} {
		for _, inner := range []bool{false, true} {
			s := parse(t, generated)
			outer := s.Targets[0].(parser.FunctionExpr)
			if inner {
				open := outer.Args[0].(parser.FunctionExpr)
				decorate(&open)
				outer.Args[0] = open
			} else {
				decorate(&outer)
			}
			s.Targets[0] = outer
			if RestoresBytes(s) { t.Fatal("admitted decorated call", inner, outer) }
		}
	}
	if RestoresBytes(nil) { t.Fatal("nil SELECT admitted") }
}
