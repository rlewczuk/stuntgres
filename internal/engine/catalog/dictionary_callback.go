package catalog

// DictionaryCallbackKind names actual direct native calls, not SQL EXECUTE.
type DictionaryCallbackKind uint8

const (
	DictionaryInitCallback DictionaryCallbackKind = iota + 1
	DictionaryLexizeCallback
)

// DictionaryCallback retains immutable selected metadata only. The host proves
// the original dictionary/template/helper link against its actual operation.
// A nil Dictionary is reserved for the genuine detached DDL init owner.
type DictionaryCallback struct {
	Read       Read
	Dictionary *TSDictionary
	Template   *TSTemplate
	Helper     *Procedure
	Kind       DictionaryCallbackKind
}
