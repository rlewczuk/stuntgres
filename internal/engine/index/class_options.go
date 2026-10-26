package index

import (
	"fmt"
	"math"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Class options follow the captured family's options callback, never a class
// spelling. Physical callbacks remain metadata; this provider owns configuration.
func hasSiglenOptions(view catalog.Read, class sqltype.IndexClass) bool {
	if !class.IsGiST() || class.InputOID() != uint32(sqltype.TSVector) {
		return false
	}
	member, ok := view.FamilySupport(ids.OID(class.FamilyOID()), ids.OID(class.InputOID()), ids.OID(class.InputOID()), 10)
	if !ok || member.Function != 3434 {
		return false
	}
	identity, ok := catalog.BuiltinRoutineIdentityByOID(member.Function)
	result, exists := catalog.BuiltinRoutineResultByOID(member.Function)
	if !ok || !exists || identity.Namespace != ids.PGCatalogNamespaceOID || identity.Signature != "2281" || result != 2278 {
		return false
	}
	if proc, exists := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function}); exists {
		return proc.Builtin != nil && proc.Builtin.Identity == identity && proc.Result == result && proc.Signature == identity.Signature
	}
	return true
}

func BindClassOptions(view catalog.Read, class sqltype.IndexClass, options []parser.ViewOption) (catalog.IndexClassOptions, error) {
	var captured catalog.IndexClassOptions
	if len(options) == 0 {
		return captured, nil
	}
	if _, err := routine.BindClassSupport(view, class); err != nil {
		return captured, err
	}
	if !hasSiglenOptions(view, class) {
		return captured, sqlError("22023", fmt.Sprintf("operator class %s has no options", class.Name))
	}
	for _, option := range options {
		name := option.Name.Text
		if strings.Contains(name, "=") {
			return catalog.IndexClassOptions{}, sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, name))
		}
		if name != "siglen" {
			return catalog.IndexClassOptions{}, sqlError("22023", fmt.Sprintf(`unrecognized parameter "%s"`, name))
		}
		if captured.Explicit {
			return catalog.IndexClassOptions{}, sqlError("22023", `parameter "siglen" specified more than once`)
		}
		n, suffix, ok := settings.ParseNumber(option.Value, true)
		n = math.RoundToEven(n)
		if !ok || suffix != "" || math.IsNaN(n) || n < math.MinInt32 || n > math.MaxInt32 {
			return catalog.IndexClassOptions{}, sqlError("22023", fmt.Sprintf(`invalid value for integer option "siglen": %s`, option.Value))
		}
		if n < 1 || n > 2024 {
			return catalog.IndexClassOptions{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`value %s out of bounds for option "siglen"`, option.Value), Detail: `Valid values are between "1" and "2024".`}
		}
		captured = catalog.IndexClassOptions{Siglen: int64(n), Text: option.Value, Explicit: true}
	}
	return captured, nil
}

func validateClassOptions(view catalog.Read, key catalog.IndexKey) error {
	o := key.ClassOptions
	if !o.Explicit {
		if o != (catalog.IndexClassOptions{}) {
			return fmt.Errorf("stuntgres: invalid absent index class options")
		}
		return nil
	}
	next, err := BindClassOptions(view, key.Class, []parser.ViewOption{{Name: parser.Identifier{Text: "siglen"}, Value: o.Text, HasValue: true}})
	if err != nil {
		return err
	}
	if next != o {
		return fmt.Errorf("stuntgres: inconsistent index class options")
	}
	return nil
}
