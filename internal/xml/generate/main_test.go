package main

import (
 "bytes"
 "os"
 "testing"
)

func TestXMLCoreEncodingNameMetadataGenerator(t *testing.T) {
 data,e:=os.ReadFile("../data/recognition.tsv");if e!=nil{t.Fatal(e)}
 v,e:=parse(data,true);if e!=nil{t.Fatal(e)}
 first:=generate(v);second:=generate(v)
 if !bytes.Equal(first,second){t.Fatal("nondeterministic output")}
 shipped,e:=os.ReadFile("../declaration_names_generated.go");if e!=nil{t.Fatal(e)}
 if !bytes.Equal(first,shipped){t.Fatal("static recognition output differs from immutable input")}
 for _,bad:=range []string{
  "G\tASCII\nG\tASCII\n","G\tascii\n","D\t1bad\n","K\t\n","Z\tUTF8\n",
  "A\tx\tx\ta\tunknown\tno\n","A\tx\tx\ta\talgorithmic\tmaybe\n",
  "A\tx\twrong\ta\talgorithmic\tno\n",
  "A\tx\tx\ta\talgorithmic\tno\nA\tX\tx\tb\talgorithmic\tno\n",
  "A\tx\tx\ta\talgorithmic\tno\nA\tX\tx\ta\talgorithmic\tno\n",
  "A\tx\tx\ta,unknown\talgorithmic\tno\n",
  "A\tx\tx\ta,locale=\talgorithmic\tno\n",
  "O\tUTF8\tmaybe\n","O\tUTF8\tACCEPT\nO\tUTF8\tREJECT\n",
 } {if _,e=parse([]byte(bad),false);e==nil{t.Fatalf("accepted invalid rows %q",bad)}}
 if _,e=parse([]byte("G\tASCII\n"),true);e==nil{t.Fatal("incomplete source accepted")}
 if _,e=parse([]byte("A\tISO_2022,locale=ja,version=0\tiso2022localejaversion0\tISO_2022,locale=ja,version=0\talgorithmic\tno\n"),false);e!=nil{t.Fatal("lost canonical options",e)}
 if _,e=parse([]byte("A\tibm-1047-s390\tibm1047s390\tibm-1047_P100-1995,swaplfnl\tshipped-cnv\tno\n"),false);e!=nil{t.Fatal("lost swaplfnl flag",e)}
}
