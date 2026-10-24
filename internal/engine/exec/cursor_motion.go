package exec

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// CursorPlace is where a SQL cursor stands relative to its rows. A failed
// cursor can no longer run.
type CursorPlace uint8

const (
	CursorBefore CursorPlace = iota
	CursorOn
	CursorAfter
	CursorFailed
)

// CursorPosition is a SQL cursor's position: the state FETCH, MOVE, portal
// Execute and HOLD materialization read and advance. OrdinalPosition counts
// the rows before the next forward row. CurrentSource and CurrentIndex
// name the current row's occurrence for CURRENT OF; they are nil and zero off
// a row and after HOLD promotion.
type CursorPosition struct {
	Place           CursorPlace
	OrdinalPosition int64
	AtEnd           bool
	CurrentSource   *Source
	CurrentIndex    int
}

// CursorProducer is a live cursor's producer for one admitted motion. Context
// is read at each use. Open opens the stage on first use and returns it.
// Exhausted runs once a forward-only producer reached its end and its stage
// closed. All three run with nothing held.
type CursorProducer interface {
	Context() context.Context
	Open() (*Stage, error)
	Exhausted() error
}

// CursorInput is what one motion reads: a live producer before HOLD
// promotion (Stage is nil until opened), the promoted store after it (Count
// rows of Width values, the first at ordinal Offset).
type CursorInput struct {
	Producer CursorProducer
	Stage    *Stage
	Store    []sqltype.Value
	Count    int
	Offset   int64
	Width    int
	Scroll   bool
}

func forwardCursorError() error {
	err := sqlError("55000", "cursor can only scan forward")
	err.(*sqlstate.Error).Hint = "Declare it with SCROLL option to enable backward scan."
	return err
}

// Rewind changes demand in the retained producer tree, never its binding or frame.
func (p *CursorPosition) Rewind(in CursorInput) error {
	if p.OrdinalPosition == 0 && !p.AtEnd {
		return nil
	}
	if !in.Scroll {
		return forwardCursorError()
	}
	if in.Stage != nil {
		if _, err := in.Stage.Rewind(in.Producer.Context()); err != nil {
			return err
		}
	}
	p.OrdinalPosition, p.AtEnd, p.Place = 0, false, CursorBefore
	p.CurrentSource, p.CurrentIndex = nil, 0
	return nil
}

func (p *CursorPosition) run(in *CursorInput, backward bool, count int64, all bool, rows *FinalRows) (int64, error) {
	if backward && !in.Scroll {
		return 0, forwardCursorError()
	}
	promoted := in.Producer == nil
	var moved int64
	for all || moved < count {
		if backward && p.OrdinalPosition == 0 || !backward && p.AtEnd {
			break
		}
		if !promoted {
			stage, err := in.Producer.Open()
			if err != nil {
				return 0, err
			}
			in.Stage = stage
		}
		ordinal := p.OrdinalPosition
		if backward {
			ordinal--
			if !p.AtEnd {
				ordinal--
			}
			if ordinal < 0 {
				p.OrdinalPosition, p.Place, p.AtEnd = 0, CursorBefore, false
				p.CurrentSource, p.CurrentIndex = nil, 0
				break
			}
		}
		var dst []sqltype.Value
		start := 0
		if rows != nil {
			width := in.Width
			max := int(^uint(0) >> 1)
			if rows.Count == max || len(rows.Values) > max-width {
				return 0, fmt.Errorf("stuntgres: cursor batch cardinality exhausted")
			}
			start = len(rows.Values)
			if !promoted || backward {
				rows.Values = slices.Grow(rows.Values, width)
				rows.Values = rows.Values[:start+width]
				dst = rows.Values[start:]
			}
		}
		var present bool
		var err error
		if promoted {
			index := ordinal - in.Offset
			present = index >= 0 && index < int64(in.Count)
			if present && rows != nil {
				width := in.Width
				if backward {
					copy(dst, in.Store[int(index)*width:(int(index)+1)*width])
				} else {
					// Forward batches borrow immutable contiguous store values.
					first, end := (int(index)-rows.Count)*width, (int(index)+1)*width
					rows.Values = in.Store[first:end:end]
				}
			}
		} else {
			present, err = in.Stage.ProjectOrdinal(in.Producer.Context(), int(ordinal), dst)
		}
		if err != nil {
			return 0, err
		}
		if !present {
			if rows != nil {
				rows.Values = rows.Values[:start]
			}
			if !promoted && !in.Scroll {
				if err := in.Stage.Close(); err != nil {
					return 0, err
				}
				if err := in.Producer.Exhausted(); err != nil {
					return 0, err
				}
			}
			p.Place, p.AtEnd = CursorAfter, true
			p.CurrentSource, p.CurrentIndex = nil, 0
			break
		}
		moved++
		if rows != nil {
			rows.Count++
		}
		p.OrdinalPosition, p.AtEnd, p.Place = ordinal+1, false, CursorOn
		if !promoted {
			p.CurrentSource, p.CurrentIndex = in.Stage.Source, in.Stage.Index(int(ordinal))
		}
	}
	return moved, nil
}

// Move runs one FETCH or MOVE and returns its row count. Fetched rows are
// appended to rows; MOVE passes nil.
func (p *CursorPosition) Move(m *parser.FetchCursor, rows *FinalRows, in CursorInput) (int64, error) {
	count := m.Count
	direction := m.Direction
	if direction == parser.CursorRelative && count != 0 {
		backward := count < 0
		if backward {
			count = -count
		}
		n, err := p.run(&in, backward, count-1, false, nil)
		if err != nil {
			return 0, err
		}
		if n != count-1 {
			return 0, nil
		}
		return p.run(&in, backward, 1, false, rows)
	}
	if direction == parser.CursorAbsolute {
		if count == 0 {
			return 0, p.Rewind(in)
		}
		if count < 0 {
			if _, err := p.run(&in, false, 0, true, nil); err != nil {
				return 0, err
			}
			if _, err := p.run(&in, true, -count-1, false, nil); err != nil {
				return 0, err
			}
			return p.run(&in, true, 1, false, rows)
		}
		if count-1 <= p.OrdinalPosition/2 {
			if err := p.Rewind(in); err != nil {
				return 0, err
			}
			if _, err := p.run(&in, false, count-1, false, nil); err != nil {
				return 0, err
			}
		} else {
			delta := count - p.OrdinalPosition - 1
			if p.AtEnd {
				delta--
			}
			if delta != 0 {
				backward := delta < 0
				if backward {
					delta = -delta
				}
				if _, err := p.run(&in, backward, delta, false, nil); err != nil {
					return 0, err
				}
			}
		}
		return p.run(&in, false, 1, false, rows)
	}
	backward := direction == parser.CursorBackward
	if count < 0 {
		backward, count = !backward, -count
	}
	if m.Move && backward && m.All {
		n := p.OrdinalPosition
		if p.Place == CursorOn {
			n--
		}
		return n, p.Rewind(in)
	}
	if !m.All && count == 0 {
		if m.Move {
			if p.Place == CursorOn {
				return 1, nil
			}
			return 0, nil
		}
		if backward && !in.Scroll {
			return 0, forwardCursorError()
		}
		if p.Place != CursorOn {
			return 0, nil
		}
		if _, err := p.run(&in, true, 1, false, nil); err != nil {
			return 0, err
		}
		return p.run(&in, false, 1, false, rows)
	}
	return p.run(&in, backward, count, m.All, rows)
}
