package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestTextSearchFileRegistration(t *testing.T) {
	for _, name := range []string{"", "../escape.dict", "/root.dict", "Upper.dict", "has-dash.dict", ".dict", "base", "base.rules", "base.dict.more", "base.DICT", "é.dict", "base\x00.dict"} {
		t.Run(name, func(t *testing.T) {
			err := stuntgres.NewDriver().RegisterTextSearchFile(stuntgres.TextSearchFile{Name: name})
			if err == nil || err.Error() != "stuntgres: invalid text search file name" {
				t.Fatal(err)
			}
		})
	}
	d := stuntgres.NewDriver()
	for _, name := range []string{"words.dict", "words.affix", "words.stop", "empty_0.dict"} {
		if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: name}); err != nil {
			t.Fatal(name, err)
		}
	}
	if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "words.dict", Data: []byte("other")}); err == nil || err.Error() != "stuntgres: duplicate text search file" {
		t.Fatal(err)
	}
	independent := stuntgres.NewDriver()
	if err := independent.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "words.dict", Data: []byte("independent")}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenConnectorAs("registration", ""); err == nil {
		t.Fatal("empty role accepted")
	}
	if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "before_success.stop"}); err != nil {
		t.Fatal("failed connector froze registration", err)
	}
	for _, dsn := range []string{"registration", "registration", "distinct"} {
		connector, err := d.OpenConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if connector.Driver() != d {
			t.Fatal("connector lost Driver ownership")
		}
	}
	if err := d.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "late.stop"}); err == nil || err.Error() != "stuntgres: text search files are frozen" {
		t.Fatal(err)
	}
	if err := independent.RegisterTextSearchFile(stuntgres.TextSearchFile{Name: "still_open.stop"}); err != nil {
		t.Fatal("other Driver frozen", err)
	}
}
