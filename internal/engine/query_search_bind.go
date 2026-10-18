package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSearchShape(d *cteDefinition) error {
	s := d.search
	s.slot = d.userWidth
	s.depth = b.typ(sqltype.Int8)
	s.keys = make([]int, len(s.syntax.Keys))
	offset := 0
	if s.syntax.Breadth {
		offset = 1
	}
	members := make([]sqltype.CompositeMember, len(s.keys)+offset)
	if offset != 0 {
		members[0] = sqltype.CompositeMember{Attribute: 1, Name: "*DEPTH*", Usage: s.depth.Usage()}
	}
	for i, key := range s.syntax.Keys {
		slot := -1
		for j, column := range d.schema[:d.userWidth] {
			if column.Name == key.Text {
				slot = j
				break
			}
		}
		if slot < 0 && s.shapeError == nil {
			s.shapeError = expr.ErrorAt("42601", fmt.Sprintf(`search column "%s" not in WITH query column list`, key.Text), s.syntax.Pos)
		}
		for _, prior := range s.keys[:i] {
			if slot >= 0 && prior == slot && s.shapeError == nil {
				s.shapeError = expr.ErrorAt("42701", fmt.Sprintf(`search column "%s" specified more than once`, key.Text), s.syntax.Pos)
			}
		}
		s.keys[i] = slot
		usage := b.typ(sqltype.Text).Usage()
		if slot >= 0 {
			usage = d.schema[slot].Usage
		}
		members[i+offset] = sqltype.CompositeMember{Attribute: int32(i + offset + 1), Name: key.Text, Usage: usage}
	}
	var err error
	s.record, err = sqltype.RecordShape(members)
	if err != nil {
		return err
	}
	s.result = s.record
	if !s.syntax.Breadth {
		s.result, err = sqltype.Array(2287, "_record", s.record)
		if err != nil {
			return err
		}
	}
	d.schema = append(d.schema, ColumnDescriptor{Name: s.syntax.Sequence.Text, Usage: s.result.Usage()})
	return nil
}

func searchNameError(d *cteDefinition) error {
	s := d.search
	if s.shapeError != nil {
		return s.shapeError
	}
	for _, column := range d.schema[:d.userWidth] {
		if column.Name == s.syntax.Sequence.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`search sequence column name "%s" already used in WITH query column list`, column.Name), s.syntax.Pos)
		}
	}
	return nil
}

func (b *expressionBinder) bindSearchIdentity(d *cteDefinition) error {
	s := d.search
	s.grouping = make([]sqltype.Grouping, s.record.CompositeLen())
	for i := range s.grouping {
		var err error
		s.grouping[i], err = sqltype.BindGrouping(s.record.CompositeMember(i).Usage)
		if err != nil {
			return err
		}
	}
	return nil
}
