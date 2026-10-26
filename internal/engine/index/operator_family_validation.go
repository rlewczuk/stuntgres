package index

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// familySupportTypes applies ALTER FAMILY admission, deliberately not the
// stronger complete opclass validators. Explicit association need not equal
// the routine's formal types.
func familySupportTypes(method ids.OID, slot int16, args []ids.OID, result ids.OID, left, right ids.OID) (ids.OID, ids.OID, error) {
	return classSupportTypes(method, slot, args, result, left, right, 0)
}

func classSupportTypes(method ids.OID, slot int16, args []ids.OID, result ids.OID, left, right, input ids.OID) (ids.OID, ids.OID, error) {
	fail := func(message string) (ids.OID, ids.OID, error) { return 0, 0, familyError("42P17", message) }
	options := method == 403 && slot == 5 || method == 405 && slot == 3 || method == 783 && slot == 10 || method == 4000 && slot == 7
	if input != 0 {
		if options && left != 0 && (left != input || right != input) {
			return fail("associated data types for operator class options parsing functions must match opclass input type")
		}
		infer := method == 403 && (slot == 1 || slot == 3) || method == 405 && (slot == 1 || slot == 2)
		if left == 0 && !infer {
			left, right = input, input
		}
	}
	if options {
		if left != right {
			return fail("left and right associated data types for operator class options parsing functions must match")
		}
		if len(args) != 1 || args[0] != 2281 || result != 2278 {
			return 0, 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: "invalid operator class options parsing function", Hint: "Valid signature of operator class options parsing function is (internal) RETURNS void."}
		}
	} else if method == 403 {
		switch slot {
		case 1:
			if len(args) != 2 {
				return fail("ordering comparison functions must have two arguments")
			}
			if result != 23 {
				return fail("ordering comparison functions must return integer")
			}
			if left == 0 {
				left, right = args[0], args[1]
			}
		case 2:
			if len(args) != 1 || args[0] != 2281 {
				return fail("ordering sort support functions must accept type \"internal\"")
			}
			if result != 2278 {
				return fail("ordering sort support functions must return void")
			}
		case 3:
			if len(args) != 5 {
				return fail("ordering in_range functions must have five arguments")
			}
			if result != 16 {
				return fail("ordering in_range functions must return boolean")
			}
			if left == 0 {
				left, right = args[0], args[2]
			}
		case 4:
			if len(args) != 1 {
				return fail("ordering equal image functions must have one argument")
			}
			if result != 16 {
				return fail("ordering equal image functions must return boolean")
			}
			if left != right {
				return fail("ordering equal image functions must not be cross-type")
			}
		case 6:
			if len(args) != 1 || args[0] != 2281 {
				return fail("btree skip support functions must accept type \"internal\"")
			}
			if result != 2278 {
				return fail("btree skip support functions must return void")
			}
			if left != right {
				return fail("btree skip support functions must not be cross-type")
			}
		}
	} else if method == 405 {
		switch slot {
		case 1:
			if len(args) != 1 {
				return fail("hash function 1 must have one argument")
			}
			if result != 23 {
				return fail("hash function 1 must return integer")
			}
			if left == 0 {
				left, right = args[0], args[0]
			}
		case 2:
			if len(args) != 2 {
				return fail("hash function 2 must have two arguments")
			}
			if result != 20 {
				return fail("hash function 2 must return bigint")
			}
			if left == 0 {
				left, right = args[0], args[0]
			}
		}
	}
	if left == 0 || right == 0 {
		return fail("associated data types must be specified for index support function")
	}
	return left, right, nil
}
