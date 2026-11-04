package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// A genuine State.Evaluation seals this association before retaining evaluator
// copies. It borrows the existing source header, not an ambient live Scope. The
// last transferred values detect lost/foreign local ownership before refresh.
// Closed captures contain only inert identity and never establish this borrow.
type routineSourceBinding struct {
	authority       *Authority
	owner           RoutineInitialization
	catalog         catalog.Read
	preserveCatalog bool
}

func (b routineSourceBinding) current(owner RoutineInitialization, source *Source) (RoutineInitialization, catalog.Read, error) {
	if source == nil || source.Authority != b.authority || owner != b.owner {
		return RoutineInitialization{}, catalog.Read{}, fmt.Errorf("stuntgres: changed retained routine source association")
	}
	a := b.authority
	read := a.RoutineCatalog
	if a.Host == nil || !a.RoutineInitialization.Active() || read.Owner() == nil {
		return RoutineInitialization{}, catalog.Read{}, fmt.Errorf("stuntgres: detached routine source attachment")
	}
	if a.RoutineInitialization.domain != b.owner.domain || a.RoutineInitialization.epoch != b.owner.epoch ||
		a.RoutineInitialization.session != b.owner.session || a.RoutineInitialization.transaction != b.owner.transaction ||
		read.Owner() != b.catalog.Owner() || read.Domain() != b.owner.domain || read.Epoch() != b.owner.epoch {
		return RoutineInitialization{}, catalog.Read{}, ids.ErrCatalogReplaced
	}
	return a.RoutineInitialization, read, nil
}

func (s *State) refreshRoutineSource() error {
	if s.ClosedEvaluation != nil {
		s.routineSource = routineSourceBinding{}
		return nil
	}
	if s.routineSource.authority == nil {
		return nil
	}
	if s.routineSource.preserveCatalog {
		var read catalog.Read
		if s.Outer != nil {
			read = s.Outer.Catalog
		}
		if s.Source != nil && s.Source.View != nil {
			read = s.Source.View.Catalog()
		}
		if read != s.routineSource.catalog {
			return fmt.Errorf("stuntgres: changed retained routine state catalog")
		}
	}
	owner, read, err := s.routineSource.current(s.RoutineInitialization, s.Source)
	if err == nil {
		s.RoutineInitialization, s.routineSource.owner = owner, owner
		if !s.routineSource.preserveCatalog {
			s.routineSource.catalog = read
		}
	}
	return err
}

func (c *Context) refreshRoutineSource() error {
	if c.ClosedEvaluation != nil {
		c.routineSource = routineSourceBinding{}
		return nil
	}
	b := &c.routineSource
	if c.Query == nil || c.Catalog != b.catalog {
		return fmt.Errorf("stuntgres: changed retained routine source catalog")
	}
	owner, read, err := b.current(c.RoutineInitialization, c.Query.Source)
	if err != nil {
		return err
	}
	if read.Owner() != c.Catalog.Owner() || read.Domain() != c.Catalog.Domain() || read.Epoch() != c.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	c.RoutineInitialization, b.owner = owner, owner
	if !b.preserveCatalog {
		c.Catalog, b.catalog = read, read
	}
	return nil
}

// AttachRoutineInitialization is the actual cursor admission seam, not an
// evaluator repair. The previous root attachment must already be detached;
// retained child state/evaluator copies refresh their sealed header association.
func (s *State) AttachRoutineInitialization(owner RoutineInitialization, read catalog.Read) error {
	if s.Source == nil || s.Source.Authority == nil || s.Source.Authority.Host == nil ||
		s.RoutineInitialization.Active() || s.Source.Authority.RoutineInitialization.Active() || s.routineSource.authority != nil {
		return fmt.Errorf("stuntgres: invalid routine source attachment boundary")
	}
	if read.Owner() != nil && read.Owner().Sepgsql != nil &&
		(!owner.Active() || owner.domain != read.Domain() || owner.epoch != read.Epoch()) {
		return ids.ErrCatalogReplaced
	}
	s.RoutineInitialization = owner
	s.Source.Authority.RoutineInitialization = owner
	s.Source.Authority.RoutineCatalog = read
	return nil
}

func (s *State) DetachRoutineInitialization() {
	if s.Source != nil && s.Source.Authority != nil {
		s.Source.Authority.RoutineInitialization = RoutineInitialization{}
		s.Source.Authority.RoutineCatalog = catalog.Read{}
	}
	s.RoutineInitialization = RoutineInitialization{}
	s.routineSource = routineSourceBinding{}
}
