package control

import (
	"strings"
	"testing"

	"stuntgres/internal/parser"
)

func TestAccessExecutionACLRepairAdmission(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want bool
	}{
		{"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='public.f(integer)'::pg_catalog.regprocedure", true},
		{"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_current_logfile()'::pg_catalog.regprocedure", true},
		{"UPDATE pg_catalog.pg_proc SET proacl=NULL::pg_catalog.aclitem[] WHERE oid='pg_catalog.pg_current_logfile(text)'::pg_catalog.regprocedure", true},
		{"UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid='pg_catalog.pg_current_logfile()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proacl=NULL::aclitem[] WHERE oid='pg_catalog.pg_current_logfile(text)'::pg_catalog.regprocedure", false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, true},
		{"UPDATE pg_catalog.pg_language SET lanowner='recipient'::pg_catalog.regrole WHERE lanname='sql'::pg_catalog.name", true},
		{`UPDATE pg_catalog.pg_foreign_data_wrapper SET fdwacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE fdwname='wrapper'::pg_catalog.name`, true},
		{`UPDATE pg_catalog.pg_foreign_server SET srvacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE srvname='server'::pg_catalog.name`, true},
		{`UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE oid='f()'::pg_catalog.regprocedure`, true},
		{"UPDATE pg_catalog.pg_namespace SET nspacl=NULL::pg_catalog.aclitem[] WHERE nspname='private'::pg_catalog.name", true},
		{"UPDATE pg_catalog.pg_namespace SET nspowner='recipient'::pg_catalog.regrole WHERE nspname='public'::pg_catalog.name", true},
		{"UPDATE pg_catalog.pg_tablespace SET spcowner='recipient'::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name", true},
		{"UPDATE pg_catalog.pg_tablespace SET spcacl=NULL::pg_catalog.aclitem[] WHERE spcname='pg_default'::pg_catalog.name", true},
		{"UPDATE pg_catalog.pg_type SET typacl=NULL::pg_catalog.aclitem[] WHERE oid='public.d[]'::pg_catalog.regtype", true},
		{"UPDATE pg_catalog.pg_database SET datacl=NULL::pg_catalog.aclitem[] WHERE datname='postgres'::pg_catalog.name", false},
		{"UPDATE pg_catalog.pg_type SET typowner='recipient'::pg_catalog.regrole WHERE oid='public.d'::pg_catalog.regtype", false},
		{"UPDATE pg_catalog.pg_namespace SET nspacl=NULL::pg_catalog.aclitem[] WHERE nspname='public'::pg_catalog.name OR TRUE", false},
		{"UPDATE pg_catalog.pg_type SET typacl=NULL::pg_catalog.aclitem[] WHERE oid=23::pg_catalog.oid", false},
		{"UPDATE pg_catalog.pg_language SET lanacl='{}'::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name", false},
		{`UPDATE pg_catalog.pg_language SET lanacl=public.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('r'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::pg_catalog."char",10::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::pg_catalog."char",$1::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::"char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault(pg_catalog.lower('c')::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid,0)::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid) WHERE lanname='sql'::pg_catalog.name`, false},
		{`UPDATE pg_catalog.pg_language SET lanacl=(pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid) FILTER (WHERE FALSE))::pg_catalog.aclitem[] WHERE lanname='sql'::pg_catalog.name`, false},
		{"UPDATE public.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid='f()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proacl='{}'::aclitem[] WHERE oid='f()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid=123::pg_catalog.oid", false},
		{"UPDATE pg_catalog.pg_proc SET proacl=$1::pg_catalog.aclitem[] WHERE oid='f()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid='f()'::pg_catalog.regprocedure OR TRUE", false},
		{"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[],proowner=10 WHERE oid='f()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proowner='recipient'::pg_catalog.regrole WHERE oid='f()'::pg_catalog.regprocedure", false},
		{"UPDATE pg_catalog.pg_proc SET proacl='{}'::pg_catalog.aclitem[] WHERE oid='f()'::pg_catalog.regprocedure RETURNING oid", false},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal("fixture syntax", tc.sql, err)
		}
		raw, ok := statements[0].(*parser.Update)
		if !ok {
			t.Fatal(statements[0])
		}
		repair, got := ParseObjectACLRepair(raw)
		if got != tc.want {
			t.Fatal("repair admission", tc.sql, got, tc.want)
		}
		if tc.want && strings.Contains(tc.sql, "pg_catalog.acldefault") && (repair.Absent || repair.Owner || repair.Text != "{}") {
			t.Fatal("empty constructor lost explicit-empty provenance", repair)
		}
		if tc.want && strings.Contains(tc.sql, "proacl=NULL::pg_catalog.aclitem[]") && (!repair.Absent || repair.Owner || repair.Text != "") {
			t.Fatal("routine NULL repair lost raw absence", repair)
		}
	}
}
