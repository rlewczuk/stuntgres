package visibility

import "testing"

func TestViewRetainReleaseIndependence(t *testing.T) {
	data := &Data{Frontier: 7}
	view := NewView(data)
	retained := view.Retain()
	if retained == nil || retained == view || retained.Data() != data {
		t.Fatal("Retain did not return an independent handle on the same data")
	}
	retained.Release()
	retained.Release()
	if retained.Load() != nil || retained.Retain() != nil {
		t.Fatal("released handle still exposes data")
	}
	if view.Data() != data || view.Frontier() != 7 {
		t.Fatal("releasing a retained handle invalidated the original")
	}
	view.Release()
	if view.Load() != nil || view.Retain() != nil {
		t.Fatal("released original still exposes data")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Data on a released view did not panic")
		}
	}()
	view.Data()
}

// A handle whose identity points elsewhere is a forged copy: it has no
// release or retain authority and cannot read through Data.
func TestViewForgedCopyHasNoAuthority(t *testing.T) {
	data := &Data{Frontier: 3}
	original := NewView(data)
	forged := &View{self: original}
	forged.data.Store(data)
	forged.Release()
	if forged.Retain() != nil || original.Data() != data {
		t.Fatal("forged view acquired release authority")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("Data on a forged view did not panic")
		}
	}()
	forged.Data()
}
