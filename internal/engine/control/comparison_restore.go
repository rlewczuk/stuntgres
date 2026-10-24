package control

import (
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type comparisonMarker struct {
	Kind, Namespace string
	End             int
}

// comparisonState is the position of a Restore in its comparison context.
type comparisonState uint8

const (
	comparisonClosed  comparisonState = iota // outside the context
	comparisonOpen                           // baseline search_path, between windows
	comparisonBound                          // a window is bound; its one DDL comes next
	comparisonClosing                        // the window's DDL ran; the close marker comes next
)

// ComparisonRestore is Restore's comparison-context state machine. Decode
// collects the markers while the script is parsed, Pair ties each to the SET
// or RESET that follows it, Before walks the states per statement, After and
// Bound wrap the root's check of a bound window's DDL, and Finish rejects an
// unclosed context. It holds no lock; one Restore run owns it.
type ComparisonRestore struct {
	markers           []comparisonMarker
	paired            map[int]comparisonMarker
	state             comparisonState
	started, finished bool
	windows           int
	namespace         ids.ObjectAddress
	before            catalog.Read
	position          int
}

// ComparisonCheck is what the root checks, with ValidateComparisonBinding,
// after a bound window's DDL ran.
type ComparisonCheck struct {
	Before    catalog.Read
	Namespace ids.ObjectAddress
	Position  int
}

// Decode records a comparison marker comment; it does not claim other
// comments.
func (c *ComparisonRestore) Decode(comment string, position parser.Position) (bool, error) {
	kind, namespace, recognized, err := DecodeComparisonContext(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	c.markers = append(c.markers, comparisonMarker{Kind: kind, Namespace: namespace, End: position.Offset + len(comment)})
	return true, nil
}

// Pair ties each marker to the first SET or RESET after it. Only blank text
// may separate them, and no statement takes two markers.
func (c *ComparisonRestore) Pair(source string, statements []parser.Statement) error {
	c.paired = make(map[int]comparisonMarker)
	for _, marker := range c.markers {
		found := false
		for i, stmt := range statements {
			var position int
			switch s := stmt.(type) {
			case *parser.Set:
				position = s.Pos.Offset
			case *parser.Reset:
				position = s.Pos.Offset
			default:
				continue
			}
			if position < marker.End {
				continue
			}
			if strings.TrimSpace(source[marker.End:position]) != "" {
				return InvalidComparisonContext()
			}
			if _, exists := c.paired[i]; exists {
				return InvalidComparisonContext()
			}
			c.paired[i] = marker
			found = true
			break
		}
		if !found {
			return InvalidComparisonContext()
		}
	}
	return nil
}

func comparisonDDLPosition(stmt parser.Statement) (int, bool) {
	switch s := stmt.(type) {
	case *parser.CreateView:
		return s.Pos.Offset, true
	case *parser.CreateQueryTable:
		return s.Pos.Offset, true
	case *parser.CreateTable:
		return s.Pos.Offset, true
	case *parser.CreateIndex:
		return s.Pos.Offset, true
	case *parser.CreateDomain:
		return s.Pos.Offset, true
	case *parser.CreateFunction:
		return s.Pos.Offset, true
	case *parser.CreatePolicy:
		return s.Pos.Offset, true
	case *parser.CreateTrigger:
		return s.Pos.Offset, true
	case *parser.CreateRule:
		return s.Pos.Offset, true
	case *parser.AlterTableDefault:
		return s.Pos.Offset, true
	case *parser.AlterTable:
		return s.Pos.Offset, true
	case *parser.AlterDomain:
		return s.Pos.Offset, true
	case *parser.ForeignDDL:
		return s.Pos.Offset, true
	}
	return 0, false
}

// Before advances the state machine for statement index. current is the
// catalog before the statement runs. execute reports a marked statement the
// caller must run itself, skipping the other Restore entries.
func (c *ComparisonRestore) Before(index int, stmt parser.Statement, current catalog.Read) (execute bool, err error) {
	if c.finished {
		return false, InvalidComparisonContext()
	}
	marker, marked := c.paired[index]
	if marked {
		delete(c.paired, index)
		set, setting := stmt.(*parser.Set)
		baseline := setting && set.Name.Text == "search_path" && !set.Local && !set.Default && set.Interval == nil && len(set.Values) == 1 && set.Values[0] == "pg_catalog"
		switch marker.Kind {
		case "begin":
			if c.started || c.finished || c.state != comparisonClosed || !baseline {
				return false, InvalidComparisonContext()
			}
			c.started = true
			c.state = comparisonOpen
		case "bind":
			if c.state != comparisonOpen || !setting || set.Name.Text != "search_path" || set.Local || set.Default || set.Interval != nil || len(set.Values) != 2 || set.Values[0] != "pg_catalog" {
				return false, InvalidComparisonContext()
			}
			installation, exists := current.Extension(current.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
			if !exists || current.Owner() == nil || current.Owner().Extensions["cube"] != extension.StockCube {
				return false, InvalidComparisonContext()
			}
			if err := extension.StockCube.ValidateExtension(current, installation); err != nil {
				return false, err
			}
			ns, exists := current.Namespace(installation.TargetNamespace)
			if !exists || ns.Temporary != (ids.SessionToken{}) || ns.Name() == "$user" || ns.Name() == "pg_temp" || ns.Name() != marker.Namespace || set.Values[1] != ns.Name() && set.Values[1] != catalog.QuoteIdentifier(ns.Name()) {
				return false, InvalidComparisonContext()
			}
			c.namespace = ns.Address()
			c.state = comparisonBound
		case "close":
			if c.state != comparisonClosing || !baseline {
				return false, InvalidComparisonContext()
			}
			c.state = comparisonOpen
			c.windows++
		case "finish":
			reset, ok := stmt.(*parser.Reset)
			if c.state != comparisonOpen || c.windows == 0 || !ok || reset.All || reset.Name.Text != "search_path" {
				return false, InvalidComparisonContext()
			}
			c.state = comparisonClosed
			c.finished = true
		default:
			return false, InvalidComparisonContext()
		}
		return true, nil
	}
	if _, ok := stmt.(*parser.Reset); ok {
		return false, InvalidComparisonContext()
	}
	if s, ok := stmt.(*parser.Set); ok && s.Name.Text == "search_path" {
		return false, InvalidComparisonContext()
	}
	if c.state == comparisonClosing {
		return false, InvalidComparisonContext()
	}
	if c.state == comparisonBound {
		position, ok := comparisonDDLPosition(stmt)
		if !ok {
			return false, InvalidComparisonContext()
		}
		c.position = position
		c.before = current
	}
	return false, nil
}

// After reports the check due once a bound window's DDL ran.
func (c *ComparisonRestore) After() (ComparisonCheck, bool) {
	if c.state != comparisonBound {
		return ComparisonCheck{}, false
	}
	return ComparisonCheck{Before: c.before, Namespace: c.namespace, Position: c.position}, true
}

// Bound records that the window's DDL passed its check.
func (c *ComparisonRestore) Bound() {
	c.before = catalog.Read{}
	c.state = comparisonClosing
}

// Finish rejects unconsumed markers and an unfinished context.
func (c *ComparisonRestore) Finish() error {
	if len(c.paired) != 0 || c.state != comparisonClosed || c.started != c.finished {
		return InvalidComparisonContext()
	}
	return nil
}
