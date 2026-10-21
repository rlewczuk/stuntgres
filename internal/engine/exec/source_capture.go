package exec

import (
	"context"
	"errors"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
)

// The nil selection path remains allocation-free for undeleted prefixes.
func CaptureSource(ctx context.Context, view *visibility.View, ref ids.ObjectRef) (*Source, error) {
	source, err := CapturePrefix(view, ref)
	if err != nil {
		return nil, err
	}
	data := source.View.Data()
	return SelectQuerySource(ctx, source, data.FrontierAt(ref.Address), data.Ends[ref.Address], data.Deleted[ref.Address])
}

func CapturePrefix(view *visibility.View, ref ids.ObjectRef) (*Source, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	data := pin.Data()
	if data.Catalog.Owner() == nil {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility catalog")
	}
	if _, ok := data.Committed[ref.Address]; !ok || ref.Domain() != data.Catalog.Owner().Domain || ref.Epoch != data.Catalog.Epoch() {
		pin.Release()
		return nil, errors.New("stuntgres: invalid visibility target")
	}
	return &Source{View: pin, Ref: ref, Committed: data.Committed[ref.Address], Own: data.Own[ref.Address], NeedsBirthFilter: data.NeedsBirthFilterAt(ref.Address)}, nil
}
