package exec

import (
	"maps"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

// Raw header demand is independent of identity demand: ctid/xmin/tableoid and
// ordinary scans do not allocate a batch or call the manager for presentation.
func queryReadsXmax(root plan.Node) bool {
	found := false
	var visit func(expr.Expr)
	visit = func(value expr.Expr) {
		plan.WalkExpression(value, func(value expr.Expr) {
			if n, ok := value.(plan.SystemAttributeExpr); ok && n.SubID == -4 {
				found = true
			}
			if n, ok := value.(plan.SubqueryExpr); ok && !n.CTEEdge && n.QueryPlan != nil {
				plan.VisitExecutableQuery(n.QueryPlan.Root, func(plan.Node) {}, visit)
			}
		})
	}
	plan.VisitExecutableQuery(root, func(plan.Node) {}, visit)
	return found
}

// Each published map is immutable. Frames retain that map even when a later
// genuine row event publishes a new projection for the same physical version.
func captureQueryXmax(root plan.Node, source *Source) error {
	if source == nil || source.RawXmax != nil || !queryReadsXmax(root) {
		return nil
	}
	return CaptureSourceXmax(source)
}

// CaptureSourceXmax is called only after an executable consumer demands xmax.
// Capture and publication precede evaluation; the published maps never mutate.
func CaptureSourceXmax(source *Source) error {
	if source == nil || source.RawXmax != nil {
		return nil
	}
	values := make(map[ids.VersionID]uint32)
	if source.Locks != nil {
		maps.Copy(values, source.Locks.RawXmax)
	}
	host := source.LiveHost()
	var capture func(*Source) error
	capture = func(prefix *Source) error {
		if prefix == nil {
			return nil
		}
		if prefix.Sources != nil {
			for _, child := range prefix.Sources {
				if child != prefix {
					if err := capture(child); err != nil {
						return err
					}
				}
			}
		}
		for _, segment := range prefix.Inheritance {
			if err := capture(segment.Prefix); err != nil {
				return err
			}
		}
		if err := capture(prefix.Input); err != nil {
			return err
		}
		if prefix.View != nil && prefix.Inheritance == nil && prefix.Anonymous == nil {
			count := len(prefix.Committed) + len(prefix.Own) + len(prefix.Repaired)
			if count != 0 {
				rows := make([]storage.RowVersion, count)
				for i := range rows {
					rows[i] = prefix.VersionAt(i)
				}
				owner := prefix.LiveHost()
				if owner == nil {
					owner = host
				}
				if owner != nil {
					raw, err := owner.CaptureTupleXmax(prefix.Ref, rows)
					if err != nil {
						return err
					}
					for i, row := range rows {
						values[row.Version] = raw[i]
					}
				} else {
					for _, row := range rows {
						values[row.Version] = row.HeapXmax()
					}
				}
			}
		}
		prefix.RawXmax = values
		return nil
	}
	if err := capture(source); err != nil {
		return err
	}
	if source.Locks != nil {
		source.Locks.RawXmax = values
	}
	return nil
}

func publishLockedXmax(locks *LockContext, version ids.VersionID, xmax uint32) {
	values := maps.Clone(locks.RawXmax)
	values[version] = xmax
	locks.RawXmax = values
}

// CaptureReturningXmax finalizes target/OLD presentation after physical event
// flushing. The canonical full-XID provenance in the frame is never modified.
func CaptureReturningXmax(c *Context, demanded bool) error {
	if !demanded {
		return nil
	}
	c.PhysicalXmaxValid, c.OldPhysicalXmaxValid = false, false
	host := c.LiveHost()
	if host == nil {
		return nil
	}
	if row := c.PhysicalVersion; row.Version != 0 {
		raw, err := host.CaptureTupleXmax(c.PhysicalTable, []storage.RowVersion{row})
		if err != nil {
			return err
		}
		c.PhysicalXmax, c.PhysicalXmaxValid = raw[0], true
	}
	if row := c.OldPhysicalVersion; row.Version != 0 {
		raw, err := host.CaptureTupleXmax(c.OldPhysicalTable, []storage.RowVersion{row})
		if err != nil {
			return err
		}
		c.OldPhysicalXmax, c.OldPhysicalXmaxValid = raw[0], true
	}
	return nil
}
