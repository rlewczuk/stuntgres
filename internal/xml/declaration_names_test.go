package xml

import (
 "bufio"
 "os"
 "strings"
 "testing"
)

func TestXMLCoreEncodingNameMetadata(t *testing.T) {
 for _,label:=range []string{"ISO-8859-11","ASCII","US-ASCII","UTF-32","LATIN1","windows-1252","iso-8859-11","ascii","UTF-32LE","UTF-32BE","HTML","UTF-16","UTF16","UNICODE","UTF_8","ISO8859-11","ISO-2022-JP","ISO-2022-CN","iscii","cns-11643-1992","ibm-33722_P12A_P12A-2004_U2","icu-internal-compound-d1","BS_4730","CP274","ISO-8859-16","JOHAB"} {
  accept(t,`<?xml version="1.0" encoding="`+label+`"?><a>é</a>`,Document)
 }
 for _,label:=range []string{"SQL_ASCII","MULE_INTERNAL","BOGUS","iso2022","CNS-11643-1992","cns116431992","IBM-33722_P12A_P12A-2004_U2","ibm-33722_p12a_p12a-2004_u2","ibm33722p12ap12a2004u2","ICU-INTERNAL-COMPOUND-D1","icuinternalcompoundd1"} {
  s:=`<?xml version="1.0" encoding="`+label+`"?><a>é</a>`;e:=reject(t,s,Document)
  if e.Detail()!=blocks(1,s,31+len(label),"Unsupported encoding "+label){t.Fatalf("recognition %q %q",label,e.Detail())}
 }
 // ICU raw60 gate precedes normalization; glibc exact punctuation is separate.
 for _,tc:=range []struct{label string;want bool}{
  {"UTF"+strings.Repeat("_",55)+"8",true},
  {"UTF"+strings.Repeat("_",56)+"8",false},
  {"CP_274",false},{"JO_HAB",false},
 } {s:=`<?xml version="1.0" encoding="`+tc.label+`"?><a/>`;if tc.want{accept(t,s,Document)}else{reject(t,s,Document)}}
 for _,tc:=range []struct{input,key string}{{"utf-0008","utf8"},{"a1000","a1000"},{"a00-01","a01"},{"ibm-037","ibm37"},{"U_T_F_8","utf8"}} {if got:=icuKey(tc.input);got!=tc.key{t.Fatalf("ICU key %q got%q want%q",tc.input,got,tc.key)}}
 if canonical,available,_,ok:=icuAlias("iso2022jp");!ok||!available||canonical!="ISO_2022,locale=ja,version=0"{t.Fatal("preferred canonical/options")}
 if canonical,available,_,ok:=icuAlias("johab");!ok||available||canonical!="windows-1361-2000"{t.Fatal("unavailable ICU preference contaminated by glibc")}
 if canonical,available,ambiguous,ok:=icuAlias("csksc56011987");!ok||!available||!ambiguous||canonical!="windows-949-2000"{t.Fatal("compiled ambiguity/default preference")}
 if canonical,available,_,ok:=icuAlias("ibm1047s390");!ok||!available||canonical!="ibm-1047_P100-1995,swaplfnl"{t.Fatal("canonical flag option")}
 // The entire finite pinned corpus is an independent observation, not generated
 // expectations or parity. No native/XML/SQL process is invoked by these units.
 f,e:=os.Open("data/recognition.tsv");if e!=nil{t.Fatal(e)};defer f.Close()
 scan:=bufio.NewScanner(f);accepted,rejected:=0,0
 for scan.Scan() {fields:=strings.Split(scan.Text(),"\t");if fields[0]!="O"{continue};if len(fields)!=3{t.Fatal("bad observation")}
  label,want:=fields[1],fields[2]=="ACCEPT"
  if got:=recognizedEncoding(label);got!=want{t.Errorf("pinned label %q recognized=%v want%v",label,got,want)}
  if want{accepted++}else{rejected++}
 }
 if e=scan.Err();e!=nil{t.Fatal(e)}
 if accepted!=2153||rejected!=361{t.Fatal("incomplete pinned witnesses",accepted,rejected)}
}
