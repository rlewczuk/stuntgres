package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Rewind changes demand in the retained producer tree, never its binding or frame.
func (c *sqlCursor) rewind() error {
	if c.ordinalPosition == 0 && !c.atEnd {
		return nil
	}
	if !c.scroll {
		return forwardSQLCursorError()
	}
	if c.stage != nil {
		c.stage.rewind()
	}
	c.ordinalPosition, c.atEnd, c.position = 0, false, cursorBefore
	c.currentSource, c.currentIndex = nil, 0
	return nil
}

func (c *sqlCursor) run(backward bool, count int64, all bool, rows *finalRows, s *StatementScope) (int64, error) {
	if backward && !c.scroll {
		return 0, forwardSQLCursorError()
	}
	var moved int64
	for all || moved < count {
		if backward && c.ordinalPosition == 0 || !backward && c.atEnd {
			break
		}
		if !c.promoted {
			if err := c.openProducer(s); err != nil {
				return 0, err
			}
		}
		ordinal := c.ordinalPosition
		if backward {
			ordinal--
			if !c.atEnd {
				ordinal--
			}
			if ordinal < 0 {
				c.ordinalPosition, c.position, c.atEnd = 0, cursorBefore, false
				c.currentSource, c.currentIndex = nil, 0
				break
			}
		}
		var dst []sqltype.Value
		start := 0
		if rows != nil {
			width := len(c.schema)
			max := int(^uint(0) >> 1)
			if rows.count == max || len(rows.values) > max-width {
				return 0, fmt.Errorf("stuntgres: cursor batch cardinality exhausted")
			}
			start = len(rows.values)
			if !c.promoted || backward {
				rows.values = slices.Grow(rows.values, width)
				rows.values = rows.values[:start+width]
				dst = rows.values[start:]
			}
		}
		var present bool
		var err error
		if c.promoted {
			index := ordinal - c.storeOffset
			present = index >= 0 && index < int64(c.count)
			if present && rows != nil {
				width := len(c.schema)
				if backward {
					copy(dst, c.values[int(index)*width:(int(index)+1)*width])
				} else {
					// Forward batches borrow immutable contiguous store values.
					first, end := (int(index)-rows.count)*width, (int(index)+1)*width
					rows.values = c.values[first:end:end]
				}
			}
		} else {
			present, err = c.stage.projectOrdinal(c.authority, int(ordinal), dst)
		}
		if err != nil {
			return 0, err
		}
		if !present {
			if rows != nil {
				rows.values = rows.values[:start]
			}
			if !c.promoted && !c.scroll {
				if err := c.stage.Close(); err != nil {
					return 0, err
				}
			}
			c.position, c.atEnd = cursorAfter, true
			c.currentSource, c.currentIndex = nil, 0
			break
		}
		moved++
		if rows != nil {
			rows.count++
		}
		c.ordinalPosition, c.atEnd, c.position = ordinal+1, false, cursorOn
		if !c.promoted {
			c.currentSource, c.currentIndex = c.stage.source, c.stage.index(int(ordinal))
		}
	}
	return moved, nil
}

func (c *sqlCursor) motion(m *parser.FetchCursor, rows *finalRows, s *StatementScope) (int64, error) {
	count := m.Count
	direction := m.Direction
	if direction == parser.CursorRelative && count != 0 {
		backward := count < 0
		if backward {
			count = -count
		}
		n, err := c.run(backward, count-1, false, nil, s)
		if err != nil {
			return 0, err
		}
		if n != count-1 {
			return 0, nil
		}
		return c.run(backward, 1, false, rows, s)
	}
	if direction == parser.CursorAbsolute {
		if count == 0 {
			return 0, c.rewind()
		}
		if count < 0 {
			if _, err := c.run(false, 0, true, nil, s); err != nil {
				return 0, err
			}
			if _, err := c.run(true, -count-1, false, nil, s); err != nil {
				return 0, err
			}
			return c.run(true, 1, false, rows, s)
		}
		if count-1 <= c.ordinalPosition/2 {
			if err := c.rewind(); err != nil {
				return 0, err
			}
			if _, err := c.run(false, count-1, false, nil, s); err != nil {
				return 0, err
			}
		} else {
			delta := count - c.ordinalPosition - 1
			if c.atEnd {
				delta--
			}
			if delta != 0 {
				backward := delta < 0
				if backward {
					delta = -delta
				}
				if _, err := c.run(backward, delta, false, nil, s); err != nil {
					return 0, err
				}
			}
		}
		return c.run(false, 1, false, rows, s)
	}
	backward := direction == parser.CursorBackward
	if count < 0 {
		backward, count = !backward, -count
	}
	if m.Move && backward && m.All {
		n := c.ordinalPosition
		if c.position == cursorOn {
			n--
		}
		return n, c.rewind()
	}
	if !m.All && count == 0 {
		if m.Move {
			if c.position == cursorOn {
				return 1, nil
			}
			return 0, nil
		}
		if backward && !c.scroll {
			return 0, forwardSQLCursorError()
		}
		if c.position != cursorOn {
			return 0, nil
		}
		if _, err := c.run(true, 1, false, nil, s); err != nil {
			return 0, err
		}
		return c.run(false, 1, false, rows, s)
	}
	return c.run(backward, count, m.All, rows, s)
}
