package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCNS1SupplementGeneration(t *testing.T) {
	data,err:=os.ReadFile("../data/Unihan-16.0.0-CNS1-extract.txt");if err!=nil{t.Fatal(err)}
	facts,err:=parseCNS1Supplement(data);if err!=nil{t.Fatal(err)}
	want:=[3]cns1Fact{{0x2728,0x4ea0},{0x272f,0x51ab},{0x2734,0x52f9}}
	if facts!=want{t.Fatal("licensed source facts",facts)}
	generated:=generateCNS1Supplement(facts)
	if !bytes.Equal(generated,generateCNS1Supplement(facts)){t.Fatal("nondeterministic supplement")}
	checked,err:=os.ReadFile("../cns1_generated.go");if err!=nil{t.Fatal(err)}
	if !bytes.Equal(generated,checked){t.Fatal("generated supplement differs from checked-in output")}
}

func TestCNS1SupplementCorruption(t *testing.T) {
	data,err:=os.ReadFile("../data/Unihan-16.0.0-CNS1-extract.txt");if err!=nil{t.Fatal(err)}
	original:=string(data)
	row:="U+4EA0\tkIRG_TSource\tT1-2728"
	if strings.Count(original,row)!=1{t.Fatal("missing actual source record")}
	for _,tc:=range []struct{name,replacement string}{
		{"missing",""},{"duplicate",row+"\n"+row},{"scalar","U+4EA1\tkIRG_TSource\tT1-2728"},
		{"surrogate","U+D800\tkIRG_TSource\tT1-2728"},{"zero","U+0000\tkIRG_TSource\tT1-2728"},
		{"cell","U+4EA0\tkIRG_TSource\tT1-2028"},{"plane","U+4EA0\tkIRG_TSource\tT2-2728"},
		{"field","U+4EA0\tkCNS1992\tT1-2728"},{"spaces","U+4EA0 kIRG_TSource T1-2728"},
		{"extra-field",row+"\textra"},{"order","U+51AB\tkIRG_TSource\tT1-272F"},
		{"invalid-hex","U+ZZZZ\tkIRG_TSource\tT1-2728"},
	} {
		t.Run(tc.name,func(t *testing.T){if _,err:=parseCNS1Supplement([]byte(strings.Replace(original,row,tc.replacement,1)));err==nil{t.Fatal("accepted corrupt actual record")}})
	}
	if _,err:=parseCNS1Supplement(append(append([]byte(nil),data...),[]byte("U+4E00\tkIRG_TSource\tT1-4421\n")...));err==nil{t.Fatal("accepted expanded inventory")}
}
