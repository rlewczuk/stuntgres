package control

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestAccessRelationsACLRepairAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql, column string
		valid       bool
	}{
		{`UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid='public.t'::pg_catalog.regclass`, "", true},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass AND attname='Mixed'::pg_catalog.name`, "Mixed", true},
		{`UPDATE pg_catalog.pg_attribute SET attacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass AND attname='a'::pg_catalog.name`, "a", true},
		{`UPDATE pg_catalog.pg_attribute SET attacl=pg_catalog.acldefault('c',0) WHERE attrelid='public.t'::pg_catalog.regclass AND attname='a'::pg_catalog.name`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl='{}'::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass AND attname='a'::pg_catalog.name`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass OR attname='a'::pg_catalog.name`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass AND attname='a'::pg_catalog.name AND TRUE`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[] WHERE attrelid='public.t'::pg_catalog.regclass AND attnum=1`, "", false},
		{`UPDATE pg_catalog.pg_attribute SET attacl=NULL::pg_catalog.aclitem[],attnum=2 WHERE attrelid='public.t'::pg_catalog.regclass AND attname='a'::pg_catalog.name`, "", false},
		{`UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid=$1::pg_catalog.regclass`, "", false},
		{`UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid=pg_catalog.to_regclass('public.t')`, "", false},
		{`UPDATE pg_catalog.pg_class SET relacl=NULL::pg_catalog.aclitem[] WHERE oid=(SELECT 'public.t'::pg_catalog.regclass)`, "", false},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal("unit syntax", tc.sql, err)
		}
		r, ok := ParseObjectACLRepair(statements[0].(*parser.Update))
		if ok != tc.valid {
			t.Fatal("admission", tc.sql, ok)
		}
		if ok && (r.Class != ids.RelationClassID || r.Identity != "public.t" || r.Column != tc.column) {
			t.Fatal("destination identity", r)
		}
	}
}
