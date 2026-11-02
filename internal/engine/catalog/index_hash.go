package catalog

type IndexMethod uint8

const (
	BtreeMethod IndexMethod = iota
	HashMethod
	GistMethod
	BrinMethod
	SpgistMethod
	BloomMethod
	GinMethod
)

func (m IndexMethod) String() string {
	if m == GinMethod {
		return "gin"
	}
	if m == BloomMethod {
		return "bloom"
	}
	if m == SpgistMethod {
		return "spgist"
	}
	if m == BrinMethod {
		return "brin"
	}
	if m == GistMethod {
		return "gist"
	}
	if m == HashMethod {
		return "hash"
	}
	return "btree"
}
