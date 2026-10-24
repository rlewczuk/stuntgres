package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
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

type comparisonRestore struct {
	markers           []comparisonMarker
	paired            map[int]comparisonMarker
	state             comparisonState
	started, finished bool
	windows           int
	namespace         ids.ObjectAddress
	before            catalog.Read
	position          int
}

// comparisonCheck is what the root checks after a bound window's DDL ran.
type comparisonCheck struct {
	Before    catalog.Read
	Namespace ids.ObjectAddress
	Position  int
}

func (c *comparisonRestore) Decode(comment string, position parser.Position) (bool, error) {
	kind, namespace, recognized, err := control.DecodeComparisonContext(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	c.markers = append(c.markers, comparisonMarker{Kind: kind, Namespace: namespace, End: position.Offset + len(comment)})
	return true, nil
}

func (c *comparisonRestore) Pair(source string, statements []parser.Statement) error {
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
				return control.InvalidComparisonContext()
			}
			if _, exists := c.paired[i]; exists {
				return control.InvalidComparisonContext()
			}
			c.paired[i] = marker
			found = true
			break
		}
		if !found {
			return control.InvalidComparisonContext()
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

func (c *comparisonRestore) Before(index int, stmt parser.Statement, current catalog.Read) (bool, error) {
	if c.finished {
		return false, control.InvalidComparisonContext()
	}
	marker, marked := c.paired[index]
	if marked {
		delete(c.paired, index)
		set, setting := stmt.(*parser.Set)
		baseline := setting && set.Name.Text == "search_path" && !set.Local && !set.Default && set.Interval == nil && len(set.Values) == 1 && set.Values[0] == "pg_catalog"
		switch marker.Kind {
		case "begin":
			if c.started || c.finished || c.state != comparisonClosed || !baseline {
				return false, control.InvalidComparisonContext()
			}
			c.started = true
			c.state = comparisonOpen
		case "bind":
			if c.state != comparisonOpen || !setting || set.Name.Text != "search_path" || set.Local || set.Default || set.Interval != nil || len(set.Values) != 2 || set.Values[0] != "pg_catalog" {
				return false, control.InvalidComparisonContext()
			}
			installation, exists := current.Extension(current.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
			if !exists || current.Owner() == nil || current.Owner().Extensions["cube"] != extension.StockCube {
				return false, control.InvalidComparisonContext()
			}
			if err := extension.StockCube.ValidateExtension(current, installation); err != nil {
				return false, err
			}
			ns, exists := current.Namespace(installation.TargetNamespace)
			if !exists || ns.Temporary != (ids.SessionToken{}) || ns.Name() == "$user" || ns.Name() == "pg_temp" || ns.Name() != marker.Namespace || set.Values[1] != ns.Name() && set.Values[1] != catalog.QuoteIdentifier(ns.Name()) {
				return false, control.InvalidComparisonContext()
			}
			c.namespace = ns.Address()
			c.state = comparisonBound
		case "close":
			if c.state != comparisonClosing || !baseline {
				return false, control.InvalidComparisonContext()
			}
			c.state = comparisonOpen
			c.windows++
		case "finish":
			reset, ok := stmt.(*parser.Reset)
			if c.state != comparisonOpen || c.windows == 0 || !ok || reset.All || reset.Name.Text != "search_path" {
				return false, control.InvalidComparisonContext()
			}
			c.state = comparisonClosed
			c.finished = true
		default:
			return false, control.InvalidComparisonContext()
		}
		return true, nil
	}
	if _, ok := stmt.(*parser.Reset); ok {
		return false, control.InvalidComparisonContext()
	}
	if s, ok := stmt.(*parser.Set); ok && s.Name.Text == "search_path" {
		return false, control.InvalidComparisonContext()
	}
	if c.state == comparisonClosing {
		return false, control.InvalidComparisonContext()
	}
	if c.state == comparisonBound {
		position, ok := comparisonDDLPosition(stmt)
		if !ok {
			return false, control.InvalidComparisonContext()
		}
		c.position = position
		c.before = current
	}
	return false, nil
}

func (c *comparisonRestore) After() (comparisonCheck, bool) {
	if c.state != comparisonBound {
		return comparisonCheck{}, false
	}
	return comparisonCheck{Before: c.before, Namespace: c.namespace, Position: c.position}, true
}

func (c *comparisonRestore) Bound() {
	c.before = catalog.Read{}
	c.state = comparisonClosing
}

func (c *comparisonRestore) Finish() error {
	if len(c.paired) != 0 || c.state != comparisonClosed || c.started != c.finished {
		return control.InvalidComparisonContext()
	}
	return nil
}

func decodeRestoreComparisonContext(r *restoreRun, comment string, position parser.Position) (bool, error) {
	return r.comparison.Decode(comment, position)
}

func pairRestoreComparisonContext(r *restoreRun) error {
	return r.comparison.Pair(r.source, r.statements)
}

// beforeRestoreComparisonContext runs a marked SET or RESET itself, so no
// later entry sees it.
func beforeRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	execute, err := r.comparison.Before(r.cur.index, stmt, r.stage.CatalogRead())
	if err != nil || !execute {
		return false, err
	}
	_, err = r.stage.Execute(op, stmt, 0, nil)
	return true, err
}

func afterRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	check, ok := r.comparison.After()
	if !ok {
		return nil
	}
	if err := r.stage.validateComparisonBindingStatement(op, stmt, check); err != nil {
		return err
	}
	r.comparison.Bound()
	return nil
}

func finishRestoreComparisonContext(r *restoreRun, _ context.Context, _ *session.Operation) error {
	return r.comparison.Finish()
}
