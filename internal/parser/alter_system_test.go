package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestAlterSystemParser(t *testing.T) {
	for _,tc := range []struct{ sql,action,name string; def bool }{
		{"ALTER SYSTEM SET application_name='x'","SET","application_name",false},
		{"ALTER SYSTEM SET application_name TO DEFAULT","SET","application_name",true},
		{`ALTER SYSTEM RESET "Custom"."Value"`,"RESET","Custom.Value",false},
		{"ALTER SYSTEM RESET ALL","RESET ALL","",false},
		{"ALTER SYSTEM SET LOCAL = on","SET","local",false},
		{"ALTER SYSTEM SET ROLE = false","SET","role",false},
		{"ALTER SYSTEM SET search_path TO public, pg_catalog","SET","search_path",false},
	} {
		statements,err := Parse(tc.sql)
		if err != nil || len(statements) != 1 { t.Fatalf("%s: %v %#v",tc.sql,err,statements) }
		node,ok := statements[0].(*AlterSystem)
		if !ok || node.Action != tc.action || node.Name.Text != tc.name || node.Default != tc.def || node.Pos.Character != 1 { t.Fatalf("%s: %#v",tc.sql,statements[0]) }
	}
	for _,value := range []string{"1.25","-2e3","+0.5","0x2a","1_000","999999999999999999999999999999999999999999"} {
		if _,err := Parse("ALTER SYSTEM SET work_mem="+value); err != nil { t.Fatalf("external numeric %s: %v",value,err) }
		if value != "999999999999999999999999999999999999999999" {
			if _,err := Parse("SET work_mem="+value); err == nil { t.Fatalf("session numeric guard broadened for %s",value) }
		} else if _,err := Parse("SET work_mem="+value); err != nil {
			t.Fatalf("existing session integer syntax narrowed: %v",err)
		}
	}
	statements,err := Parse("/*é*/ ALTER SYSTEM RESET ALL")
	if err != nil || len(statements) != 1 { t.Fatal(statements,err) }
	if pos := statements[0].(*AlterSystem).Pos; pos.Character != 7 || pos.Offset != 7 { t.Fatal(pos) }
	for _,sql := range []string{"CREATE TABLE system(system int)","SELECT 1 AS system"} {
		if _,err := Parse(sql); err != nil { t.Fatal("unreserved SYSTEM",err) }
	}
	for _,sql := range []string{
		"ALTER SYSTEM","ALTER SYSTEM RESET","ALTER SYSTEM RESET ALL junk",
		"ALTER SYSTEM SET application_name=","ALTER SYSTEM SET application_name=NULL",
		"ALTER SYSTEM SET application_name=$1","ALTER SYSTEM SET application_name FROM CURRENT",
		"ALTER SYSTEM SET SESSION application_name='x'","ALTER SYSTEM SET LOCAL application_name='x'",
		"ALTER SYSTEM SET TIME ZONE 'UTC'","ALTER SYSTEM SET NAMES 'UTF8'",
		"ALTER SYSTEM SET work_mem=1__0","ALTER SYSTEM SET work_mem=1e+",
		"SET application_name='must-not-run'; ALTER SYSTEM RESET ALL junk",
		"PREPARE p AS ALTER SYSTEM RESET ALL",
	} {
		statements,err := Parse(sql)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err,&d) || d.Code != "42601" { t.Fatalf("whole input %s: %#v %v",sql,statements,err) }
	}
}
