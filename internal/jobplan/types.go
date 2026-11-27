// Package jobplan assesses one caller-supplied job observation. It has no reader,
// persistence, scheduler or authority-bearing consumer. Reports are private data.
package jobplan

import "time"

// State describes evidence, never verified source truth. Absent is the zero value.
type State string

const (
	Absent        State = ""
	Unknown       State = "unknown"
	Supplied      State = "supplied_unverified"
	Contradictory State = "contradictory"
)

// Field distinguishes missing evidence from an explicitly supplied zero/false.
// Only Supplied may carry a nonzero Value; other states carry no value.
type Field[T comparable] struct {
	State State
	Value T
}

type Key struct{ SourceInstance, Device, User, Project, NativeID string }
type Profile string

const BBScript Profile = "bb_script"

type OwnerMode string

const (
	External   OwnerMode = "external"
	Looptender OwnerMode = "looptender"
)

type OwnerClaim struct {
	Mode     OwnerMode
	Revision Field[string]
}
type Relationship string

const (
	ObserveOnly    Relationship = "observe_only"
	ManageExternal Relationship = "manage_external_claim"
	Owned          Relationship = "owned_claim"
)

type History string

const (
	NoHistory       History = "none_reported"
	ReportedHistory History = "reported"
)

type Result string

const (
	Succeeded Result = "succeeded"
	Failed    Result = "failed"
	Skipped   Result = "skipped"
	Running   Result = "running"
)

type Capability string

const (
	Inspect  Capability = "inspect"
	Enable   Capability = "enable"
	Disable  Capability = "disable"
	RunNow   Capability = "run_now"
	Cancel   Capability = "cancel"
	Transfer Capability = "transfer"
)

type CapabilityClaim struct {
	Kind       Capability
	References []string
}

type Selection struct {
	Key                   Field[Key]
	ExpectedDefinition    Field[string]
	ExpectedOwnerRevision Field[string]
}

type Observation struct {
	Profile              Profile
	Identity             Field[Key]
	Label                string // Display only; never used for identity or echoed by this preview.
	RetrievedAt          Field[time.Time]
	Enabled              Field[bool]
	Definition           Field[string]
	Occurrence           Field[string]
	Owner                Field[OwnerClaim]
	LoopBinding          Field[string]
	History              Field[History]
	LastRunAt, NextRunAt Field[time.Time]
	LastResult           Field[Result]
	Capabilities         []CapabilityClaim
	References           []string // Opaque private declarations, not authenticated grants.
}

// Restrictions has explicit evidence for every independent control. A missing
// inventory or field is unknown, never a declaration that no holds exist.
type Restrictions struct {
	Enabled, Paused, Stop, Completed, AwaitingUser, Refused        Field[bool]
	AccountRestricted, UnknownOutcome, Superseded, PendingTransfer Field[bool]
}
type Context struct {
	Now          time.Time
	MaxAge       time.Duration
	Relationship Field[Relationship]
	Controls     Field[Restrictions]
	Intensity    string // low/medium/high/unleashed; never changes controls or effects.
}

type Facts struct {
	Identity                            Field[Key]
	RetrievedAt                         Field[time.Time]
	Enabled                             Field[bool]
	Definition, Occurrence, LoopBinding Field[string]
	Owner                               Field[OwnerClaim]
	History                             Field[History]
	LastRunAt, NextRunAt                Field[time.Time]
	LastResult                          Field[Result]
	Relationship                        Field[Relationship]
	Controls                            Field[Restrictions]
}

type EffectAvailability struct{ State, Reason string }

// Report is a private projection. Copied keys/references remain unverified and
// must not be published merely because diagnostics use fixed codes.
type Report struct {
	Selection        Selection
	SuppliedNow      time.Time
	SuppliedMaxAge   time.Duration
	Confidence       string
	Effects          EffectAvailability
	Assessability    map[string]State
	Reasons          []string // Fixed codes only, in deterministic order. Never raw input/errors.
	Restrictions     []string
	Recommendation   string
	Facts            Facts
	CapabilityClaims []CapabilityClaim
	References       []string
}

const (
	MaxStringBytes  = 128
	MaxReferences   = 16
	MaxCapabilities = 6
	MaxFreshness    = time.Hour
)
