package sqltype

import "stuntgres/internal/sqlstate"

// InputSoftError excludes setup/import/internal/context errors. Callers use it
// only at fresh destination conversion seams, never around command evaluation.
// Do not unwrap: the selected domain evaluator preserves CHECK-expression
// failures as hard wrapped causes even when their SQLSTATE resembles input.
func InputSoftError(err error) bool {
	if marked, ok := err.(*sqlstate.InputError); ok {
		return marked.Recoverable()
	}
	e, ok := err.(*sqlstate.Error)
	if !ok {
		return false
	}
	switch e.Code {
	case "22001", "22003", "22007", "22008", "22009", "22015", "22018", "22023", "22P02", "23502", "23514":
		return true
	}
	if e.Code == "54000" {
		return e.Message == "number of array dimensions exceeds the maximum allowed (6)" || e.Message == "array size exceeds the maximum allowed"
	}
	return false
}
