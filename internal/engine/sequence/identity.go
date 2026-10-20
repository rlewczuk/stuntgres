package sequence

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// IdentityTypeOption is the AS option an identity sequence takes from its
// column type, for identity creation and ALTER COLUMN TYPE.
func IdentityTypeOption(id sqltype.ID) (parser.SequenceOption, error) {
	if id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
		return parser.SequenceOption{}, OptionError("identity column type must be smallint, integer, or bigint")
	}
	_, _, name := TypeLimits(id)
	return parser.SequenceOption{Name: "as", Type: parser.TypeName{Name: name}}, nil
}

// IdentityOptions are the options of GENERATED ... AS IDENTITY split into
// the sequence options, the persistence and the explicit SEQUENCE NAME.
type IdentityOptions struct {
	// Raw is the AS option followed by the remaining sequence options.
	Raw         []parser.SequenceOption
	Persistence string
	// Name is the explicit SEQUENCE NAME; it has no parts when absent.
	Name parser.ObjectName

	persistenceSet bool
	persistencePos parser.Position
}

// SplitIdentityOptions splits options after the type's AS option, rejecting
// a repeated LOGGED/UNLOGGED or SEQUENCE NAME at its position.
func SplitIdentityOptions(as parser.SequenceOption, options []parser.SequenceOption) (IdentityOptions, error) {
	split := IdentityOptions{Raw: []parser.SequenceOption{as}, Persistence: "logged"}
	for _, option := range options {
		if option.Name == "logged" || option.Name == "unlogged" {
			if split.persistenceSet {
				return IdentityOptions{}, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
			}
			split.Persistence, split.persistenceSet, split.persistencePos = option.Name, true, option.Pos
			continue
		}
		if option.Name == "name" {
			if len(split.Name.Parts) != 0 {
				return IdentityOptions{}, expr.ErrorAt("42601", "conflicting or redundant options", option.Pos)
			}
			split.Name = option.Owned
			continue
		}
		split.Raw = append(split.Raw, option)
	}
	return split, nil
}

// CheckTemporary rejects an explicit LOGGED or UNLOGGED of a TEMP table's
// identity, at the option's position. The engine calls it after binding the
// options, which take precedence.
func (o IdentityOptions) CheckTemporary() error {
	if o.persistenceSet {
		return expr.ErrorAt("42P16", "cannot set logged status of a temporary sequence", o.persistencePos)
	}
	return nil
}

// CheckIdentitySetOptions rejects the options ALTER COLUMN ... SET does not
// accept for an identity sequence.
func CheckIdentitySetOptions(options []parser.SequenceOption) error {
	for _, option := range options {
		if option.Name == "logged" || option.Name == "unlogged" {
			return sqlError("XX000", fmt.Sprintf(`option "%s" not recognized`, option.Name))
		}
		if option.Name == "as" || option.Name == "owned" {
			return expr.ErrorAt("42601", fmt.Sprintf(`sequence option "%s" not supported here`, option.Name), option.Pos)
		}
	}
	return nil
}

// SerialOptions are the options of a serial column's sequence of type id.
func SerialOptions(id sqltype.ID) (catalog.SequenceOptions, error) {
	_, _, typeName := TypeLimits(id)
	options, _, err := BindOptions(nil, storage.SequenceGenerator{}, []parser.SequenceOption{{Name: "as", Type: parser.TypeName{Name: typeName}}})
	return options, err
}

// SerialDefault is the synthetic pg_catalog.nextval default of a serial
// column whose sequence is name in namespace; the engine binds it with the
// same closed default binder as Restore.
func SerialDefault(namespace, name string) parser.Expr {
	return parser.CallExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "nextval"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: catalog.QuoteIdentifier(namespace) + "." + catalog.QuoteIdentifier(name)}}}}
}

// CheckRestoreIdentityConfiguration admits only the parameter-free
// configuration of an existing identity sequence relation (nil when the
// name resolved to nothing): Restore reconstructs, it replays no ALTER
// history.
func CheckRestoreIdentityConfiguration(relation *catalog.Relation, syntax *parser.AlterSequence) error {
	if relation == nil || relation.Sequence == nil || !relation.Sequence.Identity {
		return sqlError("0A000", "restore accepts only generated sequence ownership or identity configuration")
	}
	if syntax.Action == "logged" || syntax.Action == "unlogged" {
		return nil
	}
	if syntax.Action != "" || len(syntax.Options) == 0 {
		return sqlError("0A000", "restore accepts only generated identity configuration")
	}
	for _, option := range syntax.Options {
		switch option.Name {
		case "as", "increment", "min", "max", "start", "cache", "cycle", "restart":
		default:
			return sqlError("0A000", "restore accepts only generated identity configuration")
		}
	}
	return nil
}
