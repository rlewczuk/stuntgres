package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillArrays(t *testing.T) {
	t.Run("validation_precedence", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'unchanged') RETURNING k,v,ctid,xmin,xmax")
			for _, tc := range []struct{ input, code, message string }{
				{"{NULL}", "22004", "array must not contain nulls"},
				{"{{\"(0,1)\"},{\"(0,2)\"}}", "22000", "argument must be empty or one-dimensional array"},
				{"{{NULL},{\"(0,1)\"}}", "22004", "array must not contain nulls"},
			} {
				for _, target := range []any{"ks_part.heap", int64(0)} {
					s := ksError(killSQL, tc.code, tc.message, target, tc.input)
					s.ErrorStage = "execute"
					freezeBoth(t, p, b, c, s)
					ksRows(t, p, b, c, a)
				}
			}
			q := "SELECT ks_part.heap_force_kill('ks_part.heap'::regclass,$1::text::tid[]) AS killed"
			for _, tc := range []struct{ input, code, message, detail string }{
				{"bad", "22P02", "malformed array literal: \"bad\"", "Array value must start with \"{\" or dimension information."},
				{"{\"(0,1)\",,NULL}", "22P02", "", "Unexpected \",\" character."},
				{"{{\"(0,1)\"},{NULL,NULL}}", "22P02", "", "Multidimensional arrays must have sub-arrays with matching dimensions."},
				{"[0:1]={\"(0,1)\"}", "22P02", "", "Specified array dimensions do not match array contents."},
				{"[2:1]={}", "2202E", "upper bound cannot be less than lower bound", ""},
				{"[2147483647:2147483647]={\"(0,1)\"}", "54000", "array upper bound is too large: 2147483647", ""},
				{"[2147483648:2147483648]={\"(0,1)\"}", "54000", "array bound is out of integer range", ""},
			} {
				message := tc.message
				if message == "" {
					message = "malformed array literal: \"" + tc.input + "\""
				}
				s := ksError(q, tc.code, message, tc.input)
				s.Diagnostic.Detail, s.ErrorStage = tc.detail, "execute"
				freezeBoth(t, p, b, c, s)
				ksRows(t, p, b, c, a)
			}
		})
	})
	for _, lower := range []int{0, -7, 17} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("lower_%d/prepared_%t", lower, prepared), func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'lower-bound') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
					s := ksVoid(killSQL, "ks_part.heap", fmt.Sprintf("[%d:%d]=%s", lower, lower, freezeTIDs(a.tid)))
					s.Prepared = prepared
					freezeRun(t, p, b, c, s)
					ksRows(t, p, b, c, z)
				})
			})
		}
	}
	for _, count := range []int{65535, 65536} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("duplicates_%d/prepared_%t", count, prepared), func(t *testing.T) {
				ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					var notices []fixture.Diagnostic
					_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					b.Recorder = rec
					a := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(1,'large') RETURNING k,v,ctid,xmin,xmax")
					z := freezeOne(t, p, nc, "INSERT INTO ks_part.heap VALUES(2,'kept') RETURNING k,v,ctid,xmin,xmax")
					input := "{" + strings.Repeat(fmt.Sprintf("%q,", a.tid), count-1) + fmt.Sprintf("%q}", a.tid)
					s := ksVoid(killSQL, "ks_part.heap", input)
					s.Prepared = prepared
					notices = nil
					freezeRun(t, p, b, nc, s)
					if len(notices) != 0 {
						t.Fatalf("intra-call duplicate notices %#v", notices)
					}
					ksRows(t, p, b, nc, z)
				})
			})
		}
	}
}
