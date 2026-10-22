package settings

import "testing"

func TestSettingsMetadataRaw(t *testing.T) {
	for _, tc := range []struct {
		d                Definition
		input, raw, show string
	}{
		{Definition{Type: Integer, Unit: Milliseconds, IntegerMax: 2147483647}, "1s", "1000", "1s"},
		{Definition{Type: Floating, Unit: Seconds, FloatMax: 10}, "1.23456789", "1.23457", "1.23457e+06us"},
		{Definition{Type: Integer, Unit: Bytes, IntegerMax: 2147483647}, "1kB", "1024", "1kB"},
		{Definition{Type: Boolean}, "yes", "on", "on"},
	} {
		v, err := tc.d.Parse(tc.input)
		if err != nil || tc.d.Raw(v) != tc.raw || tc.d.Format(v) != tc.show {
			t.Fatalf("%s: raw=%s show=%s err=%v", tc.input, tc.d.Raw(v), tc.d.Format(v), err)
		}
	}
}

func TestSettingsMetadataRegistry(t *testing.T) {
	for _, tracking := range []bool{false, true} {
		r := NewRegistry(nil, tracking)
		if len(r.Order()) != 33 {
			t.Fatal(len(r.Order()))
		}
		for _, name := range r.Order() {
			d := r.Definitions()[name]
			if d.Category == "" || d.Description == "" {
				t.Fatal(name, d)
			}
		}
		d, _ := r.Lookup("track_commit_timestamp")
		if d.BootValue().Boolean != tracking {
			t.Fatal(tracking, d.BootValue())
		}
	}
	d, err := Validate(Definition{Name: "demo.metadata", Type: Enum, BootDefault: "a", EnumValues: []string{"a", "b"}})
	if err != nil || d.Category != "Customized Options" || d.ExtraDescription != "" {
		t.Fatal(d, err)
	}
}
