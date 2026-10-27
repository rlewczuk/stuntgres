package xml

import (
 "context"
 "errors"
 "net"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "testing"
)

func TestXMLDTDExternalResources(t *testing.T) {
 dtdURILiterals(t)
 dir:=t.TempDir();path:=filepath.Join(dir,"external.dtd")
 if e:=os.WriteFile(path,[]byte(`<!ENTITY imported "WRONG"><!ATTLIST a imported CDATA "WRONG">`),0600);e!=nil{t.Fatal(e)}
 listener,e:=net.Listen("tcp","127.0.0.1:0");if e!=nil{t.Fatal(e)}
 var wg sync.WaitGroup;requests:=make(chan struct{},1);wg.Add(1)
 go func(){defer wg.Done();for {c,e:=listener.Accept();if e!=nil{return};select{case requests<-struct{}{}:default:};c.Close()}}()
 defer func(){listener.Close();wg.Wait();select{case <-requests:t.Error("external resource performed network I/O");default:}}()
 for _,id:=range []string{"file://"+path,path,"relative.dtd","http://"+listener.Addr().String()+"/never.dtd"} {
  for _,external:=range []string{`SYSTEM "`+id+`"`,`PUBLIC "public id" "`+id+`"`} {
   for _,collect:=range []bool{false,true} {
    for _,tc:=range []struct{input string;unresolved bool}{
     {`<!DOCTYPE a `+external+`><a>&missing;</a>`,true},
     {`<!DOCTYPE a [<!ENTITY % p `+external+`>%p;]><a>&missing;</a>`,true},
     {`<!DOCTYPE a [<!ENTITY e `+external+`>]><a>x&e;y</a>`,false},
     {`<?xml version="1.0" standalone="yes"?><!DOCTYPE a [<!ENTITY e `+external+`>]><a>x&e;y</a>`,false},
    } {
     r,d,e:=parse(context.Background(),tc.input,Options{Mode:Document},collect)
     if e!=nil||!r.valid||r.Original()!=tc.input||len(r.Diagnostics())!=0{t.Fatal(tc.input,r,e)}
     if collect{a:=dtdRoot(t,d);if len(a.attributes)!=0{t.Fatal("imported external default")};if tc.unresolved{if len(a.children)!=1||a.children[0].kind!=entityReferenceNode||a.children[0].name!="missing"{t.Fatal(a)}}else{var text strings.Builder;for _,n:=range a.children{if n.kind!=textNode{t.Fatal(n)};text.WriteString(n.value)};if text.String()!="xy"{t.Fatal(a)}}}
    }
   }
   for _,s:=range []string{
    `<!DOCTYPE a [<!ENTITY e `+external+`>]><a x="&e;"/>`,
    `<?xml version="1.0" standalone="yes"?><!DOCTYPE a `+external+`><a>&missing;</a>`,
    `<!DOCTYPE a `+external+` [<!ENTITY e "&missing;">]><a>&e;</a>`,
   }{r,d,e:=parseExpanded(context.Background(),s,Options{Mode:Document});var pe *ParseError;if !errors.As(e,&pe)||r.valid||d!=nil{t.Fatal(s,r,e)}}
  }
 }
}
