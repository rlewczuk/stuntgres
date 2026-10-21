package regobj

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ClassNumeric(text string, typ *sqltype.Descriptor) (uint32, bool, error) {
	input := text
	if input == "-" {
		input = "0"
	}
	if input == "" {
		return 0, false, nil
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			return 0, false, nil
		}
	}
	value, err := typ.Input(context.Background(), input, -1)
	if err != nil {
		return 0, true, err
	}
	n, err := value.OID()
	return n, true, err
}

func ClassQualifiedName(name parser.ObjectName, text, database string) (parser.ObjectName, error) {
	if len(name.Parts) > 3 {
		return parser.ObjectName{}, sqlError("42601", "improper relation name (too many dotted names): "+text)
	}
	if len(name.Parts) == 3 {
		if name.Parts[0].Text != database {
			return parser.ObjectName{}, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, catalog.ObjectNameText(name)))
		}
		name.Parts = name.Parts[1:]
	}
	return name, nil
}
