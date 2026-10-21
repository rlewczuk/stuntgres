package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
)

// Empty protected inputs still own their immutable visibility catalog. Do not
// capture physical prefixes or permit later live-version selection to revive rows.
func CaptureDeniedQuerySource(view *visibility.View, ref ids.ObjectRef) (*exec.Source, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, fmt.Errorf("stuntgres: visibility view is released")
	}
	data := pin.Data()
	if data.Catalog.Owner() == nil || ref.Domain() != data.Catalog.Owner().Domain || ref.Epoch != data.Catalog.Epoch() || data.Catalog.Get(ref.Address) == nil {
		pin.Release()
		return nil, fmt.Errorf("stuntgres: invalid visibility target")
	}
	return &exec.Source{View: pin, Ref: ref, RlsDenied: true}, nil
}

// rowSecurityLayers collects the owner and security_invoker flag of each
// view layer of an auto-updatable view write, outermost first.
func RowSecurityLayers(view catalog.Read, b analyze.Binding) []access.RowSecurityLayer {
	if b.ViewWrite == nil {
		return nil
	}
	layers := make([]access.RowSecurityLayer, 0, len(ViewWriteOf(b.ViewWrite).Layers))
	for _, layer := range ViewWriteOf(b.ViewWrite).Layers {
		current := view.MustRelation(layer.Ref.Address)
		layers = append(layers, access.RowSecurityLayer{Owner: current.Owner, Invoker: stored.ViewOf(current).Invoker()})
	}
	return layers
}
