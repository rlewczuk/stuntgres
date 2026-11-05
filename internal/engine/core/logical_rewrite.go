package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
)

// LogicalRewriteCapture borrows this actual statement/catalog stage only for
// destination construction. There is no callback registry or retained binding;
// a slotless rewrite pays no capture allocation or datum traversal.
func (s *StatementScope) LogicalRewriteCapture(stage *CatalogStage) table.RewriteCapture {
	e := s.Engine()
	e.Mu.Lock()
	capture := e.logicalCaptureLocked()
	e.Mu.Unlock()
	if !capture {
		return nil
	}
	return &logicalRewriteCapture{scope: s, stage: stage}
}

type logicalRewriteCapture struct {
	scope    *StatementScope
	stage    *CatalogStage
	relation *logical.Relation
	checked  bool
}

// CaptureRewriteRow receives the real validated destination before it joins the
// new heap. A later conversion/index/statement failure retains journal evidence
// but cannot publish the stage's successful-effect projection.
func (c *logicalRewriteCapture) CaptureRewriteRow(d *catalog.Relation, row storage.RowVersion) error {
	s, e := c.scope, c.scope.Engine()
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !e.logicalCaptureLocked() {
		return nil
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	if !c.checked {
		c.relation = logicalRelationOf(c.stage.StageRead(), d)
		c.checked = true
	}
	if c.relation == nil {
		return nil
	}
	if err := e.AssignTransactionXIDLocked(s.TxRef); err != nil {
		return err
	}
	change := ownLogicalChange(logical.Change{
		LSN:      e.Cluster.logical.lsn.Allocate(),
		Kind:     logical.Insert,
		Relation: c.relation,
		New:      row.Values,
		Rewrite:  true,
	})
	s.appendLogicalEventLocked(logical.RowEvent, change)
	c.stage.logicalChanges = append(c.stage.logicalChanges, *change)
	return nil
}
