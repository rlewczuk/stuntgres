package xml

// Recognize handlers for already-decoded DOCUMENT input, never new codecs.
// Branches follow libxml2 encoding.c: registered, iconv, ICU, canonical retry.
func recognizedEncoding(label string) bool {
 if len(label)>=100 {return false}
 var upper [99]byte
 for i:=range len(label) {b:=label[i];if b>='a'&&b<='z' {b-='a'-'A'};upper[i]=b}
 name:=string(upper[:len(label)])
 switch name {case "UTF-8","UTF-16LE","UTF-16BE","UTF-16","ISO-8859-1","ASCII","US-ASCII","HTML":return true}
 if glibcEncoding(name) {return true}
 if len(label)<60 {
  key:=icuKey(label)
  if _,available,_,found:=icuAlias(key);found {if available{return true}}else if algorithmicEncoding(key) {return true}
  if directEncoding(label) {return true}
 }
 // xmlParseCharEncoding's canonical aliases, retried after native handlers.
 switch name {
 case "UTF-8","UTF8","UTF-16","UTF16",
  "ISO-10646-UCS-2","UCS-2","UCS2","ISO-10646-UCS-4","UCS-4","UCS4",
  "ISO-8859-1","ISO-LATIN-1","ISO-8859-2","ISO-LATIN-2",
  "ISO-8859-3","ISO-8859-4","ISO-8859-5","ISO-8859-6","ISO-8859-7","ISO-8859-8","ISO-8859-9",
  "ISO-2022-JP","SHIFT_JIS","EUC-JP":return true
 }
 return false
}
// ICU72 ucnv_io.cpp:383-413. Punctuation resets digit state; a leading
// zero is ignored only before a digit and only outside an existing digit run.
func icuKey(s string) string {
 var out [60]byte;n:=0;afterDigit:=false
 for i:=range len(s) {
  b:=s[i]
  if b>='A'&&b<='Z' {b+='a'-'A'}
  if b>='a'&&b<='z' {out[n]=b;n++;afterDigit=false;continue}
  if b>='0'&&b<='9' {
   if b=='0'&&!afterDigit&&i+1<len(s)&&s[i+1]>='0'&&s[i+1]<='9' {continue}
   out[n]=b;n++;afterDigit=true
  }else{afterDigit=false}
 }
 return string(out[:n])
}
