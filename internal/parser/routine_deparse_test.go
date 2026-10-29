package parser

import "testing"

func TestRoutineStandardBodyPresentation(t *testing.T) {
	for _,tc := range []struct{sql string; single bool; returns []bool}{
		{"CREATE FUNCTION rd_return() RETURNS integer RETURN 7",true,[]bool{true}},
		{"CREATE FUNCTION rd_atomic() RETURNS integer BEGIN ATOMIC SELECT 7; END",false,[]bool{false}},
		{"CREATE FUNCTION rd_atomic() RETURNS integer BEGIN ATOMIC RETURN 7; END",false,[]bool{true}},
		{"CREATE FUNCTION rd_atomic() RETURNS integer BEGIN ATOMIC SELECT 7; RETURN 8; END",false,[]bool{false,true}},
		{"CREATE OR REPLACE FUNCTION rd_return() RETURNS integer LANGUAGE SQL RETURN 7",true,[]bool{true}},
		{"CREATE PROCEDURE rd_proc() LANGUAGE SQL BEGIN ATOMIC SELECT 7; END",false,[]bool{false}},
	} {
		statements,err := Parse(tc.sql)
		if err != nil || len(statements) != 1 { t.Fatal(tc.sql,statements,err) }
		d := statements[0].(*CreateFunction)
		if d.StandardReturn != tc.single || len(d.Standard) != len(tc.returns) { t.Fatal(tc.sql,d) }
		for i,want := range tc.returns {
			s,ok := d.Standard[i].(*Select)
			if !ok || s.RoutineReturn != want { t.Fatalf("%s statement %d: %#v",tc.sql,i,d.Standard[i]) }
		}
	}
}
