package engine

import (
	"stuntgres/internal/engine/catalog"
)

type alterDropProvider struct {
	catalog.Provider
	callback func()
}

func (p alterDropProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}
