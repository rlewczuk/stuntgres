package index

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Eligibility is captured once from the actual ordered provider input. TYPE
// reminting remains the existing stage owner's separate metadata effect.
func CaptureUsingIndexDefinition(reader catalog.DefinitionReader, parent *catalog.Relation, raw *parser.UsingIndexConstraint) (UsingIndexAttachment, error) {
	address := reader.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.Index.Text})
	descriptor, _ := reader.Descriptor(address)
	if descriptor == nil {
		return UsingIndexAttachment{}, expr.ErrorAt("42704", fmt.Sprintf(`index "%s" does not exist`, raw.Index.Text), raw.Pos)
	}
	index, ok := descriptor.(*catalog.Relation)
	if !ok || index.SQLIndex == nil {
		return UsingIndexAttachment{}, sqlError("42809", fmt.Sprintf(`"%s" is not an index`, raw.Index.Text))
	}
	attributes, err := UsingIndexEligibility(parent, index, raw)
	if err != nil {
		return UsingIndexAttachment{}, err
	}
	return UsingIndexAttachment{Syntax: raw, Index: index, Attributes: attributes}, nil
}

func CaptureUsingIndexDefinitions(reader catalog.DefinitionReader, parent *catalog.Relation, actions []parser.AlterTableAction) ([]UsingIndexAttachment, error) {
	var captured []UsingIndexAttachment
	for _, action := range actions {
		if action.Action == parser.AddUsingIndex {
			attachment, err := CaptureUsingIndexDefinition(reader, parent, action.UsingIndex)
			if err != nil {
				return nil, err
			}
			captured = append(captured, attachment)
		}
	}
	return captured, nil
}

type UsingIndexPreparation struct {
	Parent, Next       *catalog.Relation
	Index, Replacement *catalog.Relation
	Notice             sqlstate.Error
	Err                error
}
