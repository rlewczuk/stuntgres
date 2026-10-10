package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
)

// Source provenance is CREATE-local, never part of the persistent constraint.
// Ordinary LIKE index copies retain their existing independent copy semantics.
type createUniqueConstraint struct {
	uniqueConstraint
	source       *relationDescriptor
	like         *createLikeInput
	copyComments bool
}

func bindCreateUniques(s *parser.CreateTable, index map[string]int, likes []createLikeInput, catalog CatalogRead) ([]createUniqueConstraint, error) {
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	var declarations []createUniqueConstraint
	primary := -1
	bind := func(raw *parser.UniqueConstraint, column *parser.Column) error {
		if raw.Primary && primary >= 0 {
			return insertError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, table), raw.Pos)
		}
		width := len(raw.Columns)
		if column != nil {
			width = 1
		}
		if width == 0 || width > 32 {
			return sqlError("54011", "cannot use more than 32 columns in an index")
		}
		c := createUniqueConstraint{uniqueConstraint: uniqueConstraint{Name: raw.Name.Text, Primary: raw.Primary, NullsNotDistinct: raw.NullsNotDistinct, Attributes: make([]int32, width)}}
		for i := range c.Attributes {
			name := ""
			if column != nil {
				name = column.Name
			} else {
				name = raw.Columns[i].Text
			}
			slot, ok := index[name]
			if !ok {
				return insertError("42703", fmt.Sprintf(`column "%s" named in key does not exist`, name), raw.Pos)
			}
			attribute := int32(slot + 1)
			if slices.Contains(c.Attributes[:i], attribute) {
				kind := "unique"
				if raw.Primary {
					kind = "primary key"
				}
				return insertError("42701", fmt.Sprintf(`column "%s" appears twice in %s constraint`, name, kind), raw.Pos)
			}
			c.Attributes[i] = attribute
		}
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
		if in.options&likeIndexes == 0 {
			continue
		}
		for _, old := range in.source.uniqueConstraints {
			if !old.Primary {
				continue
			}
			if primary >= 0 {
				return nil, sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, table))
			}
			source, ok := catalog.state.descriptors[old.Index].(*relationDescriptor)
			if !ok || source.sqlIndex == nil {
				return nil, fmt.Errorf("stuntgres: missing LIKE primary backing")
			}
			c := createUniqueConstraint{uniqueConstraint: uniqueConstraint{Primary: true, Attributes: make([]int32, len(old.Attributes))}, source: source, like: in, copyComments: in.options&likeComments != 0}
			for i, attribute := range old.Attributes {
				slot := slices.Index(in.source.attributes, attribute)
				if slot < 0 {
					return nil, fmt.Errorf("stuntgres: missing LIKE primary attribute")
				}
				c.Attributes[i] = int32(in.offset + slot + 1)
			}
			retained = append(retained, createUniqueConstraint{})
			copy(retained[1:], retained[:len(retained)-1])
			retained[0] = c
			primary = 0
		}
	}
	return retained, nil
}
