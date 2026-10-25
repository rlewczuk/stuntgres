package engine

import (
	"context"
)

type fdwUnitTransaction struct {
	calls       []string
	commitError error
	order       *[]string
	name        string
	check       func()
}

func (p *fdwUnitTransaction) record(call string) error {
	if p.check != nil {
		p.check()
	}
	p.calls = append(p.calls, call)
	return nil
}

func (p *fdwUnitTransaction) BeginStatement(context.Context) error {
	return p.record("begin-statement")
}

func (p *fdwUnitTransaction) EndStatement(_ context.Context, success bool) error {
	if success {
		return p.record("statement-success")
	}
	return p.record("statement-failure")
}

func (p *fdwUnitTransaction) Commit(context.Context) error {
	p.record("commit")
	if p.order != nil {
		*p.order = append(*p.order, p.name)
	}
	return p.commitError
}

func (p *fdwUnitTransaction) Rollback(context.Context) error { return p.record("rollback") }

func (p *fdwUnitTransaction) Savepoint(_ context.Context, name string) error {
	return p.record("save:" + name)
}

func (p *fdwUnitTransaction) RollbackTo(_ context.Context, name string) error {
	return p.record("rollback:" + name)
}

func (p *fdwUnitTransaction) Release(_ context.Context, name string) error {
	return p.record("release:" + name)
}
