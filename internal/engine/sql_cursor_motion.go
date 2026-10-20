package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Rewind changes demand in the retained producer tree, never its binding or frame.
func (c *sqlCursor) rewind(ctx context.Context) error {
	if c.ordinalPosition == 0 && !c.atEnd {
		return nil
	}
	if !c.scroll {
		return forwardSQLCursorError()
	}
	if c.stage != nil {
		if _, err := c.stage.Rewind(ctx); err != nil {
			return err
		}
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
			if rows.Count == max || len(rows.Values) > max-width {
				return 0, fmt.Errorf("stuntgres: cursor batch cardinality exhausted")
			}
			start = len(rows.Values)
			if !c.promoted || backward {
				rows.Values = slices.Grow(rows.Values, width)
				rows.Values = rows.Values[:start+width]
				dst = rows.Values[start:]
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
					first, end := (int(index)-rows.Count)*width, (int(index)+1)*width
					rows.Values = c.values[first:end:end]
				}
			}
		} else {
			present, err = c.stage.ProjectOrdinal(s.op.Context(), int(ordinal), dst)
		}
		if err != nil {
			return 0, err
		}
		if !present {
			if rows != nil {
				rows.Values = rows.Values[:start]
			}
			if !c.promoted && !c.scroll {
				if err := c.stage.Close(); err != nil {
					return 0, err
				}
				if err := s.finishForeignOwner(c.foreignOwner, nil); err != nil {
					return 0, err
				}
			}
			c.position, c.atEnd = cursorAfter, true
			c.currentSource, c.currentIndex = nil, 0
			break
		}
		moved++
		if rows != nil {
			rows.Count++
		}
		c.ordinalPosition, c.atEnd, c.position = ordinal+1, false, cursorOn
		if !c.promoted {
			c.currentSource, c.currentIndex = c.stage.Source, c.stage.Index(int(ordinal))
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
			return 0, c.rewind(s.op.Context())
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
			if err := c.rewind(s.op.Context()); err != nil {
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
		return n, c.rewind(s.op.Context())
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
