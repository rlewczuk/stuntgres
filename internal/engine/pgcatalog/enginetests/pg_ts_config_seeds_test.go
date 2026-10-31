package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func assertSeedConfigurations(t *testing.T, view catalog.Read, source *catalog.Relation, rows [][]sqltype.Value) {
	t.Helper()
	if len(rows) != 21 {
		t.Fatal("exact simple and twenty released Snowball configuration inventory", rows)
	}
	english := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "english"})
	if english.ObjectID == 0 || english.ObjectID == 3748 {
		t.Fatal("actual allocated English configuration", english)
	}
	arabic := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "arabic"})
	if arabic.ObjectID == 0 || arabic.ObjectID == 3748 || arabic == english {
		t.Fatal("actual allocated Arabic configuration", arabic)
	}
	armenian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "armenian"})
	if armenian.ObjectID == 0 || armenian.ObjectID == 3748 || armenian == english || armenian == arabic {
		t.Fatal("actual allocated Armenian configuration", armenian)
	}
	basque := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "basque"})
	if basque.ObjectID == 0 || basque.ObjectID == 3748 || basque == english || basque == arabic || basque == armenian {
		t.Fatal("actual allocated Basque configuration", basque)
	}
	danish := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "danish"})
	if danish.ObjectID == 0 || danish.ObjectID == 3748 || danish == english || danish == arabic || danish == armenian || danish == basque {
		t.Fatal("actual allocated Danish configuration", danish)
	}
	dutch := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "dutch"})
	if dutch.ObjectID == 0 || dutch.ObjectID == 3748 || dutch == english || dutch == arabic || dutch == armenian || dutch == basque || dutch == danish {
		t.Fatal("actual allocated Dutch configuration", dutch)
	}
	estonian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "estonian"})
	if estonian.ObjectID == 0 || estonian.ObjectID == 3748 || estonian == english || estonian == arabic || estonian == armenian || estonian == basque || estonian == danish || estonian == dutch {
		t.Fatal("actual allocated Estonian configuration", estonian)
	}
	german := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "german"})
	if german.ObjectID == 0 || german.ObjectID == 3748 || german == english || german == arabic || german == armenian || german == basque || german == danish || german == dutch || german == estonian {
		t.Fatal("actual allocated German configuration", german)
	}
	greek := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "greek"})
	if greek.ObjectID == 0 || greek.ObjectID == 3748 || greek == english || greek == arabic || greek == armenian || greek == basque || greek == danish || greek == dutch || greek == estonian || greek == german {
		t.Fatal("actual allocated Greek configuration", greek)
	}
	hindi := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "hindi"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if hindi.ObjectID == 0 || hindi == earlier {
			t.Fatal("actual distinct Hindi configuration", hindi)
		}
	}
	hungarian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "hungarian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if hungarian.ObjectID == 0 || hungarian == earlier {
			t.Fatal("actual distinct Hungarian configuration", hungarian)
		}
	}
	irish := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "irish"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if irish.ObjectID == 0 || irish == earlier {
			t.Fatal("actual distinct Irish configuration", irish)
		}
	}
	italian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "italian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if italian.ObjectID == 0 || italian == earlier {
			t.Fatal("actual distinct Italian configuration", italian)
		}
	}
	lithuanian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "lithuanian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if lithuanian.ObjectID == 0 || lithuanian == earlier {
			t.Fatal("actual distinct Lithuanian configuration", lithuanian)
		}
	}
	norwegian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "norwegian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if norwegian.ObjectID == 0 || norwegian == earlier {
			t.Fatal("actual distinct Norwegian configuration", norwegian)
		}
	}
	portuguese := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "portuguese"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if portuguese.ClassID != ids.TSConfigurationClassID || portuguese.ObjectID == 0 || portuguese == earlier {
			t.Fatal("actual distinct Portuguese configuration", portuguese)
		}
	}
	yiddish := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "yiddish"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if yiddish.ClassID != ids.TSConfigurationClassID || yiddish.ObjectID == 0 || yiddish == earlier { t.Fatal("actual distinct Yiddish configuration", yiddish) }
	}
	indonesian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "indonesian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if indonesian.ObjectID == 0 || indonesian == earlier {
			t.Fatal("actual distinct Indonesian configuration", indonesian)
		}
	}
	nepali := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "nepali"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if nepali.ObjectID == 0 || nepali == earlier {
			t.Fatal("actual distinct Nepali configuration", nepali)
		}
	}
	romanian := view.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, Name: "romanian"})
	for _, earlier := range []ids.ObjectAddress{english, arabic, armenian, basque, danish, dutch, estonian, german, greek, hindi, hungarian, irish, italian, lithuanian, norwegian, portuguese, yiddish, indonesian, nepali, {ClassID: ids.TSConfigurationClassID, ObjectID: 3748}} {
		if romanian.ClassID != ids.TSConfigurationClassID || romanian.ObjectID == 0 || romanian == earlier {
			t.Fatal("actual distinct Romanian configuration", romanian)
		}
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) != 5 {
			t.Fatal("configuration row width", row)
		}
		for i, value := range row {
			if value.IsNull() || value.Type() != source.Columns[i].Usage.Type {
				t.Fatal("source-owned configuration cell", i, value)
			}
		}
		name, err := row[1].Text()
		if err != nil || seen[name] || name != "simple" && name != "english" && name != "arabic" && name != "armenian" && name != "basque" && name != "danish" && name != "dutch" && name != "estonian" && name != "german" && name != "greek" && name != "hindi" && name != "hungarian" && name != "indonesian" && name != "irish" && name != "italian" && name != "lithuanian" && name != "norwegian" && name != "portuguese" && name != "yiddish" && name != "nepali" && name != "romanian" {
			t.Fatal("configuration identity", name, err)
		}
		seen[name] = true
		want := uint32(3748)
		if name == "english" {
			want = uint32(english.ObjectID)
		}
		if name == "arabic" {
			want = uint32(arabic.ObjectID)
		}
		if name == "armenian" {
			want = uint32(armenian.ObjectID)
		}
		if name == "basque" {
			want = uint32(basque.ObjectID)
		}
		if name == "danish" {
			want = uint32(danish.ObjectID)
		}
		if name == "dutch" {
			want = uint32(dutch.ObjectID)
		}
		if name == "estonian" {
			want = uint32(estonian.ObjectID)
		}
		if name == "german" {
			want = uint32(german.ObjectID)
		}
		if name == "greek" {
			want = uint32(greek.ObjectID)
		}
		if name == "hindi" {
			want = uint32(hindi.ObjectID)
		}
		if name == "hungarian" {
			want = uint32(hungarian.ObjectID)
		}
		if name == "irish" {
			want = uint32(irish.ObjectID)
		}
		if name == "italian" {
			want = uint32(italian.ObjectID)
		}
		if name == "lithuanian" {
			want = uint32(lithuanian.ObjectID)
		}
		if name == "norwegian" {
			want = uint32(norwegian.ObjectID)
		}
		if name == "portuguese" {
			want = uint32(portuguese.ObjectID)
		}
		if name == "yiddish" { want = uint32(yiddish.ObjectID) }
		if name == "indonesian" {
			want = uint32(indonesian.ObjectID)
		}
		if name == "nepali" {
			want = uint32(nepali.ObjectID)
		}
		if name == "romanian" {
			want = uint32(romanian.ObjectID)
		}
		for i, expected := range map[int]uint32{0: want, 2: 11, 3: 10, 4: 3722} {
			got, err := row[i].OID()
			if err != nil || got != expected {
				t.Fatal("configuration seed relationship", name, i, got, expected, err)
			}
		}
	}
}
