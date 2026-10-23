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
type comparisonRestore struct {
	markers           []comparisonMarker
	paired            map[int]comparisonMarker
	state             uint8 // closed, baseline, one DDL, close
	started, finished bool
	windows           int
	namespace         ids.ObjectAddress
	before            catalog.Read
	position          int
}

func decodeRestoreComparisonContext(r *restoreRun, comment string, position parser.Position) (bool, error) {
	kind, namespace, recognized, err := control.DecodeComparisonContext(comment)
	if err != nil || !recognized {
		return recognized, err
	}
	r.comparison.markers = append(r.comparison.markers, comparisonMarker{Kind: kind, Namespace: namespace, End: position.Offset + len(comment)})
	return true, nil
}

func pairRestoreComparisonContext(r *restoreRun) error {
	r.comparison.paired = make(map[int]comparisonMarker)
	for _, marker := range r.comparison.markers {
		found := false
		for i, stmt := range r.statements {
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
			if strings.TrimSpace(r.source[marker.End:position]) != "" {
				return control.InvalidComparisonContext()
			}
			if _, exists := r.comparison.paired[i]; exists {
				return control.InvalidComparisonContext()
			}
			r.comparison.paired[i] = marker
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

func beforeRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	c := &r.comparison
	if c.finished {
		return false, control.InvalidComparisonContext()
	}
	marker, marked := c.paired[r.cur.index]
	if marked {
		delete(c.paired, r.cur.index)
		set, setting := stmt.(*parser.Set)
		baseline := setting && set.Name.Text == "search_path" && !set.Local && !set.Default && set.Interval == nil && len(set.Values) == 1 && set.Values[0] == "pg_catalog"
		switch marker.Kind {
		case "begin":
			if c.started || c.finished || c.state != 0 || !baseline {
				return false, control.InvalidComparisonContext()
			}
			c.started = true
			c.state = 1
		case "bind":
			if c.state != 1 || !setting || set.Name.Text != "search_path" || set.Local || set.Default || set.Interval != nil || len(set.Values) != 2 || set.Values[0] != "pg_catalog" {
				return false, control.InvalidComparisonContext()
			}
			view := r.stage.CatalogRead()
			installation, exists := view.Extension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "cube"}))
			if !exists || view.Owner() == nil || view.Owner().Extensions["cube"] != extension.StockCube {
				return false, control.InvalidComparisonContext()
			}
			if err := extension.StockCube.ValidateExtension(view, installation); err != nil {
				return false, err
			}
			ns, exists := view.Namespace(installation.TargetNamespace)
			if !exists || ns.Temporary != (ids.SessionToken{}) || ns.Name() == "$user" || ns.Name() == "pg_temp" || ns.Name() != marker.Namespace || set.Values[1] != ns.Name() && set.Values[1] != catalog.QuoteIdentifier(ns.Name()) {
				return false, control.InvalidComparisonContext()
			}
			c.namespace = ns.Address()
			c.state = 2
		case "close":
			if c.state != 3 || !baseline {
				return false, control.InvalidComparisonContext()
			}
			c.state = 1
			c.windows++
		case "finish":
			reset, ok := stmt.(*parser.Reset)
			if c.state != 1 || c.windows == 0 || !ok || reset.All || reset.Name.Text != "search_path" {
				return false, control.InvalidComparisonContext()
			}
			c.state = 0
			c.finished = true
		default:
			return false, control.InvalidComparisonContext()
		}
		_, err := r.stage.Execute(op, stmt, 0, nil)
		return true, err
	}
	if _, ok := stmt.(*parser.Reset); ok {
		return false, control.InvalidComparisonContext()
	}
	if s, ok := stmt.(*parser.Set); ok && s.Name.Text == "search_path" {
		return false, control.InvalidComparisonContext()
	}
	if c.state == 3 {
		return false, control.InvalidComparisonContext()
	}
	if c.state == 2 {
		position, ok := comparisonDDLPosition(stmt)
		if !ok {
			return false, control.InvalidComparisonContext()
		}
		c.position = position
		c.before = r.stage.CatalogRead()
	}
	return false, nil
}

func afterRestoreComparisonContext(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	c := &r.comparison
	if c.state != 2 {
		return nil
	}
	if err := r.stage.validateComparisonBindingStatement(op, stmt, c.before, c.namespace, c.position); err != nil {
		return err
	}
	c.before = catalog.Read{}
	c.state = 3
	return nil
}

func finishRestoreComparisonContext(r *restoreRun, _ context.Context, _ *session.Operation) error {
	c := &r.comparison
	if len(c.paired) != 0 || c.state != 0 || c.started != c.finished {
		return control.InvalidComparisonContext()
	}
	return nil
}
