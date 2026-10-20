package engine

// Option parsing and validation shared by CREATE OPERATOR and ALTER OPERATOR.
// These helpers read no engine state; staging lives in operator_ddl.go.

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ValidOperatorName(name string) bool {
	if len(name) == 0 || len(name) > 63 || name == "!=" || name == "=>" || strings.Contains(name, "/*") || strings.Contains(name, "--") {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("~!@#^&|`?+-*/%<>=", c) {
			return false
		}
	}
	return len(name) == 1 || !strings.ContainsAny(name[len(name)-1:], "+-") || strings.ContainsAny(name, "~!@#^&|`?%")
}

func OperatorOptionName(o parser.OperatorOption) (parser.ObjectName, error) {
	if !o.HasValue {
		return parser.ObjectName{}, sqlError("42601", o.Name+" requires a parameter")
	}
	switch o.Kind {
	case parser.OperatorOptionNameArgument:
		return o.Reference, nil
	case parser.OperatorOptionTypeArgument:
		if len(o.Type.Parts) != 0 {
			return parser.ObjectName{Parts: o.Type.Parts}, nil
		}
		parts := make([]parser.Identifier, 0, 2)
		if o.Type.Schema != nil {
			parts = append(parts, *o.Type.Schema)
		}
		parts = append(parts, parser.Identifier{Text: o.Type.Name, Pos: o.Type.Pos})
		return parser.ObjectName{Parts: parts}, nil
	case parser.OperatorOptionStringArgument:
		return parser.ObjectName{Parts: []parser.Identifier{{Text: o.Value, Pos: o.ValuePos}}}, nil
	case parser.OperatorOptionNoneArgument:
		return parser.ObjectName{Parts: []parser.Identifier{{Text: "none", Pos: o.ValuePos}}}, nil
	}
	return parser.ObjectName{}, sqlError("42601", "argument of "+o.Name+" must be a name")
}

func OperatorOptionType(o parser.OperatorOption) (parser.TypeName, error) {
	if !o.HasValue {
		return parser.TypeName{}, sqlError("42601", o.Name+" requires a parameter")
	}
	switch o.Kind {
	case parser.OperatorOptionTypeArgument:
		return o.Type, nil
	case parser.OperatorOptionStringArgument, parser.OperatorOptionNoneArgument:
		name := o.Value
		if o.Kind == parser.OperatorOptionNoneArgument {
			name = "none"
		}
		return parser.TypeName{Name: name, Quoted: true, Parts: []parser.Identifier{{Text: name, Pos: o.ValuePos}}, Pos: o.ValuePos}, nil
	}
	return parser.TypeName{}, sqlError("42601", "argument of "+o.Name+" must be a type name")
}

// ALTER's unquoted NONE is a NULL argument; quoted/literal none remains a name.
func OperatorAlterOption(o parser.OperatorOption) parser.OperatorOption {
	if o.Kind == parser.OperatorOptionNoneArgument {
		o.HasValue, o.Kind = false, parser.OperatorOptionNoArgument
	}
	return o
}

func OperatorInputName(v catalog.Read, left, right ids.OID, name parser.ObjectName) string {
	n := objectNameText(name)
	if left != 0 {
		t, _ := v.LookupType(sqltype.ID(left))
		n = t.DiagnosticName() + " " + n
	}
	if right != 0 {
		t, _ := v.LookupType(sqltype.ID(right))
		n += " " + t.DiagnosticName()
	}
	return n
}

// Both join signatures are legal, but neither wins over the other when both
// exist. Inputs are lookup identities, not callable provider declarations.
func OperatorEstimatorVariant(name parser.ObjectName, five catalog.RoutineIdentity, fiveCount int, four catalog.RoutineIdentity, fourCount int) (catalog.RoutineIdentity, int, error) {
	if fiveCount != 0 && fourCount != 0 {
		return catalog.RoutineIdentity{}, 0, sqlError("42725", "join estimator function "+objectNameText(name)+" has multiple matches")
	}
	if fiveCount != 0 {
		return five, fiveCount, nil
	}
	return four, fourCount, nil
}

func ValidateOperatorEstimatorMetadata(name parser.ObjectName, join bool, result ids.OID, builtin, superuser bool) error {
	if !builtin && !superuser {
		return sqlError("42501", "must be superuser to specify a non-built-in selectivity estimation function")
	}
	if result == 0 {
		return sqlError("0A000", "operator estimator return metadata is unavailable")
	}
	if result != 701 {
		kind := "restriction"
		if join {
			kind = "join"
		}
		return sqlError("42P17", kind+" estimator function "+objectNameText(name)+" must return type float8")
	}
	return nil
}

func OperatorOptionError(name string) error {
	return sqlError("42P13", fmt.Sprintf(`operator attribute "%s" cannot be changed if it has already been set`, name))
}

func ValidateOperatorOptions(d *catalog.Operator, comm, neg parser.ObjectName) error {
	// OperatorValidateParams completes binary eligibility before checking
	// Boolean results, even when an earlier Boolean-only option is also set.
	if d.Left == 0 {
		if len(comm.Parts) > 0 {
			return sqlError("42P13", "only binary operators can have commutators")
		}
		if d.Join != 0 {
			return sqlError("42P13", "only binary operators can have join selectivity")
		}
		if d.Merge {
			return sqlError("42P13", "only binary operators can merge join")
		}
		if d.Hash {
			return sqlError("42P13", "only binary operators can hash")
		}
	}
	if d.Result != ids.OID(sqltype.Bool) {
		if len(neg.Parts) > 0 {
			return sqlError("42P13", "only boolean operators can have negators")
		}
		if d.Restriction != 0 {
			return sqlError("42P13", "only boolean operators can have restriction selectivity")
		}
		if d.Join != 0 {
			return sqlError("42P13", "only boolean operators can have join selectivity")
		}
		if d.Merge {
			return sqlError("42P13", "only boolean operators can merge join")
		}
		if d.Hash {
			return sqlError("42P13", "only boolean operators can hash")
		}
	}
	return nil
}

func OperatorBoolean(o parser.OperatorOption) (bool, error) {
	if !o.HasValue {
		return true, nil
	}
	value := o.Value
	switch o.Kind {
	case parser.OperatorOptionIntegerArgument:
		// Integer nodes use their value, including signed zero and radix input.
		digits := strings.ReplaceAll(value, "_", "")
		sign := int64(1)
		if len(digits) > 0 && (digits[0] == '+' || digits[0] == '-') {
			if digits[0] == '-' {
				sign = -1
			}
			digits = digits[1:]
		}
		base := 10
		if len(digits) > 2 && digits[0] == '0' {
			switch digits[1] {
			case 'x', 'X':
				base, digits = 16, digits[2:]
			case 'o', 'O':
				base, digits = 8, digits[2:]
			case 'b', 'B':
				base, digits = 2, digits[2:]
			}
		}
		n, err := strconv.ParseInt(digits, base, 32)
		if err == nil && (n*sign == 0 || n*sign == 1) {
			return n*sign == 1, nil
		}
	case parser.OperatorOptionTypeArgument:
		// A complete decorated or qualified TypeName cannot equal a Boolean
		// word; never accept just its last component.
		if o.Type.Schema == nil && len(o.Type.Parts) <= 1 && !o.Type.Setof && len(o.Type.Modifiers) == 0 && len(o.Type.ArrayBounds) == 0 {
			value = o.Type.Name
		}
		fallthrough
	case parser.OperatorOptionStringArgument, parser.OperatorOptionFloatArgument:
		switch strings.ToLower(value) {
		case "true", "on":
			return true, nil
		case "false", "off":
			return false, nil
		}
	}
	return false, sqlError("42601", o.Name+" requires a Boolean value")
}

func CheckOperatorLinkReplacement(d, peer *catalog.Operator, negator bool) error {
	if peer == nil {
		return nil
	}
	prior := d.Commutator
	if negator {
		prior = d.Negator
	}
	if prior != 0 && prior != peer.Address().ObjectID {
		attribute := "commutator"
		if negator {
			attribute = "negator"
		}
		return OperatorOptionError(attribute)
	}
	return nil
}
