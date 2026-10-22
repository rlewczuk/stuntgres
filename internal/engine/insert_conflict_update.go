package engine

import (
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
)

func preflightConflictUpdate(c *dml.InsertConflictState, evaluation *exec.Context) error {
	if c.Update == nil {
		return nil
	}
	action, err := preflightUpdateWithPlanning(evaluation, c.Update, true)
	if err != nil {
		return err
	}
	c.Update = action
	return nil
}
