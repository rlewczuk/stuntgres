package parser

import "strings"

// Statement is the closed set of supported statements.
type Statement interface{ statement() }

// Explain retains a nonexecuting utility wrapper and ordered option syntax.
// Option interpretation belongs to the command owner, not the parser.
type Explain struct {
	Statement Statement
	Options   []ExplainOption
	Pos       Position
}

type ExplainOption struct {
	Name  string
	Value Expr // nil means the value was omitted.
	Pos   Position
}

func (*Explain) statement() {}

// CreateDatabaseStmt preserves ordered option occurrences for execution-time
// validation. Preparation is syntax-only and must not resolve these values.
type CreateDatabaseStmt struct {
	Name    string
	Options []DatabaseOption
}

type DatabaseOption struct {
	Name     string
	Value    string
	Default  bool
	Numeric  bool
	Position int // One-based SQL character position of the option name.
}

func (*CreateDatabaseStmt) statement() {}

// Vacuum retains ordered controls for execution-time validation.
type Vacuum struct {
	Options []VacuumOption
	Targets []VacuumTarget
	Pos     Position
}
type VacuumOption struct {
	Name     Identifier
	Value    Value
	HasValue bool
}
type VacuumTarget struct {
	Name    ObjectName
	Only    bool
	Star    bool
	Columns []Identifier
}

func (*Vacuum) statement() {}

// Analyze shares VACUUM's positioned controls and targets without inheriting
// VACUUM's execution semantics. Options remain in encounter order.
type Analyze struct {
	Options []VacuumOption
	Targets []VacuumTarget
	Pos     Position
}

func (*Analyze) statement() {}

// Load retains a decoded filename; native library loading is external.
type Load struct {
	Filename string
	Pos      Position
}

func (*Load) statement() {}

// AlterSystem is syntax-only external configuration, never a session setting.
// Values are deliberately not retained or normalized.
type AlterSystem struct {
	Action  string // SET, RESET, or RESET ALL.
	Name    Identifier
	Default bool
	Pos     Position
}

func (*AlterSystem) statement() {}

// CreateTablespace retains SQL validation inputs; physical creation is external.
type CreateTablespace struct {
	Name        Identifier
	Owner       *RoleSpec
	Location    string
	LocationPos Position
	Options     []IndexOption
	Pos         Position
}

func (*CreateTablespace) statement() {}

// Trigger lifecycle identities are local names qualified by their owning relation.
type AlterTrigger struct {
	Name   Identifier
	Table  ObjectName
	Action string
	Target Identifier
	Pos    Position
}

func (*AlterTrigger) statement() {}

type DropTrigger struct {
	Name     Identifier
	Table    ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropTrigger) statement() {}

// Rules retain their ordered query commands as one outer SQL utility.
type CreateRule struct {
	Name             Identifier
	Table            ObjectName
	Event            string
	Condition        Expr
	Actions          []Statement
	Replace, Instead bool
	Pos              Position
}

func (*CreateRule) statement() {}

type AlterRule struct {
	Name   Identifier
	Table  ObjectName
	Rename Identifier
	Pos    Position
}

func (*AlterRule) statement() {}

type DropRule struct {
	Name     Identifier
	Table    ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropRule) statement() {}

// Notify stages a local session notification; rule actions remain unsupported.
type Notify struct {
	Channel Identifier
	Payload string
	Pos     Position
}

func (*Notify) statement() {}

type Listen struct {
	Channel Identifier
	Pos     Position
}

func (*Listen) statement() {}

type Unlisten struct {
	Channel Identifier
	All     bool
	Pos     Position
}

func (*Unlisten) statement() {}

// Policy conditions are optional definitions, not enforcement-time fallbacks.
type CreatePolicy struct {
	Name         Identifier
	Table        ObjectName
	Permissive   bool
	Command      string
	Roles        []GrantGrantee
	Using, Check Expr
	Pos          Position
}

func (*CreatePolicy) statement() {}

type AlterPolicy struct {
	Name         Identifier
	Table        ObjectName
	Roles        []GrantGrantee
	Using, Check Expr
	Rename       *Identifier
	// Release 18 accepts this only for rename, but rename_policy does not
	// suppress missing-object errors.
	IfExists bool
	Pos      Position
}

func (*AlterPolicy) statement() {}

type DropPolicy struct {
	Name     Identifier
	Table    ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropPolicy) statement() {}

type CopyEndpoint uint8

const (
	CopyStdin CopyEndpoint = iota
	CopyStdout
	CopyFile
	CopyProgram
)

// Copy retains ordered options so duplicate diagnostics preserve their positions.
type Copy struct {
	Name     ObjectName
	Query    *Select
	Columns  []Identifier
	From     bool
	Endpoint CopyEndpoint
	Filename string
	Options  []CopyOption
	Where    Expr
	Pos      Position
}

func (*Copy) statement() {}

type copyWhereClause struct {
	expression Expr
	pos        Position
}

type CopyArgument uint8

const (
	CopyArgumentNone CopyArgument = iota
	CopyArgumentScalar
	CopyArgumentStar
	CopyArgumentColumns
)

type CopyOption struct {
	Name     string
	Argument CopyArgument
	Value    string
	HasValue bool
	Numeric  bool
	Columns  []Identifier
	Pos      Position
}

// SQL names belong to the physical session, not to driver statement handles.
type PrepareSQL struct {
	Name   Identifier
	Types  []TypeName
	Body   Statement
	Source string
	Pos    Position
}

func (*PrepareSQL) statement() {}

type ExecuteSQL struct {
	Name      Identifier
	Arguments []Expr
	Pos       Position
}

func (*ExecuteSQL) statement() {}

type DeallocateSQL struct {
	Name Identifier
	All  bool
	Pos  Position
}

func (*DeallocateSQL) statement() {}

type DiscardCategory uint8

const (
	DiscardAll DiscardCategory = iota
	DiscardPlans
	DiscardSequences
	DiscardTemp
)

type Discard struct {
	Category DiscardCategory
	Pos      Position
}

func (*Discard) statement() {}

type ExtensionOption struct {
	Name  string
	Value Identifier
	Pos   Position
}
type CreateExtension struct {
	Name        Identifier
	IfNotExists bool
	Options     []ExtensionOption
	Pos         Position
}

func (*CreateExtension) statement() {}

type DropExtension struct {
	Names    []Identifier
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropExtension) statement() {}

type AlterExtensionUpdate struct {
	Name    Identifier
	Options []ExtensionOption
	Pos     Position
}

func (*AlterExtensionUpdate) statement() {}

// ExtensionMemberTarget retains utility syntax until its real identity owner
// resolves it. Routine argument modes and type/operator identities are typed.
type ExtensionMemberTarget struct {
	Kind             string
	Name             ObjectName
	Routine          GrantRoutineTarget
	Type, TargetType TypeName
	Operator         OperatorSignature
	Method           Identifier
}

type AlterExtension struct {
	Name   Identifier
	Action string
	Schema Identifier
	Member ExtensionMemberTarget
	Pos    Position
}

func (*AlterExtension) statement() {}

// TypeName is syntax, not a resolved catalog type.
type TypeName struct {
	Name        string
	Pos         Position
	Quoted      bool
	Schema      *Identifier
	Modifiers   []TypeModifier
	ArrayBounds []int32
	// Qualified calls and standalone regtype syntax retain identifier positions.
	Parts  []Identifier
	System bool
	Setof  bool
}

type EnumLabel struct {
	Text string
	Pos  Position
}
type CreateEnum struct {
	Name   ObjectName
	Labels []EnumLabel
	Pos    Position
}

func (*CreateEnum) statement() {}

type RangeOption struct {
	Name  Identifier
	Value TypeName
}

type CreateRange struct {
	Name    ObjectName
	Options []RangeOption
	Pos     Position
}

func (*CreateRange) statement() {}

type AlterEnum struct {
	Name                        ObjectName
	Label, Old, Neighbor        EnumLabel
	Rename, IfNotExists, Before bool
	Pos                         Position
}

func (*AlterEnum) statement() {}

// AlterType changes identity only; enum labels and composite attributes have
// their own statements and executable-definition lifetimes.
type AlterType struct {
	Name      ObjectName
	Target    Identifier
	SetSchema bool
	Pos       Position
}

func (*AlterType) statement() {}

type CreateComposite struct {
	Name    ObjectName
	Members []Column
	Pos     Position
}

func (*CreateComposite) statement() {}

// Composite attribute actions reuse the positioned table shape actions, but
// have no physical default/constraint/USING syntax or storage authority.
type AlterComposite struct {
	Name    ObjectName
	Actions []AlterTableAction
	Rename  *Identifier
	Column  Identifier
	Mode    DropMode
	Pos     Position
}

func (*AlterComposite) statement() {}

type DropType struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropType) statement() {}

type AlterTableOf struct {
	Name ObjectName
	Type *ObjectName // nil is NOT OF
	Only bool
	Pos  Position
}

func (*AlterTableOf) statement() {}

type CreateDomain struct {
	Name        ObjectName
	Base        TypeName
	Collation   *ObjectName
	Constraints []ColumnConstraint
	Pos         Position
}

func (*CreateDomain) statement() {}

type DropDomain struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropDomain) statement() {}

type DomainAction uint8

const (
	DomainAddCheck DomainAction = iota + 1
	DomainDropCheck
	DomainValidateCheck
	DomainRename
	DomainSetSchema
	DomainSetDefault
	DomainDropDefault
	DomainSetNotNull
	DomainDropNotNull
)

type AlterDomain struct {
	Name       ObjectName
	Action     DomainAction
	Check      *CheckConstraint
	Default    Expr
	Constraint Identifier
	Target     Identifier
	IfExists   bool
	Mode       DropMode
	Pos        Position
}

func (*AlterDomain) statement() {}

// TypeModifier retains constant syntax until descriptor-owned resolution.
type TypeModifier struct {
	Integer   *int32
	Raw       string
	UnaryPlus bool
	NonSimple bool // Actual modifier expression; no arithmetic evaluation.
	Pos       Position
}

type Position struct{ Offset, Character int }
type Identifier struct {
	Text string
	Pos  Position
}
type ObjectName struct{ Parts []Identifier }
type Column struct {
	Name        string
	TypeName    TypeName
	Constraints []ColumnConstraint
	Collation   *ObjectName
	Pos         Position
}

type IdentityMode uint8

const (
	IdentityNone IdentityMode = iota
	IdentityAlways
	IdentityByDefault
)

type IdentityDefinition struct {
	Mode             IdentityMode
	Options          []SequenceOption
	Pos              Position
	ConflictingModes bool
}

type GenerationDefinition struct {
	Expression   Expr
	Mode         IdentityMode
	Kind         string
	Pos, ModePos Position
}

type GenerationRestriction struct {
	Code, Message string
	Pos           Position
}
type ForbiddenGenerationExpr struct{ GenerationRestriction }

func (ForbiddenGenerationExpr) expression()                {}
func (e ForbiddenGenerationExpr) SourcePosition() Position { return e.Pos }

type InsertOverride uint8

const (
	OverrideNone InsertOverride = iota
	OverrideSystem
	OverrideUser
)

type ColumnConstraint struct {
	NotNull           bool
	Default           Expr
	Check             *CheckConstraint
	Unique            *UniqueConstraint
	ForeignKey        *ForeignKeyConstraint
	Identity          *IdentityDefinition
	Generation        *GenerationDefinition
	Name              Identifier
	Pos               Position
	EnforcementSet    bool
	NotEnforced       bool
	EnforcementPos    Position
	EnforcementRepeat Position
	DeferrabilitySet  bool
	InitiallySet      bool
	Deferrable        bool
	InitiallyDeferred bool
	TimingError       string
	TimingPos         Position
	// NamedDeclarationPos retains CONSTRAINT for named DEFAULT/NULL/NOT NULL
	// diagnostics without replacing the clause keyword's existing Pos.
	NamedDeclarationPos Position
}

type NotNullConstraint struct {
	Column Identifier
	Name   Identifier
	Pos    Position
	timing constraintTiming
}

type CheckConstraint struct {
	Name           Identifier
	Expression     Expr
	NoInherit      bool
	NotValid       bool
	NotEnforced    bool
	EnforcementSet bool
	EnforcementPos Position
	Pos            Position
	timing         constraintTiming
}

type UniqueConstraint struct {
	Name    Identifier
	Columns []Identifier
	// Primary distinguishes PRIMARY KEY from UNIQUE on this shared attachment.
	Primary           bool
	NullsNotDistinct  bool
	Deferrable        bool
	InitiallyDeferred bool
	deferrabilitySet  bool
	initiallySet      bool
	Pos               Position
}

// ForeignKeyConstraint retains one declaration for column and table syntax.
// Column REFERENCES receives its local column when the CREATE binder walks it.
type ForeignKeyConstraint struct {
	Name              Identifier
	Columns           []Identifier
	Target            ObjectName
	Referenced        []Identifier
	Full              bool
	NotValid          bool
	NotEnforced       bool
	EnforcementSet    bool
	EnforcementPos    Position
	Deferrable        bool
	InitiallyDeferred bool
	deferrabilitySet  bool
	initiallySet      bool
	Pos               Position
	UpdateAction      ForeignKeyAction
	DeleteAction      ForeignKeyAction
	DeleteColumns     []Identifier
}

// The zero value is the SQL default, independently for each event direction.
type ForeignKeyAction uint8

const (
	ForeignKeyNoAction ForeignKeyAction = iota
	ForeignKeyRestrict
	ForeignKeyCascade
	ForeignKeySetNull
	ForeignKeySetDefault
)

type foreignActionClause struct {
	action  ForeignKeyAction
	columns []Identifier
}

// OnCommitAction is stored with a temporary table's transactional descriptor.
// Unspecified differs from explicit PRESERVE for permanent-table diagnostics.
type OnCommitAction uint8

const (
	OnCommitUnspecified OnCommitAction = iota
	OnCommitPreserve
	OnCommitDelete
	OnCommitDrop
)

// onCommitClause is parser-only metadata, not an unfinished table declaration.
type onCommitClause struct {
	action OnCommitAction
	pos    Position
}

type CreateTable struct {
	Name        ObjectName
	Elements    []TableElement
	OfType      *ObjectName
	Parents     []ObjectName
	Pos         Position
	IfNotExists bool
	Persistence string
	OnCommit    OnCommitAction
	OnCommitPos Position
	PartitionBy *PartitionKey
	PartitionOf *PartitionParent
}

type PartitionStrategy uint8

const (
	PartitionRange PartitionStrategy = iota
	PartitionHash
	PartitionList
)

// Partition keys reuse the existing positioned scalar, collation and class
// names; bounds retain sentinels separately from SQL NULL expressions.
type PartitionKey struct {
	Strategy PartitionStrategy
	Keys     []IndexKey
	Pos      Position
}
type PartitionDatum struct {
	Expression Expr
	Kind       int8 // -1 MINVALUE, 0 finite expression, +1 MAXVALUE
	Pos        Position
}
type PartitionBound struct {
	Strategy                 PartitionStrategy
	Modulus, Remainder       int32
	ModulusPos, RemainderPos Position
	Default                  bool
	Lower, Upper             []PartitionDatum
	Values                   []PartitionDatum
	Pos                      Position
}
type PartitionParent struct {
	Name  ObjectName
	Bound PartitionBound
	Pos   Position
}

// ATTACH consumes an existing relation rather than declaring a new child.
type AlterTableAttachPartition struct {
	Name, Partition ObjectName
	Bound           PartitionBound
	IfExists, Only  bool
	Pos, AttachPos  Position
}

func (*AlterTableAttachPartition) statement() {}

type PartitionDetachMode uint8

const (
	DetachOrdinary PartitionDetachMode = iota
	DetachConcurrent
	DetachFinalize
)

// DETACH removes only an existing immediate partition edge.
type AlterTableDetachPartition struct {
	Name, Partition ObjectName
	IfExists, Only  bool
	Pos, DetachPos  Position
	Mode            PartitionDetachMode
	ModePos         Position
}

func (*AlterTableDetachPartition) statement() {}

func (*CreateTable) statement() {}

type QueryTableForm uint8

const (
	CreateTableAs QueryTableForm = iota
	SelectInto
	CreateMaterializedView
)

type CreateQueryTable struct {
	Name        ObjectName
	Columns     []Identifier
	Query       *Select
	Pos         Position
	Form        QueryTableForm
	IfNotExists bool
	NoData      bool
}

func (*CreateQueryTable) statement() {}

type RefreshMaterializedView struct {
	Name       ObjectName
	NoData     bool
	Concurrent Position
	Pos        Position
}

func (*RefreshMaterializedView) statement() {}

type IndexKey struct {
	Column            Identifier
	Expression        Expr
	Collation         *ObjectName
	Opclass           *ObjectName
	Options           []ViewOption
	Desc              bool
	DirectionExplicit bool
	Nulls             NullPlacement
	Pos               Position
}
type CreateIndex struct {
	Name             Identifier
	Table            ObjectName
	Only             bool
	Method           string
	Keys             []IndexKey
	Include          []IndexKey
	Predicate        Expr
	IfNotExists      bool
	Unique           bool
	UniquePos        Position
	NullsNotDistinct bool
	NullsPos         Position
	Options          []IndexOption
	WithPos          Position
	Concurrent       Position
	Pos              Position
}

func (*CreateIndex) statement() {}

// IndexSubqueryExpr consumes forbidden syntax without introducing query execution.
type IndexSubqueryExpr struct{ Pos Position }

func (IndexSubqueryExpr) expression()                {}
func (e IndexSubqueryExpr) SourcePosition() Position { return e.Pos }

type ViewOption struct {
	Name     Identifier
	Value    string
	HasValue bool
}
type CreateView struct {
	Name        ObjectName
	Columns     []Identifier
	Options     []ViewOption
	Query       *Select
	Pos         Position
	Replace     bool
	Persistence string
}

func (*CreateView) statement() {}

type AlterView struct {
	Name                    ObjectName
	Action                  string
	Column, Target          Identifier
	Options                 []ViewOption
	OwnerIdentity, IfExists bool
	Materialized            bool
	Pos                     Position
}

func (*AlterView) statement() {}

type DropView struct {
	Names        []ObjectName
	IfExists     bool
	Mode         DropMode
	Pos          Position
	Materialized bool
}

func (*DropView) statement() {}

type SequenceOption struct {
	Name  string
	Value int64
	Type  TypeName
	Owned ObjectName
	Pos   Position
}
type CreateSequence struct {
	Name        ObjectName
	Options     []SequenceOption
	Persistence string
	IfNotExists bool
	Pos         Position
}

func (*CreateSequence) statement() {}

type AlterSequence struct {
	Name          ObjectName
	Options       []SequenceOption
	Action        string
	Target        Identifier
	OwnerIdentity bool
	IfExists      bool
	Pos           Position
}

func (*AlterSequence) statement() {}

type DropSequence struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropSequence) statement() {}

type CallExpr struct {
	Name ObjectName
	Args []Expr
}

func (CallExpr) expression() {}
func (e CallExpr) SourcePosition() Position {
	if len(e.Name.Parts) == 0 {
		return Position{}
	}
	return e.Name.Parts[0].Pos
}

type RegclassExpr struct {
	Child Expr
	Pos   Position
}

func (RegclassExpr) expression()                {}
func (e RegclassExpr) SourcePosition() Position { return e.Pos }

// ArrayExpr retains ordered scalar or array children of ARRAY[...] syntax.
type ArrayExpr struct {
	Elements []Expr
	Pos      Position
}

func (ArrayExpr) expression()                {}
func (e ArrayExpr) SourcePosition() Position { return e.Pos }

type ArraySubscript struct {
	Lower, Upper Expr
	Slice        bool
	Pos          Position
}

type SubscriptExpr struct {
	Base       Expr
	Subscripts []ArraySubscript
	Pos        Position
}

func (SubscriptExpr) expression()                {}
func (e SubscriptExpr) SourcePosition() Position { return e.Pos }

// Children uses caller storage for admitted ranks. Larger raw syntax still
// traverses safely before the binder reports the dimension-limit diagnostic.
func (e SubscriptExpr) Children(storage *[13]Expr) []Expr {
	count := 0
	if e.Base != nil {
		count++
	}
	for _, s := range e.Subscripts {
		if s.Lower != nil {
			count++
		}
		if s.Upper != nil {
			count++
		}
	}
	children := storage[:]
	if count > len(children) {
		children = make([]Expr, count)
	}
	at := 0
	if e.Base != nil {
		children[at] = e.Base
		at++
	}
	for _, s := range e.Subscripts {
		if s.Lower != nil {
			children[at] = s.Lower
			at++
		}
		if s.Upper != nil {
			children[at] = s.Upper
			at++
		}
	}
	return children[:count]
}

type TimeZoneExpr struct {
	Value, Zone Expr
	Local       bool
	Pos         Position
}

func (TimeZoneExpr) expression()                {}
func (e TimeZoneExpr) SourcePosition() Position { return e.Pos }

type CollateExpr struct {
	Child Expr
	Name  ObjectName
	Pos   Position
}

func (CollateExpr) expression()                {}
func (e CollateExpr) SourcePosition() Position { return e.Pos }

type TableElement struct {
	Column     *Column
	Like       *TableLike
	NotNull    *NotNullConstraint
	Check      *CheckConstraint
	Unique     *UniqueConstraint
	ForeignKey *ForeignKeyConstraint
	Exclusion  *ExclusionConstraint
	Pos        Position
}

type TableLike struct {
	Name    ObjectName
	Options []LikeOption
}

type LikeOption struct {
	Name      string
	Including bool
	Pos       Position
}

type DropMode uint8

const (
	DropRestrict DropMode = iota
	DropCascade
)

type DropTable struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropTable) statement() {}

type AlterTableRenameColumn struct {
	Name     ObjectName
	Column   Identifier
	NewName  Identifier
	IfExists bool
	Only     bool
	Pos      Position
}

func (*AlterTableRenameColumn) statement() {}

type AlterTableRenameTable struct {
	Name     ObjectName
	NewName  Identifier
	IfExists bool
	Pos      Position
}

func (*AlterTableRenameTable) statement() {}

type AlterTableSetSchema struct {
	Name     ObjectName
	Schema   Identifier
	IfExists bool
	Only     bool
	Pos      Position
}

func (*AlterTableSetSchema) statement() {}

type AlterTableActionKind uint8

const (
	SetNotNull AlterTableActionKind = iota + 1
	DropNotNull
	AddNotNull
	DropConstraint
	DropColumn
	AddIdentity
	DropIdentity
	SetIdentity
	AlterColumnType
	AddColumn
	AddExclusion
	EnableRowSecurity
	DisableRowSecurity
	ForceRowSecurity
	NoForceRowSecurity
	AddUsingIndex
	SetExpression
	DropExpression
	AddCheck
	AddUnique
	AddForeignKey
	SetTablespace
	SetLogged
	SetUnlogged
	SetAccessMethod
	InheritTable
	NoInheritTable
	SetRelationOptions
	ResetRelationOptions
	ValidateConstraint
	AlterConstraintEnforcement
	AlterConstraintTiming
	EnableRule
	DisableRule
	EnableReplicaRule
	EnableAlwaysRule
	EnableTrigger
	DisableTrigger
	EnableReplicaTrigger
	EnableAlwaysTrigger
	ClusterOn
	SetWithoutCluster
	SetWithoutOIDs
	SetStorage
	SetCompression
)

type TriggerSelector uint8

const (
	TriggerNamed TriggerSelector = iota
	TriggerAll
	TriggerUser
)

type AlterTableAction struct {
	Column          Identifier
	Constraint      Identifier
	Rule            Identifier
	Trigger         Identifier
	TriggerSelector TriggerSelector
	Tablespace      Identifier
	Property        Identifier
	Method          Identifier
	ClusterIndex    Identifier
	MethodDefault   bool
	Parent          ObjectName
	Action          AlterTableActionKind
	IfExists        bool
	IfNotExists     bool
	Definition      *Column
	Mode            DropMode
	Pos             Position
	Identity        *IdentityDefinition
	TypeName        TypeName
	Collation       *ObjectName
	Using           Expr
	Exclusion       *ExclusionConstraint
	UsingIndex      *UsingIndexConstraint
	Generation      *GenerationDefinition
	Check           *CheckConstraint
	Unique          *UniqueConstraint
	ForeignKey      *ForeignKeyConstraint
	Options         []IndexOption
	// NOT NULL uses the flat attachment; CHECK/FK retain validity in their AST.
	NotValid          bool
	NotEnforced       bool
	EnforcementSet    bool
	EnforcementPos    Position
	NoInherit         bool
	ValidityPos       Position
	Deferrable        bool
	InitiallyDeferred bool
	DeferrabilitySet  bool
	InitiallySet      bool
}

type AlterTable struct {
	Name         ObjectName
	Actions      []AlterTableAction
	Only         bool
	IfExists     bool
	Materialized bool // ALTER MATERIALIZED VIEW spelling requires an exact target kind.
	Pos          Position
}

func (*AlterTable) statement() {}

// AlterTableAllInTablespace is a standalone utility, not a relation action list.
type AlterTableAllInTablespace struct {
	Source, Destination Identifier
	Owners              []RoleSpec
	Nowait              bool
	Pos                 Position
}

func (*AlterTableAllInTablespace) statement() {}

// UsingIndexConstraint refers to an existing index, never a key-list creation.
type UsingIndexConstraint struct {
	Name, Index                    Identifier
	Primary                        bool
	Pos, KindPos                   Position
	Deferrable, InitiallyDeferred  bool
	deferrabilitySet, initiallySet bool
}

type DefaultAction uint8

const (
	SetDefault DefaultAction = iota + 1
	DropDefault
)

type AlterTableDefault struct {
	Name       ObjectName
	Column     Identifier
	Only       bool
	Action     DefaultAction
	Expression Expr
	Pos        Position
}

func (*AlterTableDefault) statement() {}

type alterTarget struct {
	Name     ObjectName
	Only     bool
	Star     bool
	IfExists bool
}

type dropTarget struct {
	Name     Identifier
	IfExists bool
}

type Insert struct {
	With          []CommonTableExpression
	WithRecursive bool
	Name          ObjectName
	Alias         Identifier
	Targets       []InsertTarget
	Rows          [][]Expr
	Source        *InsertSource
	Pos           Position
	DefaultValues bool
	Override      InsertOverride
	Conflict      *InsertConflict
	Returning     *Returning
}

// InsertConflict is absent for ordinary insertion. An empty DO NOTHING target
// means all enforcing providers; UPDATE requires inference or a constraint.
// Predicate belongs to inference, Where to the action; Pos and OnPos retain
// their distinct diagnostic authority.
type InsertConflict struct {
	Keys        []IndexKey
	Predicate   Expr
	Constraint  Identifier
	Pos         Position
	OnPos       Position
	Update      bool
	Assignments []UpdateAssignment
	Where       Expr
}

type InsertTarget struct {
	Name  string
	Pos   Position
	Steps []TargetStep
}

// A target path retains field/subscript order, including adjacent dimensions.
type TargetStep struct {
	Field     *Identifier
	Subscript *ArraySubscript
}

func (*Insert) statement() {}

// Merge uses the ordinary query and mutation expression machinery.
type Merge struct {
	With          []CommonTableExpression
	WithRecursive bool
	Name          ObjectName
	Alias         Identifier
	Source        FromItem
	On            Expr
	Arms          []MergeArm
	Returning     *Returning
	Pos           Position
}

type MergeMatch uint8

const (
	MergeNotMatched MergeMatch = iota
	MergeMatched
	MergeNotMatchedBySource
)

type MergeAction uint8

const (
	MergeInsertAction MergeAction = iota
	MergeUpdateAction
	MergeDeleteAction
	MergeNothingAction
)

// MergeActionExpr is special SQL syntax, not an ordinary catalog function.
type MergeActionExpr struct{ Pos Position }

func (MergeActionExpr) expression()                {}
func (e MergeActionExpr) SourcePosition() Position { return e.Pos }

type MergeArm struct {
	Match          MergeMatch
	Action         MergeAction
	Assignments    []UpdateAssignment
	Condition      Expr
	Targets        []InsertTarget
	Values         []Expr
	DefaultValues  bool
	Override       InsertOverride
	Pos, ActionPos Position
}

func (*Merge) statement() {}

// Undecorated VALUES stays destination-context input, even in parentheses.
// Only decorated VALUES is represented as a query source.
type InsertSource struct {
	Select *Select
	Values *ValuesQuery
}

func insertStatement(name ObjectName, targets []InsertTarget, source *InsertSource, pos Position) *Insert {
	stmt := &Insert{Name: name, Targets: targets, Pos: pos}
	if source.Select == nil && source.Values.Order == nil && source.Values.Bounds == nil {
		stmt.Rows = source.Values.Rows
	} else {
		stmt.Source = source
	}
	return stmt
}

type Delete struct {
	With          []CommonTableExpression
	WithRecursive bool
	Name          ObjectName
	Only          bool
	Star          bool
	Alias         Identifier
	Using         FromItem
	Where         Expr
	CurrentOf     *Identifier
	Returning     *Returning
	Pos           Position
}

// Mutations own exactly one qualifier; CURRENT OF is not a Boolean expression.
type mutationQualifier struct {
	Where     Expr
	CurrentOf *Identifier
}

func (*Delete) statement() {}

type Returning struct {
	Targets []Expr
	Labels  []Identifier
	Aliases []ReturningAlias
	Pos     Position
}

type ReturningAlias struct {
	New   bool
	Alias Identifier
	Pos   Position
}

type Update struct {
	With          []CommonTableExpression
	WithRecursive bool
	Name          ObjectName
	Only          bool
	Star          bool
	Alias         Identifier
	From          FromItem
	Assignments   []UpdateAssignment
	Where         Expr
	CurrentOf     *Identifier
	Returning     *Returning
	Pos           Position
}
type UpdateAssignment struct {
	Target Identifier
	Steps  []TargetStep
	// Targets is nonempty only for an ordinary UPDATE assignment group.
	// Value remains its single, positioned constructor or subquery source.
	Targets []InsertTarget
	Pos     Position
	Value   Expr
}

func (*Update) statement() {}

type Select struct {
	With          []CommonTableExpression
	WithRecursive bool
	From          FromItem
	Values        *ValuesQuery
	Targets       []Expr
	Labels        []Identifier
	Where         Expr
	GroupBy       *GroupClause
	Having        Expr
	Windows       []WindowDefinition
	Order         *Order
	Pos           Position
	Into          *ObjectName
	Locking       []RowLockClause
	Bounds        *QueryBounds
	Distinct      *DistinctClause
	Set           *SetQuery
	Parenthesized bool // Explicit query operand grouping.
}

func (*Select) statement() {}

type CTEMaterialization uint8

const (
	CTEDefault CTEMaterialization = iota
	CTEMaterialized
	CTENotMaterialized
)

type CommonTableExpression struct {
	Name            Identifier
	Columns         []Identifier
	Query           Statement
	Materialization CTEMaterialization
	Search          *SearchClause
	Cycle           *CycleClause
}

// SearchClause is symbolic syntax, not a traversal scheduler or invocation state.
type SearchClause struct {
	Breadth  bool
	Keys     []Identifier
	Sequence Identifier
	Pos      Position
}

// CycleClause retains only symbolic syntax; execution paths belong to invocations.
type CycleClause struct {
	Keys           []Identifier
	Mark, Path     Identifier
	Value, Default Expr
	Pos            Position
}

// Parentheses can decorate the same Select; retain the outer WITH token for
// multiple-WITH diagnostics rather than overwriting the existing clause.
type cteClause struct {
	Definitions []CommonTableExpression
	Recursive   bool
	Pos         Position
}

type GroupKind uint8

const (
	GroupExpression GroupKind = iota
	GroupList
	GroupEmpty
	GroupSets
	GroupRollup
	GroupCube
)

// A nil clause means no grouping; an explicit empty item is never absent.
type GroupClause struct {
	Items         []GroupItem
	Distinct, All bool
	Pos           Position
}

type GroupItem struct {
	Kind  GroupKind
	Expr  Expr
	Items []GroupItem
	Pos   Position
}

func groupExpressionItem(expr Expr) GroupItem {
	if row, ok := expr.(RowExpr); ok && !row.Explicit {
		items := make([]GroupItem, len(row.Fields))
		for i, field := range row.Fields {
			items[i] = groupExpressionItem(field)
		}
		return GroupItem{Kind: GroupList, Items: items, Pos: row.Pos}
	}
	return GroupItem{Kind: GroupExpression, Expr: expr, Pos: expr.SourcePosition()}
}

// VisitExpressions gives capture, rendering and source traversal one stable
// depth-first leaf order, including leaves below nested grouping constructs.
func (g *GroupClause) VisitExpressions(visit func(Expr) error) error {
	if g == nil {
		return nil
	}
	var walk func([]GroupItem) error
	walk = func(items []GroupItem) error {
		for _, item := range items {
			if item.Kind == GroupExpression {
				if err := visit(item.Expr); err != nil {
					return err
				}
			} else if err := walk(item.Items); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(g.Items)
}

type GroupingExpr struct {
	Args []Expr
	Pos  Position
}

func (GroupingExpr) expression()                {}
func (e GroupingExpr) SourcePosition() Position { return e.Pos }

// SQL cursors retain a private query or exported HOLD output; FETCH publishes detached batches.
type CursorScroll uint8

const (
	CursorScrollDefault CursorScroll = iota
	CursorNoScroll
	CursorScrollEnabled
)

type CursorDirection uint8

const (
	CursorForward CursorDirection = iota
	CursorBackward
	CursorAbsolute
	CursorRelative
)

type DeclareCursor struct {
	Name   Identifier
	Query  *Select
	Source string
	Hold   bool
	Scroll CursorScroll
	Pos    Position
}

func (*DeclareCursor) statement() {}

type FetchCursor struct {
	Name      Identifier
	Count     int64
	All       bool
	Direction CursorDirection
	Move      bool
	Pos       Position
}

func (*FetchCursor) statement() {}

type CloseCursor struct {
	Name Identifier
	All  bool
	Pos  Position
}

func (*CloseCursor) statement() {}

// LeftmostSelect follows set arms only; FROM and expression subqueries are
// separate statement frontiers and must never supply an outer INTO destination.
func LeftmostSelect(query *Select) *Select {
	for query != nil && query.Set != nil {
		query = query.Set.Left
	}
	return query
}

// parenthesizedQuery retains the syntax wrapper's position independently of SELECT.
type parenthesizedQuery struct {
	Query *Select
	Pos   Position
}

// groupedPrimary retains the opening position for field and subscript syntax
// without adding a grouping node to the persisted expression tree.
type groupedPrimary struct {
	Expr Expr
	Pos  Position
}

// RowLockClause is one positioned trailing locking clause.
type RowLockClause struct {
	Mode    string
	Targets []ObjectName
	Wait    RowLockWait
	Pos     Position
}

type RowLockWait uint8

const (
	RowLockBlock RowLockWait = iota
	RowLockSkip
	RowLockNowait
)

// DISTINCT retains plain execution syntax or the positioned ON expression list.
type DistinctClause struct {
	On  []Expr
	Pos Position
}
type SetQuery struct {
	Left, Right *Select
	Operator    string
	All         bool
	Pos         Position
}

// QueryBounds retains decoration presence even for LIMIT ALL/NULL.
type QueryBounds struct {
	Offset, Count Expr
	WithTies      bool
	WithPosition  Position
}

// WindowSpec preserves the input-key namespace and specification caret owner.
type WindowSpec struct {
	Name      *Identifier
	Partition []Expr
	Order     []OrderKey
	Frame     *WindowFrame
	Pos       Position
	Bare      bool
}

type WindowDefinition struct {
	Name Identifier
	Spec WindowSpec
}

// FrameRange is zero so implicit/default frames retain RANGE semantics.
type FrameUnit uint8

const (
	FrameRange FrameUnit = iota
	FrameGroups
	FrameRows
)

type FrameBoundKind uint8

const (
	FrameUnboundedPreceding FrameBoundKind = iota
	FrameOffsetPreceding
	FrameCurrentRow
	FrameOffsetFollowing
	FrameUnboundedFollowing
)

type FrameBound struct {
	Kind   FrameBoundKind
	Offset Expr
	Pos    Position
}

type FrameExclusion uint8

const (
	ExcludeNoOthers FrameExclusion = iota
	ExcludeCurrentRow
	ExcludeGroup
	ExcludeTies
)

type WindowFrame struct {
	Unit       FrameUnit
	Exclusion  FrameExclusion
	Start, End FrameBound
	Pos        Position
}

// Parser-only keyword identity is separate from the specification caret and is
// discarded on attachment; it is not part of the persisted expression tree.
type windowOverClause struct {
	spec  *WindowSpec
	token lexeme
}

type queryTail struct {
	Bounds  *QueryBounds
	Locking []RowLockClause
}

func singletonArgument(expr Expr) bool {
	switch e := expr.(type) {
	case XMLParseExpr:
		return singletonArgument(e.Child)
	case XMLSerializeExpr:
		return singletonArgument(e.Child)
	case CaseExpr:
		if e.Test != nil && !singletonArgument(e.Test) {
			return false
		}
		for _, arm := range e.Arms {
			if !singletonArgument(arm.Condition) || !singletonArgument(arm.Result) {
				return false
			}
		}
		return e.Else == nil || singletonArgument(e.Else)
	case ConditionalExpr:
		for _, arg := range e.Args {
			if !singletonArgument(arg) {
				return false
			}
		}
		return true
	case CurrentDateTimeExpr, LiteralExpr, TypedLiteralExpr, ParameterExpr:
		return true
	case CastExpr:
		return singletonArgument(e.Child)
	case CollateExpr:
		return singletonArgument(e.Child)
	case TimeZoneExpr:
		return singletonArgument(e.Value) && (e.Local || singletonArgument(e.Zone))
	case OperatorExpr:
		// Preserve the former signed literal input spelling, not computed
		// arithmetic through the selected singleton projection exception.
		if len(e.Name.Parts) != 1 || (e.Name.Parts[0].Text != "+" && e.Name.Parts[0].Text != "-") || len(e.Args) != 1 {
			return false
		}
		child := e.Args[0]
		for {
			cast, ok := child.(CastExpr)
			if !ok {
				break
			}
			child = cast.Child
		}
		literal, ok := child.(LiteralExpr)
		return ok && (literal.Value.Kind == IntegerValue || literal.Value.Kind == NumericValue)
	case FunctionExpr:
		if len(e.Name.Parts) < 1 || len(e.Name.Parts) > 2 {
			return false
		}
		if !e.ResolutionOnly && !e.CompositeCandidate && !SnapshotFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !TransactionIdentityFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !ACLValueFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !ObjectAddressFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !CommitTimestampFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !ArrayTransformFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !ArrayRandomFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !NamespaceInquiryFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !BlockingInquiryFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !AccessInquiryFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !RangeInspectFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !UUIDExtractFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) && !NetworkFormatFunctionName(e.Name.Parts[len(e.Name.Parts)-1].Text) {
			switch e.Name.Parts[len(e.Name.Parts)-1].Text {
			case "pg_get_userbyid", "pg_get_catalog_foreign_keys", "pg_tablespace_location", "pg_tablespace_databases", "pg_partition_tree", "pg_partition_ancestors", "pg_partition_root":
			case "bit_length", "octet_length", "length", "char_length", "character_length", "strpos", "btrim", "ltrim", "rtrim", "overlay", "position", "substring", "substr", "lower", "upper", "casefold", "initcap", "replace", "translate", "starts_with", "lpad", "rpad", "repeat", "reverse", "format":
			case "lgamma", "ln", "log", "log10", "power", "sqrt", "width_bucket":
			case "sinh", "cosh", "tanh", "asinh", "acosh", "atanh":
			case "cbrt", "degrees", "radians", "pi", "erf", "erfc", "gamma", "pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible", "col_description", "obj_description", "shobj_description", "num_nulls", "num_nonnulls", "current_schemas", "random", "random_normal", "setseed", "min_scale", "round", "scale", "trim_scale", "trunc", "sign", "mod", "abs", "ceil", "ceiling", "div", "exp", "factorial", "floor", "gcd", "lcm", "acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d", "cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand":
			case "pg_collation_actual_version":
			case "pg_indexam_has_property", "pg_index_has_property", "pg_index_column_has_property":
			case "gen_random_uuid", "uuidv4", "uuidv7":
			case "macaddr8_set7bit":
			case "json_object", "jsonb_object", "jsonb_set", "jsonb_set_lax", "jsonb_insert", "jsonb_path_query_array", "jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first", "jsonb_path_query":
			case "json_typeof", "jsonb_typeof", "json_array_length", "jsonb_array_length", "json_object_keys", "jsonb_object_keys", "json_each", "json_each_text", "json_array_elements", "json_array_elements_text", "jsonb_array_elements", "jsonb_array_elements_text", "json_strip_nulls":
			case "generate_series", "generate_subscripts", "unnest":
			case "unaccent":
			case "regexp_count", "regexp_instr", "regexp_substr", "regexp_like", "regexp_match", "regexp_matches", "regexp_replace", "regexp_split_to_array", "regexp_split_to_table":
			case "isempty", "lower_inc", "upper_inc", "lower_inf", "upper_inf", "range_merge":
			default:
				name := e.Name.Parts[len(e.Name.Parts)-1].Text
				if VersionInformationFunctionName(name) || TypeInquiryFunctionName(name) {
					break
				}
				if strings.EqualFold(name, "int_array_enum") || strings.EqualFold(name, "pgrowlocks") {
					break
				}
				if !strings.EqualFold(name, "unnest") && !strings.EqualFold(name, "generate_series") && !strings.EqualFold(name, "generate_subscripts") && !JSONFunctionName(name) && !JSONBSetFunctionName(name) && !strings.EqualFold(name, "jsonb_path_query_array") && !JSONBPathQueryFunctionName(name) && !enumFunctionName(name) && !mathConstantFunction(name) && !gcdLcmFunction(name) && !trigFunction(name) && !AdvisoryFunctionName(name) && !binaryConversionFunction(name) && !binaryHashFunction(name) && !binaryAccessFunction(name) && !datetimeFunctionName(name) && !stringEncodingFunction(name) && !strings.EqualFold(name, "format") && !strings.EqualFold(name, "json_object") && !strings.EqualFold(name, "jsonb_object") && !strings.HasPrefix(strings.ToLower(name), "pg_get_") {
					return false
				}
			}
		}
		for _, arg := range e.Args {
			if !singletonArgument(arg) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

type TableLockMode uint8

const (
	LockAccessShare TableLockMode = iota
	LockRowShare
	LockRowExclusive
	LockShareUpdateExclusive
	LockShare
	LockShareRowExclusive
	LockExclusive
	LockAccessExclusive
)

type LockTarget struct {
	Name ObjectName
	Only bool
}

type LockTable struct {
	Targets []LockTarget
	Mode    TableLockMode
	Pos     Position
	Nowait  bool
}

func (*LockTable) statement() {}

// Truncate is a zero-result utility. Identity defaults to CONTINUE.
type Truncate struct {
	Targets         []LockTarget
	Mode            DropMode
	Pos             Position
	RestartIdentity bool
}

func (*Truncate) statement() {}

type CommentKind uint8

const (
	CommentTable CommentKind = iota + 1
	CommentColumn
	CommentSchema
	CommentRole
	CommentSequence
	CommentForeignTable
)

type Comment struct {
	Kind CommentKind
	Name ObjectName
	Text string
	Null bool
	Pos  Position
}

func (*Comment) statement() {}

type TransactionControlKind uint8

const (
	KindStart TransactionControlKind = iota
	KindCommit
	KindRollback
	KindPrepareTransaction
	KindCommitPrepared
	KindRollbackPrepared
)

type TransactionIsolation uint8

const (
	IsolationDefault TransactionIsolation = iota
	IsolationReadCommitted
	IsolationReadUncommitted
	IsolationRepeatableRead
	IsolationSerializable
)

type TransactionMode struct {
	Isolation     TransactionIsolation
	AccessSet     bool
	ReadOnly      bool
	DeferrableSet bool
	Deferrable    bool
}

type TransactionCharacteristics struct {
	Session bool
	Modes   []TransactionMode
	Pos     Position
}

func (*TransactionCharacteristics) statement() {}

type TransactionControl struct {
	Kind  TransactionControlKind
	Chain bool
	Pos   Position
	Modes []TransactionMode
	GID   string
}

func (*TransactionControl) statement() {}

type SavepointControlKind uint8

const (
	CreateSavepoint SavepointControlKind = iota
	RollbackSavepoint
	ReleaseSavepoint
)

type SavepointControl struct {
	Kind SavepointControlKind
	Name Identifier
	Pos  Position
}

func (*SavepointControl) statement() {}

type Order struct {
	Keys []OrderKey
}

type NullPlacement uint8

const (
	NullsDefault NullPlacement = iota
	NullsFirst
	NullsLast
)

type OrderKey struct {
	Expr  Expr
	Pos   Position
	Desc  bool
	Nulls NullPlacement
}

// OrderConstant classifies SQL92 ORDER/GROUP ordinals without admitting an
// oversized unsigned token merely because its signed result fits int32.
type OrderConstant struct {
	Value Value
	Sign  string
	Real  bool
}

func (c OrderConstant) Ordinal() (int64, bool) {
	if c.Value.Kind != NumericValue || c.Sign == "+" {
		return 0, false
	}
	v := numberValue(lexeme{text: c.Value.Text, numeric: c.Real, pos: c.Value.Pos}, "")
	if v.Kind != IntegerValue || v.Integer > 2147483647 {
		return 0, false
	}
	if c.Sign == "-" {
		return -v.Integer, true
	}
	return v.Integer, true
}

type ValueKind uint8

const (
	NullValue ValueKind = iota
	IntegerValue
	NumericValue
	StringValue
	BooleanValue
	BitStringValue
)

type Value struct {
	Kind    ValueKind
	Integer int64
	Text    string
	Boolean bool
	Pos     Position
}

// Expr is the closed set of admitted scalar syntax occurrences.
type Expr interface {
	expression()
	SourcePosition() Position
}
type LiteralExpr struct{ Value Value }
type TypedLiteralExpr struct {
	TypeName TypeName
	Text     string
	Pos      Position
	// Nominal syntax requires a domain unless the binder admits the resolved
	// installed scalar. The parser records syntax, not type authority.
	RequiresDomain bool
}
type ColumnExpr struct {
	Name ObjectName
	Pos  Position
}
type ParameterExpr struct {
	Number int
	Pos    Position
}

// DefaultExpr requests the destination's default; it is not a scalar value.
type DefaultExpr struct{ Pos Position }

func (DefaultExpr) expression()                {}
func (e DefaultExpr) SourcePosition() Position { return e.Pos }

type OperatorExpr struct {
	Name ObjectName
	Args []Expr
	Pos  Position
	// SQLSimilar distinguishes SQL regex lowering from adjacent POSIX operators.
	SQLSimilar bool
}

// InListExpr is scalar membership, not a query-bearing subquery.
type InListExpr struct {
	Left   Expr
	Items  []Expr
	Negate bool
	Pos    Position
}

// ArrayComparisonExpr compares a scalar with the flat elements of an array.
// SOME is normalized to ANY (All=false) during parsing.
type ArrayComparisonExpr struct {
	Left, Right Expr
	Name        ObjectName
	All         bool
	Pos         Position
}

func (InListExpr) expression()                         {}
func (e InListExpr) SourcePosition() Position          { return e.Pos }
func (ArrayComparisonExpr) expression()                {}
func (e ArrayComparisonExpr) SourcePosition() Position { return e.Pos }

type LogicalOperator uint8

const (
	LogicalAnd LogicalOperator = iota
	LogicalOr
	LogicalNot
)

type LogicalExpr struct {
	Op          LogicalOperator
	Left, Right Expr
	Pos         Position
}

type PredicateKind uint8

const (
	IsNull PredicateKind = iota
	IsNotNull
	IsTrue
	IsNotTrue
	IsFalse
	IsNotFalse
	IsUnknown
	IsNotUnknown
	IsDistinct
	IsNotDistinct
)

type PredicateExpr struct {
	Kind        PredicateKind
	Left, Right Expr
	Pos         Position
}

func (LogicalExpr) expression() {}
func (e LogicalExpr) SourcePosition() Position {
	pos := e.Pos
	for _, child := range [...]Expr{e.Left, e.Right} {
		if child != nil {
			p := child.SourcePosition()
			if p.Character > 0 && (pos.Character <= 0 || p.Character < pos.Character) {
				pos = p
			}
		}
	}
	return pos
}
func (PredicateExpr) expression()                {}
func (e PredicateExpr) SourcePosition() Position { return e.Pos }

// IdentityExpr is SQL keyword syntax, not an ordinary catalog function call.
type IdentityExpr struct {
	Name string
	Pos  Position
}

func (IdentityExpr) expression()                {}
func (e IdentityExpr) SourcePosition() Position { return e.Pos }

// CurrentDateTimeExpr retains SQL value-function syntax, never a captured value.
type CurrentDateTimeKind uint8

const (
	CurrentDate CurrentDateTimeKind = iota
	CurrentTime
	CurrentTimestamp
	LocalTime
	LocalTimestamp
)

func (k CurrentDateTimeKind) Name() string {
	switch k {
	case CurrentDate:
		return "current_date"
	case CurrentTime:
		return "current_time"
	case CurrentTimestamp:
		return "current_timestamp"
	case LocalTime:
		return "localtime"
	case LocalTimestamp:
		return "localtimestamp"
	}
	return ""
}

type CurrentDateTimeExpr struct {
	Kind         CurrentDateTimeKind
	Precision    int32
	HasPrecision bool
	Pos          Position
}

func (CurrentDateTimeExpr) expression()                {}
func (e CurrentDateTimeExpr) SourcePosition() Position { return e.Pos }

// FunctionSyntax retains SQL special-form provenance for selected scalar families.
type FunctionSyntax uint8

const (
	OrdinaryFunctionSyntax FunctionSyntax = iota
	SQLFunctionSyntax
	SimilarEscapeFunctionSyntax
)

// StringSyntax retains the selected string family's distinct SQL forms.
// Shared BYTEA TRIM lowering translates TrimStringSyntax to SQLFunctionSyntax.
type StringSyntax uint8

const (
	OrdinaryStringSyntax StringSyntax = iota
	TrimStringSyntax
	NormalizeStringSyntax
	NormalizedStringSyntax
	PositionStringSyntax
	OverlayStringSyntax
	PrefixStringSyntax
	SubstringStringSyntax
)

type FunctionExpr struct {
	ProcedureCall bool // CALL matches every declaration position, including OUT.
	Name          ObjectName
	Args          []Expr
	// ArgNames is nil for positional calls; zero entries mark positional arguments.
	// Args and labels always retain written order, independently of bound slots.
	ArgNames           []Identifier
	Pos                Position
	Variadic           bool
	ResolutionOnly     bool // Absent ordinary call: diagnostics, never provider dispatch.
	CompositeCandidate bool // Otherwise-unselected call; only typed composite resolution is admitted.
	// StoredSQLRoutine marks binder-owned copied syntax for a selected SQL
	// scalar routine. Source parsing never sets it; it is not dispatch authority.
	StoredSQLRoutine bool
	// StoredProviderRoutine requires a checked released/installed capture,
	// never SQL-body capability or a public-name fallback.
	StoredProviderRoutine bool
	StoredProviderSymbol  string // Checked recipe data, never name-resolution authority.
	Syntax                FunctionSyntax
	StringSyntax          StringSyntax
	Window                *WindowSpec
	// Selected time providers retain modifiers only for ordinary-function
	// diagnostics; these fields never authorize aggregate/window execution.
	Star      bool
	Distinct  bool
	All       bool
	Order     []OrderKey
	Filter    Expr
	Within    bool
	WithinPos Position
	// Per-function definitions belong to the corresponding ROWS FROM member.
	ColumnDefinitions []FunctionColumnDefinition
	// Retains the direct selected-call DEFAULT boundary after scalar factoring.
	// Parenthesized calls clear this parser-only marker.
	directSelectedCall bool
}

// JSONFormat retains explicit SQL/JSON clauses and their diagnostic positions.
type JSONFormat struct {
	Pos         Position
	Encoding    string
	EncodingPos Position
}

type JSONAggregateOptions struct {
	Input        *JSONFormat
	Absent       bool
	NullPos      Position
	Unique       bool
	UniquePos    Position
	Returning    *TypeName
	Output       *JSONFormat
	ReturningPos Position
}

// SQLJSONExpr retains written SQL/JSON clauses without ordinary function lookup.
type SQLJSONExpr struct {
	Kind           string
	Args           []Expr
	Formats        []*JSONFormat
	Query          *Select
	Returning      *TypeName
	Output         *JSONFormat
	Absent, Unique bool
	Members        bool
	NullPos        Position
	Pos            Position
}

func (SQLJSONExpr) expression()                {}
func (e SQLJSONExpr) SourcePosition() Position { return e.Pos }

// SQLJSONQueryExpr keeps query-function clauses separate from constructors.
// Children are in execution order; DEFAULT children follow all PASSING children.
type SQLJSONQueryExpr struct {
	Kind            string
	Document, Path  Expr
	Input, Output   *JSONFormat
	Passing         []SQLJSONPassing
	Returning       *TypeName
	Wrapper, Quotes string
	Empty, Error    SQLJSONBehavior
	Pos             Position
	TableColumn     string // Analysis-only context for JSON_TABLE column semantics.
}

type SQLJSONPassing struct {
	Value Expr
	Name  Identifier
}

type SQLJSONBehavior struct {
	Kind      string
	Default   Expr
	Pos       Position
	BranchPos Position
}

func (SQLJSONQueryExpr) expression()                {}
func (e SQLJSONQueryExpr) SourcePosition() Position { return e.Pos }

func (e SQLJSONQueryExpr) Children() []Expr {
	children := make([]Expr, 0, 4+len(e.Passing))
	children = append(children, e.Document, e.Path)
	for _, binding := range e.Passing {
		children = append(children, binding.Value)
	}
	if e.Empty.Default != nil {
		children = append(children, e.Empty.Default)
	}
	if e.Error.Default != nil {
		children = append(children, e.Error.Default)
	}
	return children
}

// WithChildren retains all clause metadata while replacing expression children.
func (e SQLJSONQueryExpr) WithChildren(children []Expr) SQLJSONQueryExpr {
	e.Document, e.Path = children[0], children[1]
	e.Passing = append([]SQLJSONPassing(nil), e.Passing...)
	i := 2
	for j := range e.Passing {
		e.Passing[j].Value = children[i]
		i++
	}
	if e.Empty.Default != nil {
		e.Empty.Default = children[i]
		i++
	}
	if e.Error.Default != nil {
		e.Error.Default = children[i]
	}
	return e
}

type JSONPredicateExpr struct {
	Child       Expr
	Kind        string
	Unique, Not bool
	Pos         Position
}

func (JSONPredicateExpr) expression()                {}
func (e JSONPredicateExpr) SourcePosition() Position { return e.Pos }

// AggregateExpr retains ordinary aggregate arguments and call-local modifiers.
type AggregateExpr struct {
	// StoredAggregate marks binder-owned copied syntax for a real owned
	// aggregate. Source parsing never sets it; captured identity is required.
	StoredAggregate       bool
	StoredProviderRoutine bool   // Checked provider capture; not an owned aggregate.
	StoredProviderSymbol  string // Preserves the written syntax independently of executable identity.
	Name                  string
	Star                  bool
	Args                  []Expr
	Order                 []OrderKey
	Distinct              bool
	Variadic              bool
	Within                bool
	WithinPos             Position
	Filter                Expr
	Schema                *Identifier
	Window                *WindowSpec
	FilterPos             Position
	Pos                   Position
	JSON                  *JSONAggregateOptions
}

func (AggregateExpr) expression()                {}
func (e AggregateExpr) SourcePosition() Position { return e.Pos }

type CastSyntax uint8

const (
	StandardCast CastSyntax = iota
	PostfixCast
	FunctionCast
)

type CastExpr struct {
	Child    Expr
	TypeName TypeName
	Syntax   CastSyntax
	Callee   string
	Pos      Position
	// FrozenMethod retains stored IO/relabel semantics without a pg_cast dependency.
	// It is never set by SQL parsing.
	FrozenMethod  string
	FrozenContext string
}

func (CastExpr) expression() {}
func (e CastExpr) SourcePosition() Position {
	if e.Syntax == PostfixCast {
		return e.Child.SourcePosition()
	}
	return e.Pos
}

func (LiteralExpr) expression()                     {}
func (TypedLiteralExpr) expression()                {}
func (ColumnExpr) expression()                      {}
func (ParameterExpr) expression()                   {}
func (OperatorExpr) expression()                    {}
func (FunctionExpr) expression()                    {}
func (e LiteralExpr) SourcePosition() Position      { return e.Value.Pos }
func (e TypedLiteralExpr) SourcePosition() Position { return e.Pos }
func (e ColumnExpr) SourcePosition() Position       { return e.Pos }
func (e ParameterExpr) SourcePosition() Position    { return e.Pos }
func (e OperatorExpr) SourcePosition() Position     { return e.Pos }
func (e FunctionExpr) SourcePosition() Position     { return e.Pos }

// ExpressionStart differs from an operator's diagnostic token location.
// Grouping is transparent; folded literals already retain their sign location.
func ExpressionStart(expr Expr) Position {
	pos := expr.SourcePosition()
	switch e := expr.(type) {
	case InListExpr:
		return ExpressionStart(e.Left)
	case ArrayComparisonExpr:
		return ExpressionStart(e.Left)
	case PredicateExpr:
		return ExpressionStart(e.Left)
	case JSONPredicateExpr:
		return ExpressionStart(e.Child)
	case LogicalExpr:
		if start := ExpressionStart(e.Left); start.Offset < pos.Offset {
			return start
		}
	}
	if subscript, ok := expr.(SubscriptExpr); ok {
		return ExpressionStart(subscript.Base)
	}
	if cast, ok := expr.(CastExpr); ok && cast.Syntax == PostfixCast {
		return ExpressionStart(cast.Child)
	}
	if collate, ok := expr.(CollateExpr); ok {
		return ExpressionStart(collate.Child)
	}
	if operator, ok := expr.(OperatorExpr); ok {
		for _, arg := range operator.Args {
			start := ExpressionStart(arg)
			if start.Offset < pos.Offset {
				pos = start
			}
		}
	}
	return pos
}

// Set uses setting-specific scalar text; Default is distinct from quoted text.
type Set struct {
	Name        Identifier
	Local       bool
	Default     bool
	FromCurrent bool
	Values      []string
	Interval    *TimeZoneInterval
	Pos         Position
}

func (*Set) statement() {}

type TimeZoneInterval struct {
	Text      string
	Precision int32
	Qualifier string
	Pos       Position
}

type Show struct {
	Name Identifier
	Pos  Position
	All  bool
}

func (*Show) statement() {}

type CreateRole struct {
	Name         Identifier
	DefaultLogin bool
	Options      RoleOptions
}

type InitialRoleMembership struct {
	Kind    string
	Pos     Position
	Members []RoleSpec
	raw     string
}

type RoleSpec struct {
	Name     string
	Identity string
	Pos      Position
}

type MembershipOption struct {
	Name  string
	Value bool
	Pos   Position
}

type RoleMembership struct {
	Targets, Members []RoleSpec
	Options          []MembershipOption
	Grantor          *RoleSpec
	Revoke           bool
	RevokeOption     string
	Cascade          bool
	Alias            bool
}

func (*RoleMembership) statement() {}

// ObjectGrant retains ordered privilege and target syntax for execution-time
// resolution. Role memberships remain a separate statement.
type GrantPrivilege struct {
	Name     string
	Pos      Position
	Columns  []Identifier
	All      bool
	RoleName bool
}
type GrantGrantee struct {
	Role   RoleSpec
	Public bool
}
type GrantRoutineArgument struct {
	Mode string
	Name Identifier
	Type TypeName
	Pos  Position
}
type GrantRoutineTarget struct {
	Name               ObjectName
	Arguments          []GrantRoutineArgument
	SignatureSpecified bool
}

type ObjectGrant struct {
	Privileges  []GrantPrivilege
	Relations   []ObjectName
	Routines    []GrantRoutineTarget
	Schemas     []Identifier
	Kind        string
	Grantees    []GrantGrantee
	Grantor     *RoleSpec
	GrantOption bool
	Revoke      bool
	OptionOnly  bool
	Cascade     bool
}

func (*ObjectGrant) statement() {}

type AlterRole struct {
	Name     Identifier
	Identity string
	Options  RoleOptions
}

func (*AlterRole) statement() {}

func (*CreateRole) statement() {}

type DropRole struct{ Names []string }

func (*DropRole) statement() {}

type SessionAuthorization struct {
	Name           string
	Default        bool
	OwnerDirective bool
	// ResetCommand preserves RESET versus SET ... DEFAULT for completion tags.
	ResetCommand bool
}

func (*SessionAuthorization) statement() {}

type RoleSelection struct {
	Name  string
	Reset bool
	Local bool
	// ResetCommand preserves RESET versus SET ROLE TO DEFAULT.
	ResetCommand bool
	Pos          Position
}

func (*RoleSelection) statement() {}

type RoleField uint16

const (
	RoleSuperuser RoleField = 1 << iota
	RoleCreateDB
	RoleCreateRole
	RoleLogin
	RoleInherit
	RoleReplication
	RoleBypassRLS
	RoleConnectionLimit
	RolePassword
	RoleValidUntil
)

type RoleOptions struct {
	Present                                                                 RoleField
	Superuser, CreateDB, CreateRole, Login, Inherit, Replication, BypassRLS bool
	ConnectionLimit                                                         int32
	Password                                                                string
	PasswordNull                                                            bool
	ValidUntil                                                              string
	Duplicate                                                               Position
	Positions                                                               [10]Position // First occurrence, indexed by the RoleField bit.
	InitialMemberships                                                      []InitialRoleMembership
}

func (s RoleOptions) add(field RoleField, pos Position) RoleOptions {
	if s.Present&field != 0 && s.Duplicate == (Position{}) {
		s.Duplicate = pos
	}
	if s.Present&field == 0 {
		for i := range len(s.Positions) {
			if field == RoleField(1)<<i {
				s.Positions[i] = pos
				break
			}
		}
	}
	s.Present |= field
	return s
}

func (s RoleOptions) members(kind string, tok lexeme, members []RoleSpec) RoleOptions {
	for _, option := range s.InitialMemberships {
		if option.Kind == kind && s.Duplicate == (Position{}) {
			s.Duplicate = tok.pos
			break
		}
	}
	s.InitialMemberships = append(s.InitialMemberships, InitialRoleMembership{Kind: kind, Pos: tok.pos, Members: members, raw: tok.raw})
	return s
}

func (s RoleOptions) alter(sqllex sqlLexer) bool {
	if len(s.InitialMemberships) == 0 {
		return true
	}
	option := s.InitialMemberships[0]
	sqllex.(*lexer).syntax(lexeme{raw: option.raw, pos: option.Pos})
	return false
}

func (s RoleOptions) toggle(tok lexeme) (RoleOptions, bool) {
	word := tok.text
	value := true
	if len(word) > 2 && word[:2] == "no" {
		word = word[2:]
		value = false
	}
	var field RoleField
	switch word {
	case "superuser":
		field = RoleSuperuser
		s.Superuser = value
	case "createdb":
		field = RoleCreateDB
		s.CreateDB = value
	case "createrole":
		field = RoleCreateRole
		s.CreateRole = value
	case "login":
		field = RoleLogin
		s.Login = value
	case "inherit":
		field = RoleInherit
		s.Inherit = value
	case "replication":
		field = RoleReplication
		s.Replication = value
	case "bypassrls":
		field = RoleBypassRLS
		s.BypassRLS = value
	default:
		return s, false
	}
	return s.add(field, tok.pos), true
}

type Reset struct {
	Name Identifier
	All  bool
	Pos  Position
}

func (*Reset) statement() {}

type CreateSchema struct {
	Name                  string
	Authorization         string
	AuthorizationIdentity bool
	IfNotExists           bool
	Tables                []*CreateTable
}

func (*CreateSchema) statement() {}

type AlterSchema struct {
	Name          string
	NewName       string
	Owner         string
	OwnerIdentity bool
}

func (*AlterSchema) statement() {}

type DropSchema struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

func (*DropSchema) statement() {}

type settingList struct {
	values       []string
	stringValues []bool
}
type settingAtom struct {
	text     string
	isString bool
}

func settingIdentifier(value string) string {
	safe := value != ""
	for i := range len(value) {
		c := value[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			safe = false
			break
		}
	}
	if keyword := lookupKeyword(value); keyword != nil && keyword.category != Unreserved {
		safe = false
	}
	if safe {
		return value
	}
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}

// SettingQuotesIdentifiers identifies GUC_LIST_QUOTE settings. Their SQL SET
// arguments are separate identifiers, not one string containing the joined list.
func SettingQuotesIdentifiers(name string) bool {
	switch strings.ToLower(name) {
	case "search_path", "temp_tablespaces", "local_preload_libraries", "session_preload_libraries", "shared_preload_libraries", "output_plugin_libraries":
		return true
	}
	return false
}

func parsedSet(name Identifier, list settingList, pos Position) *Set {
	values := list.values
	if SettingQuotesIdentifiers(name.Text) {
		for i, isString := range list.stringValues {
			if isString {
				values[i] = settingIdentifier(values[i])
			}
		}
	}
	return &Set{Name: name, Values: values, Pos: pos}
}
