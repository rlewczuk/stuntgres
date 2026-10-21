package largeobject

import (
	"stuntgres/internal/parser"
	"testing"
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
