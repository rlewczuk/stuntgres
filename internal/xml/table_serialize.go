package xml

// tableNamespaces mirrors subtree copy reconciliation: local nsDef lists survive
// unchanged; a name's binding outside the copied subtree is added on its root.
// Both maps and the undo vector are invocation-local, never accepted syntax.
func tableNamespaces(w *serializer, root *node) ([]nodeAttribute,bool) {
 scope:=make(map[string]string)
 inherited:=make(map[string]string)
 type saved struct{ prefix,uri string; present bool }
 var undo []saved
 output:=root.namespaces
 changed:=false
 require:=func(prefix,uri string){
  if uri=="" || prefix=="xml" || !w.poll() {return}
  if existing,ok:=scope[prefix];ok && existing==uri {return}
  if inherited[prefix]==uri {return}
  if !changed {output=append([]nodeAttribute(nil),output...);changed=true}
  name:="xmlns";if prefix!="" {name+=":"+prefix}
  output=append(output,nodeAttribute{name:name,local:prefix,value:uri})
  inherited[prefix]=uri
 }
 var visit func(*node)
 visit=func(n *node){
  if !w.poll() || n.kind!=elementNode {return}
  at:=len(undo)
  for _,ns:=range n.namespaces {
   if !w.poll() {return}
   prefix:=ns.local;if ns.name=="xmlns" {prefix=""}
   old,present:=scope[prefix]
   undo=append(undo,saved{prefix:prefix,uri:old,present:present})
   scope[prefix]=ns.value
  }
  require(n.prefix,n.uri)
  for _,attribute:=range n.attributes {
   if !w.poll() {return}
   require(attribute.prefix,attribute.uri)
  }
  for _,child:=range n.children {if !w.poll() {return};visit(child)}
  for i:=len(undo)-1;i>=at;i-- {
   item:=undo[i]
   if item.present {scope[item.prefix]=item.uri} else {delete(scope,item.prefix)}
  }
  undo=undo[:at]
 }
 visit(root)
 return output,changed
}
