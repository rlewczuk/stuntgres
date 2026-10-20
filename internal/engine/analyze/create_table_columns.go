package analyze

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
)

// LIKE contributes copied identities at its original table-element position.
type LikeConstraint struct {
	Element int
	catalog.NotNullConstraint
}

func BindCreateColumnDeclaration(column *parser.Column, table string) (ColumnDeclaration, error) {
	var declaration ColumnDeclaration
	declaration.Serial = sequence.SerialTypeName(column.TypeName) != ""
	seen := declaration.Serial
	declaration.ColumnNotNull = declaration.Serial
	for _, constraint := range column.Constraints {
		if constraint.Check != nil || constraint.ForeignKey != nil {
			continue
		}
		if constraint.Unique != nil {
			if !constraint.Unique.Primary {
				continue
			}
			// PK implies local NN, but its name belongs only to the index
			// constraint. An explicit NN name remains independent.
			constraint.NotNull = true
			constraint.Name = parser.Identifier{}
		}
		if constraint.Generation != nil {
			generation := constraint.Generation
			if generation.Mode != parser.IdentityAlways {
				return declaration, insertError("42601", "for a generated column, GENERATED ALWAYS must be specified", generation.ModePos)
			}
			conflict := ""
			if declaration.Generation != nil {
				conflict = "multiple generation clauses"
			}
			if declaration.DefaultExpr != nil || declaration.Serial {
				conflict = "both default and generation expression"
			}
			if declaration.Identity != nil {
				conflict = "both identity and generation expression"
			}
			if conflict != "" {
				return declaration, insertError("42601", fmt.Sprintf(`%s specified for column "%s" of table "%s"`, conflict, column.Name, table), constraint.Pos)
			}
			declaration.Generation = generation
			continue
		}
		if constraint.Identity != nil {
			if declaration.Generation != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both identity and generation expression specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.Identity != nil {
				return declaration, insertError("42601", fmt.Sprintf(`multiple identity specifications for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.DefaultExpr != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			declaration.Identity = constraint.Identity
			constraint.NotNull = true
		}
		if constraint.Default != nil {
			if declaration.Generation != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and generation expression specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.Identity != nil {
				return declaration, insertError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			if declaration.DefaultExpr != nil {
				return declaration, insertError("42601", fmt.Sprintf(`multiple default values specified for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
			}
			declaration.DefaultExpr = constraint.Default
			continue
		}
		if seen && declaration.ColumnNotNull != constraint.NotNull {
			return declaration, insertError("42601", fmt.Sprintf(`conflicting NULL/NOT NULL declarations for column "%s" of table "%s"`, column.Name, table), constraint.Pos)
		}
		name := constraint.Name.Text
		if constraint.NotNull && declaration.ColumnName != "" && name != "" && declaration.ColumnName != name {
			return declaration, sqlError("XX000", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, declaration.ColumnName, name))
		}
		seen, declaration.ColumnNotNull = true, constraint.NotNull
		if constraint.NotNull && name != "" {
			declaration.ColumnName = name
		}
	}
	if declaration.Serial {
		if declaration.DefaultExpr != nil {
			return declaration, sqlError("42601", fmt.Sprintf(`multiple default values specified for column "%s" of table "%s"`, column.Name, table))
		}
		if declaration.Identity != nil {
			return declaration, sqlError("42601", fmt.Sprintf(`both default and identity specified for column "%s" of table "%s"`, column.Name, table))
		}
	}
	return declaration, nil
}

func BindCreateColumnIndex(columns []catalog.Column) (map[string]int, error) {
	index := make(map[string]int, len(columns))
	for i, c := range columns {
		if _, exists := index[c.Name]; exists {
			return nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, c.Name))
		}
		if catalog.IsSystemColumn(c.Name) {
			return nil, sqlError("42701", fmt.Sprintf("column name %q conflicts with a system column name", c.Name))
		}
		index[c.Name] = i
	}
	return index, nil
}

func BindCreateNotNull(s *parser.CreateTable, columns []catalog.Column, declarations []ColumnDeclaration, inherited []LikeConstraint, uniques []UniqueConstraint, index map[string]int) ([]catalog.NotNullConstraint, error) {
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	// Retain first occurrence order while merging later declarations' names.
	var constraints []catalog.NotNullConstraint
	merge := func(slot int, name string) error {
		old := &declarations[slot]
		if old.Name != "" && name != "" && old.Name != name {
			return sqlError("42601", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, old.Name, name))
		}
		if !old.NotNull {
			constraints = append(constraints, catalog.NotNullConstraint{Attribute: int32(slot + 1)})
		}
		old.NotNull = true
		if name != "" {
			old.Name = name
		}
		return nil
	}
	inheritedIndex := 0
	for elementIndex, element := range s.Elements {
		if c := element.Column; c != nil {
			slot := index[c.Name]
			if declarations[slot].ColumnNotNull {
				if err := merge(slot, declarations[slot].ColumnName); err != nil {
					return nil, err
				}
			}
		} else if constraint := element.NotNull; constraint != nil {
			if catalog.IsSystemColumn(constraint.Column.Text) {
				return nil, sqlError("0A000", fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, constraint.Column.Text))
			}
			slot, exists := index[constraint.Column.Text]
			if !exists {
				return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, constraint.Column.Text, table))
			}
			if err := merge(slot, constraint.Name.Text); err != nil {
				return nil, err
			}
		} else {
			for inheritedIndex < len(inherited) && inherited[inheritedIndex].Element == elementIndex {
				constraint := inherited[inheritedIndex]
				if err := merge(int(constraint.Attribute-1), constraint.Name); err != nil {
					return nil, err
				}
				inheritedIndex++
			}
		}
	}
	// Table-form PK adds NN only after the original column/table/LIKE NN
	// merge. Explicit NULL in a column therefore does not conflict here.
	for _, constraint := range uniques {
		if !constraint.Primary {
			continue
		}
		for _, attribute := range constraint.Attributes {
			if err := merge(int(attribute-1), ""); err != nil {
				return nil, err
			}
		}
	}
	for i := range declarations {
		columns[i].Nullable = !declarations[i].NotNull
	}
	for i := range constraints {
		constraints[i].Name = declarations[constraints[i].Attribute-1].Name
	}
	return constraints, nil
}

func BindCreateUniques(s *parser.CreateTable, index map[string]int, likes []LikeInput, view catalog.Read) ([]UniqueConstraint, error) {
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	var declarations []UniqueConstraint
	primary := -1
	bind := func(raw *parser.UniqueConstraint, column *parser.Column) error {
		if raw.Primary && primary >= 0 {
			return insertError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, table), raw.Pos)
		}
		definition, err := BindUniqueAttributes(raw, column, index, nil)
		if err != nil {
			return err
		}
		c := UniqueConstraint{UniqueConstraint: definition}
		if raw.Primary {
			primary = len(declarations)
		}
		declarations = append(declarations, c)
		return nil
	}
	for _, element := range s.Elements {
		if element.Unique != nil {
			if err := bind(element.Unique, nil); err != nil {
				return nil, err
			}
		}
		if column := element.Column; column != nil {
			for _, constraint := range column.Constraints {
				if constraint.Unique != nil {
					if err := bind(constraint.Unique, column); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if primary > 0 {
		c := declarations[primary]
		copy(declarations[1:primary+1], declarations[:primary])
		declarations[0] = c
	}
	retained := declarations[:0]
	for _, c := range declarations {
		redundant := false
		for i := range retained {
			old := &retained[i]
			if old.NullsNotDistinct == c.NullsNotDistinct && slices.Equal(old.Attributes, c.Attributes) {
				if old.Name == "" {
					old.Name = c.Name
				}
				redundant = true
				break
			}
		}
		if !redundant {
			retained = append(retained, c)
		}
	}
	// A copied PK is a separately owned index declaration, not an explicit
	// redundancy candidate. It is retained first, even before explicit UNIQUE.
	for i := range likes {
		in := &likes[i]
		if in.Options&LikeIndexes == 0 {
			continue
		}
		for _, old := range in.Source.Constraints.Unique {
			if !old.Primary {
				continue
			}
			if primary >= 0 {
				return nil, sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, table))
			}
			source, ok := view.Relation(old.Index)
			if !ok || source.SQLIndex == nil {
				return nil, fmt.Errorf("stuntgres: missing LIKE primary backing")
			}
			c := UniqueConstraint{UniqueConstraint: catalog.UniqueConstraint{Primary: true, Attributes: make([]int32, len(old.Attributes))}, Source: source, Like: in, CopyComments: in.Options&LikeComments != 0}
			for i, attribute := range old.Attributes {
				slot := slices.Index(in.Source.Attributes, attribute)
				if slot < 0 {
					return nil, fmt.Errorf("stuntgres: missing LIKE primary attribute")
				}
				c.Attributes[i] = int32(in.Offset + slot + 1)
			}
			retained = append(retained, UniqueConstraint{})
			copy(retained[1:], retained[:len(retained)-1])
			retained[0] = c
			primary = 0
		}
	}
	return retained, nil
}

// CREATE's sequential attributes and ALTER's sparse stable attributes use the
// same key-list rules, without synthesizing a CREATE statement or slot identity.
func BindUniqueAttributes(raw *parser.UniqueConstraint, column *parser.Column, index map[string]int, attributes []int32) (catalog.UniqueConstraint, error) {
	width := len(raw.Columns)
	if column != nil {
		width = 1
	}
	if width == 0 || width > 32 {
		return catalog.UniqueConstraint{}, sqlError("54011", "cannot use more than 32 columns in an index")
	}
	c := catalog.UniqueConstraint{Name: raw.Name.Text, Primary: raw.Primary, NullsNotDistinct: raw.NullsNotDistinct, Attributes: make([]int32, width)}
	for i := range c.Attributes {
		name := ""
		if column != nil {
			name = column.Name
		} else {
			name = raw.Columns[i].Text
		}
		slot, ok := index[name]
		if !ok {
			return catalog.UniqueConstraint{}, insertError("42703", fmt.Sprintf(`column "%s" named in key does not exist`, name), raw.Pos)
		}
		attribute := int32(slot + 1)
		if attributes != nil {
			attribute = attributes[slot]
		}
		if slices.Contains(c.Attributes[:i], attribute) {
			kind := "unique"
			if raw.Primary {
				kind = "primary key"
			}
			return catalog.UniqueConstraint{}, insertError("42701", fmt.Sprintf(`column "%s" appears twice in %s constraint`, name, kind), raw.Pos)
		}
		c.Attributes[i] = attribute
	}
	return c, nil
}

func TableLikeOptions(options []parser.LikeOption) LikeMask {
	var mask LikeMask
	for _, option := range options {
		var bit LikeMask
		switch strings.ToLower(option.Name) {
		case "comments":
			bit = LikeComments
		case "compression":
			bit = LikeCompression
		case "constraints":
			bit = LikeConstraints
		case "defaults":
			bit = LikeDefaults
		case "generated":
			bit = LikeGenerated
		case "identity":
			bit = LikeIdentity
		case "indexes":
			bit = LikeIndexes
		case "statistics":
			bit = LikeStatistics
		case "storage":
			bit = LikeStorage
		case "all":
			bit = LikeAll
		}
		if option.Including {
			mask |= bit
		} else {
			mask &^= bit
		}
	}
	return mask
}
