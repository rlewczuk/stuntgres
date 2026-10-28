package xml

import "strings"

const xhtmlURI="http://www.w3.org/1999/xhtml"
func isXHTML(d *doctype)bool {
 switch d.publicID{case "-//W3C//DTD XHTML 1.0 Strict//EN","-//W3C//DTD XHTML 1.0 Transitional//EN","-//W3C//DTD XHTML 1.0 Frameset//EN":return true}
 switch d.systemID{case "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd","http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd","http://www.w3.org/TR/xhtml1/DTD/xhtml1-frameset.dtd":return true};return false
}
func xhtmlEmpty(n *node)bool {
 if n.prefix!=""||n.uri!=""&&n.uri!=xhtmlURI{return false}
 switch n.local{case "area","br","base","basefont","col","frame","hr","img","input","isindex","link","meta","param":return true};return false
}
func booleanAttribute(name string)bool {
 // Attribute names are bounded by the parser. ASCII comparisons need no copy.
 for _,s:=range []string{"checked","compact","declare","defer","disabled","ismap","multiple","nohref","noresize","noshade","nowrap","readonly","selected"}{if strings.EqualFold(name,s){return true}};return false
}
func (w *serializer) xhtmlAttributes(n *node){
 var name,id,lang,xmlLang *nodeAttribute
 for i:=range n.attributes {if !w.poll(){return};a:=&n.attributes[i];v:=a.value
  switch {case a.prefix==""&&a.local=="name":name=a;case a.prefix==""&&a.local=="id":id=a;case a.prefix==""&&a.local=="lang":lang=a;case a.prefix=="xml"&&a.local=="lang":xmlLang=a;case a.prefix==""&&v==""&&booleanAttribute(a.local):v=a.local}
  w.attribute(a.name,v)
 }
 if name!=nil&&id==nil {switch n.local{case "a","p","div","img","map","applet","form","frame","iframe":w.attribute("id",name.value)}}
 if lang!=nil&&xmlLang==nil{w.attribute("xml:lang",lang.value)}else if xmlLang!=nil&&lang==nil{w.attribute("lang",xmlLang.value)}
}
func (w *serializer) hasContentType(n *node)bool {
 for _,child:=range n.children{if !w.poll(){return false};if child.local!="meta"{continue};for _,a:=range child.attributes{if !w.poll(){return false};if a.local=="http-equiv"&&strings.EqualFold(a.value,"Content-Type"){return true}}};return false
}
func (w *serializer) meta(depth int,format bool){if format{w.put("\n");w.indent(depth+1)};w.put("<meta http-equiv=\"Content-Type\" content=\"text/html; charset=UTF-8\" />")}
