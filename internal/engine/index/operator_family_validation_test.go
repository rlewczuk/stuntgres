package index

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestOperatorFamilyValidation(t *testing.T) {
	cases := []struct {
		name                                     string
		method                                   ids.OID
		slot                                     int16
		args                                     []ids.OID
		result, left, right, wantLeft, wantRight ids.OID
		message, hint                            string
	}{
		{name: "comparison inference", method: 403, slot: 1, args: []ids.OID{23, 23}, result: 23, wantLeft: 23, wantRight: 23},
		{name: "explicit independent association", method: 403, slot: 1, args: []ids.OID{23, 23}, result: 23, left: 21, right: 20, wantLeft: 21, wantRight: 20},
		{name: "comparison arity", method: 403, slot: 1, args: []ids.OID{23}, result: 23, message: "ordering comparison functions must have two arguments"},
		{name: "comparison result", method: 403, slot: 1, args: []ids.OID{23, 23}, result: 16, message: "ordering comparison functions must return integer"},
		{name: "in range inference", method: 403, slot: 3, args: []ids.OID{23, 23, 20, 16, 16}, result: 16, wantLeft: 23, wantRight: 20},
		{name: "weaker equalimage formal", method: 403, slot: 4, args: []ids.OID{2275}, result: 16, left: 23, right: 23, wantLeft: 23, wantRight: 23},
		{name: "equalimage cross", method: 403, slot: 4, args: []ids.OID{2275}, result: 16, left: 23, right: 20, message: "ordering equal image functions must not be cross-type"},
		{name: "sort explicit pair", method: 403, slot: 2, args: []ids.OID{2281}, result: 2278, left: 23, right: 23, wantLeft: 23, wantRight: 23},
		{name: "sort missing pair", method: 403, slot: 2, args: []ids.OID{2281}, result: 2278, message: "associated data types must be specified for index support function"},
		{name: "skip", method: 403, slot: 6, args: []ids.OID{2281}, result: 2278, left: 23, right: 23, wantLeft: 23, wantRight: 23},
		{name: "skip cross", method: 403, slot: 6, args: []ids.OID{2281}, result: 2278, left: 23, right: 20, message: "btree skip support functions must not be cross-type"},
		{name: "hash inference", method: 405, slot: 1, args: []ids.OID{23}, result: 23, wantLeft: 23, wantRight: 23},
		{name: "hash extended weaker second formal", method: 405, slot: 2, args: []ids.OID{20, 20}, result: 20, left: 23, right: 23, wantLeft: 23, wantRight: 23},
		{name: "hash extended inference", method: 405, slot: 2, args: []ids.OID{23, 20}, result: 20, wantLeft: 23, wantRight: 23},
		{name: "hash arity", method: 405, slot: 1, args: []ids.OID{23, 23}, result: 23, message: "hash function 1 must have one argument"},
		{name: "hash result", method: 405, slot: 1, args: []ids.OID{20}, result: 20, message: "hash function 1 must return integer"},
		{name: "generic gist", method: 783, slot: 12, args: []ids.OID{23, 23}, result: 23, left: 23, right: 23, wantLeft: 23, wantRight: 23},
		{name: "generic gist missing pair", method: 783, slot: 1, args: []ids.OID{23, 23}, result: 23, message: "associated data types must be specified for index support function"},
	}
	for _, method := range []ids.OID{403, 405, 783} {
		slot := int16(5)
		if method == 405 {
			slot = 3
		} else if method == 783 {
			slot = 10
		}
		cases = append(cases, struct {
			name                                     string
			method                                   ids.OID
			slot                                     int16
			args                                     []ids.OID
			result, left, right, wantLeft, wantRight ids.OID
			message, hint                            string
		}{name: "options", method: method, slot: slot, args: []ids.OID{2281}, result: 2278, left: 23, right: 23, wantLeft: 23, wantRight: 23})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left, right, err := familySupportTypes(tc.method, tc.slot, tc.args, tc.result, tc.left, tc.right)
			if tc.message == "" {
				if err != nil || left != tc.wantLeft || right != tc.wantRight {
					t.Fatal(left, right, err)
				}
				return
			}
			diag, ok := err.(*sqlstate.Error)
			if !ok || diag.Code != "42P17" || diag.Message != tc.message || diag.Hint != tc.hint {
				t.Fatalf("diagnostic: %#v", err)
			}
		})
	}
}
