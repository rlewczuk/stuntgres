package engine

import (
	"strings"
	"testing"
)

func TestBtreeIndexReleaseNaming(t *testing.T) {
	e := New()
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	run(t, e, "CREATE TABLE t(a INT,"+a+" INT,"+b+" INT,"+c+" INT)")
	run(t, e, "CREATE INDEX ON t(a,a)")
	index, _ := btreeRoot(t, e, "t_a_a1_idx")
	if index.columns[0].Name != "a" || index.columns[1].Name != "a1" || index.sqlIndex.keys[0].attribute != index.sqlIndex.keys[1].attribute {
		t.Fatal(index)
	}
	run(t, e, "CREATE INDEX ON t(a,a)")
	btreeRoot(t, e, "t_a_a1_idx1")
	keys := a + "," + b + "," + c
	run(t, e, "CREATE INDEX ON t("+keys+")")
	btreeRoot(t, e, "t_"+a+"_"+strings.Repeat("b", 16)+"_idx")
	run(t, e, "CREATE INDEX ON t("+keys+")")
	btreeRoot(t, e, "t_"+a+"_"+strings.Repeat("b", 15)+"_idx1")
	table := strings.Repeat("t", 40)
	run(t, e, "CREATE TABLE "+table+"("+a+" INT,"+b+" INT,"+c+" INT)")
	run(t, e, "CREATE INDEX ON "+table+"("+keys+")")
	btreeRoot(t, e, strings.Repeat("t", 29)+"_"+strings.Repeat("a", 29)+"_idx")
	run(t, e, "CREATE INDEX ON "+table+"("+keys+")")
	btreeRoot(t, e, strings.Repeat("t", 29)+"_"+strings.Repeat("a", 28)+"_idx1")
	run(t, e, `CREATE TABLE "éTable"("列" INT,index TEXT)`)
	run(t, e, `CREATE INDEX "éIndex" ON "éTable"("列",index DESC)`)
	index, _ = btreeRoot(t, e, `"éIndex"`)
	if index.columns[0].Name != "列" || !index.sqlIndex.keys[1].desc {
		t.Fatal(index)
	}
}

func TestBtreeIndexUtf8AndRepeatedLabels(t *testing.T) {
	e := New()
	unicodeKey := strings.Repeat("é", 30)
	second := strings.Repeat("b", 40)
	run(t, e, `CREATE TABLE t("`+unicodeKey+`" INT,`+second+` INT)`)
	run(t, e, `CREATE INDEX ON t("`+unicodeKey+`",`+second+`)`)
	index, _ := btreeRoot(t, e, `"t_`+strings.Repeat("é", 28)+`_idx"`)
	if len(index.name) != 62 {
		t.Fatal("UTF8 clipping redistributed spare byte", index.name)
	}
	run(t, e, "CREATE TABLE dup(a INT,a1 INT)")
	run(t, e, "CREATE INDEX ON dup(a,a,a1)")
	index, _ = btreeRoot(t, e, "dup_a_a1_a11_idx")
	if index.columns[2].Name != "a11" {
		t.Fatal(index.columns)
	}
}
