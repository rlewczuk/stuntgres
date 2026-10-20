package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindSearchShape(d *cteDefinition) error {
	s := d.Search
	s.Slot = d.UserWidth
	s.Depth = b.typ(sqltype.Int8)
	s.Keys = make([]int, len(s.Syntax.Keys))
	offset := 0
	if s.Syntax.Breadth {
		offset = 1
	}
	members := make([]sqltype.CompositeMember, len(s.Keys)+offset)
	if offset != 0 {
		members[0] = sqltype.CompositeMember{Attribute: 1, Name: "*DEPTH*", Usage: s.Depth.Usage()}
	}
	for i, key := range s.Syntax.Keys {
		slot := -1
		for j, column := range d.Schema[:d.UserWidth] {
			if column.Name == key.Text {
				slot = j
				break
			}
		}
		if slot < 0 && s.ShapeError == nil {
			s.ShapeError = expr.ErrorAt("42601", fmt.Sprintf(`search column "%s" not in WITH query column list`, key.Text), s.Syntax.Pos)
		}
		for _, prior := range s.Keys[:i] {
			if slot >= 0 && prior == slot && s.ShapeError == nil {
				s.ShapeError = expr.ErrorAt("42701", fmt.Sprintf(`search column "%s" specified more than once`, key.Text), s.Syntax.Pos)
			}
		}
		s.Keys[i] = slot
		usage := b.typ(sqltype.Text).Usage()
		if slot >= 0 {
			usage = d.Schema[slot].Usage
		}
		members[i+offset] = sqltype.CompositeMember{Attribute: int32(i + offset + 1), Name: key.Text, Usage: usage}
	}
	var err error
	s.Record, err = sqltype.RecordShape(members)
	if err != nil {
		return err
	}
	s.Result = s.Record
	if !s.Syntax.Breadth {
		s.Result, err = sqltype.Array(2287, "_record", s.Record)
		if err != nil {
			return err
		}
	}
	d.Schema = append(d.Schema, catalog.Column{Name: s.Syntax.Sequence.Text, Usage: s.Result.Usage()})
	return nil
}

func searchNameError(d *cteDefinition) error {
	s := d.Search
	if s.ShapeError != nil {
		return s.ShapeError
	}
	for _, column := range d.Schema[:d.UserWidth] {
		if column.Name == s.Syntax.Sequence.Text {
			return expr.ErrorAt("42601", fmt.Sprintf(`search sequence column name "%s" already used in WITH query column list`, column.Name), s.Syntax.Pos)
		}
	}
	return nil
}

func (b *expressionBinder) bindSearchIdentity(d *cteDefinition) error {
	s := d.Search
	s.Grouping = make([]sqltype.Grouping, s.Record.CompositeLen())
	for i := range s.Grouping {
		var err error
		s.Grouping[i], err = sqltype.BindGrouping(s.Record.CompositeMember(i).Usage)
		if err != nil {
			return err
		}
	}
	return nil
}
