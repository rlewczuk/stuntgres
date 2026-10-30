package stuntgres

import "testing"

func TestTextSearchFileOwnership(t *testing.T) {
	d := NewDriver()
	data := []byte("original\n")
	if err := d.RegisterTextSearchFile(TextSearchFile{Name: "owned.dict", Data: data}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'x'
	if d.textSearchFiles["owned.dict"] != "original\n" {
		t.Fatal("registration borrowed caller bytes")
	}
	if err := d.RegisterTextSearchFile(TextSearchFile{Name: "empty.affix"}); err != nil {
		t.Fatal(err)
	}
	if value, exists := d.textSearchFiles["empty.affix"]; !exists || value != "" {
		t.Fatal("empty file became absence")
	}
	first, err := d.OpenConnector("first")
	if err != nil {
		t.Fatal(err)
	}
	resources := d.textSearchResources
	if resources == nil || first.(*Connector).engine.TextSearchFiles != resources {
		t.Fatal("startup resource publication")
	}
	again, err := d.OpenConnector("first")
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.OpenConnector("other")
	if err != nil {
		t.Fatal(err)
	}
	if again.(*Connector).engine != first.(*Connector).engine || other.(*Connector).engine == first.(*Connector).engine || other.(*Connector).engine.TextSearchFiles != resources {
		t.Fatal("exact-DSN engine or immutable capability reuse")
	}
	independent := NewDriver()
	if err := independent.RegisterTextSearchFile(TextSearchFile{Name: "owned.dict", Data: []byte("different\n")}); err != nil {
		t.Fatal(err)
	}
	independentConnector, err := independent.OpenConnector("first")
	if err != nil {
		t.Fatal(err)
	}
	if independent.textSearchResources == resources || independentConnector.(*Connector).engine == first.(*Connector).engine || d.textSearchFiles["owned.dict"] != "original\n" {
		t.Fatal("independent Driver adopted another startup state")
	}
}
