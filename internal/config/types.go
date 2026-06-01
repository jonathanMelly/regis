// internal/config/types.go
package config

//go:generate go run ../../cmd/regis-docgen

// DefaultsConfig holds config-level defaults inherited by all cues.
type DefaultsConfig struct {
	Env map[string]string `yaml:"env"` // doc: Base env for all local cues; overridden by Scenario.Env and cue-level env
}

// Config is the fully parsed and merged config from all regis.yml files.
type Config struct {
	Targets     []Target            `yaml:"targets"`
	Scenarios   map[string]Scenario `yaml:"scenarios"`
	Defaults    DefaultsConfig      `yaml:"defaults"`
	Pre         []PrePost           `yaml:"pre"`
	Post        []PrePost           `yaml:"post"`
	Includes    []string            `yaml:"includes"`
	Run         RunConfig           `yaml:"run"`
	Release     ReleaseConfig       `yaml:"release"`
	Concurrency ConcurrencyConfig   `yaml:"concurrency"`

	// ScenarioNames preserves YAML declaration order (not from YAML tag; set by UnmarshalYAML).
	ScenarioNames []string `yaml:"-"`
	// SourceFiles lists all files loaded in merge order (not from YAML).
	SourceFiles []string `yaml:"-"`
	// BaseDir is the directory of the primary config file (not from YAML).
	BaseDir string `yaml:"-"`
}

type Target struct {
	Name   string `yaml:"name"`   // doc: Unique identifier; drives auto-discovery of .env.<name>
	Host   string `yaml:"host"`   // doc: SSH hostname or IP; accepts ${VAR}
	User   string `yaml:"user"`   // doc: SSH login user
	Port   string `yaml:"port"`   // doc: SSH port (default 22); accepts ${VAR} for env-driven port
	Dir    string `yaml:"dir"`    // doc: Remote working directory; relative dest paths anchor here
	Sudo   bool   `yaml:"sudo"`   // doc: Global sudo default for this target; overridable per cue
	Dotenv string `yaml:"dotenv"` // doc: Explicit env file path; overrides auto-discovery of .env.<name>
}

type RunConfig struct {
	Mode        string `yaml:"mode"`         // doc: How selected targets are called: sequential | batched | parallel (default: sequential)
	BatchSize   int    `yaml:"batch_size"`   // doc: Used only with mode: batched
	MaxFailures int    `yaml:"max_failures"` // doc: Allowed target failures before halting
	OnFailure   string `yaml:"on_failure"`   // doc: Reaction when a target fails: halt | continue (default: halt)
	SuccessWhen string `yaml:"success_when"` // doc: How success is determined: auto | command | service | checks (default: auto)
	OnError     string `yaml:"on_error"`     // doc: Reaction on error: rollback | halt | continue (default: halt)
}

type ReleaseConfig struct {
	Enabled  *bool  `yaml:"enabled"`   // doc: Assign a release ID per deploy, archive files locally and remotely — enables rollback; false = live deploy, no history (default: true)
	Dir      string `yaml:"dir"`       // doc: Remote archive directory for rollback snapshots; default <target.dir>/.regis-releases
	LocalDir string `yaml:"local_dir"` // doc: Local directory for release manifests and rollback snapshots (default: .regis-releases)
	Keep     int    `yaml:"keep"`      // doc: Release snapshots to retain for rollback when --prune-releases is used (default 5)
}

type ConcurrencyConfig struct {
	Lock     bool   `yaml:"lock"`      // doc: Enable deploy lock to prevent concurrent deploys (default: true)
	OnLocked string `yaml:"on_locked"` // doc: Behaviour when lock is held: wait | skip | fail (default: fail)
	LockWait string `yaml:"lock_wait"` // doc: Max wait time when on_locked: wait — Go duration e.g. "30s" (default: 30s)
}

// StringOrList unmarshals a YAML scalar or sequence into []string.
// Custom UnmarshalYAML is in yaml.go.
type StringOrList []string

// PrePost is a top-level pre:/post: entry — a command string or {cmd, local, if} object.
// Custom UnmarshalYAML is in yaml.go.
type PrePost struct {
	Cmd   string
	Local bool   // true = run on local machine instead of target (SSH)
	If    string
}

// PostAction is a cue's post: field — a shorthand string or {cmd, sudo} object.
// "restart:svc" and "reload:svc" are accepted shorthands.
// Custom UnmarshalYAML is in yaml.go.
type PostAction struct {
	Cmd  string
	Sudo bool
}

// WhenExpr represents changed_when / failed_when.
// Exactly one of Expression, Shell, ScenarioRef, or BoolLiteral will be set.
// Custom UnmarshalYAML is in yaml.go.
type WhenExpr struct {
	Expression  string // "stdout contains X" / "exit != 0"
	Shell       string // shell probe
	ScenarioRef string // scenario name reference
	CueRef      string // cue name within ScenarioRef
	BoolLiteral *bool  // literal true or false
}

// Scenario is a named deployment unit.
// Custom UnmarshalYAML handles the requires/needs alias.
type Scenario struct {
	Describe    string            // doc: Human-readable label shown in output
	Env         map[string]string // doc: Env vars for all cues in this scenario; overrides defaults.env, overridden by cue-level env
	Post        PostAction        // doc: Remote command (or restart:/reload: shorthand) run once if any remote cue changed
	Requires    StringOrList      // doc: Scenario names that must complete first (alias: needs) — deduplicated
	Cues        []CueRef          // doc: Ordered list of cues or scenario references
	Checks      []CueRef          // doc: Terminal acceptance phase — read-only probes, run after cues complete
	Rollback    []CueRef          // doc: Actions run on the remote after file-snapshot restore when on_error: rollback triggers; ${RELEASE_ID} is available
	SuccessWhen string            // doc: Override for run.success_when: auto | command | service | checks
	OnError     string            // doc: Override for run.on_error: rollback | halt | continue (default: halt)
	SourceFile  string            // which file this came from (set during load)
}

// CueRollback declares the compensation to execute when on_error: rollback triggers
// and this cue had already executed.
// File natures (binary/config/secret/render/pack): rollback: true restores the
// previous remote state from the local release snapshot.
// Action natures: rollback: "shell cmd" or {shell, sudo} runs a compensation command.
// Custom UnmarshalYAML handles the polymorphic forms.
type CueRollback struct {
	Enabled bool   // true = rollback active for this cue
	Shell   string // compensation shell command (action natures only)
	Sudo    bool   // run compensation with sudo
}

// CueRef is one entry in a cue list — either an inline cue or a scenario reference.
// If ScenarioRef != "", this is a reference. Custom UnmarshalYAML handles shell/cmd alias.
type CueRef struct {
	// Scenario reference fields
	ScenarioRef string       // yaml:"scenario"
	NarrowCue   string       // yaml:"cue" — narrow to one cue within a scenario reference
	CueNames    StringOrList // yaml:"cues" — narrow to subset

	// Inline cue fields
	Name            string            // doc: Unique within the scenario
	Nature          string            // doc: binary | config | secret | action | generate | render | pack | service — inferred when manager: is set
	Local           bool              // doc: true = run on local machine (action only); default = run on SSH target
	Src             StringOrList      // doc: Local source path — binary/secret: single file only; config: scalar path, glob string, or YAML list
	Dest            string            // doc: Remote destination; folder (trailing /) when src is glob or list
	Shell           string            // doc: Shell command to execute (canonical field; cmd: is an alias)
	Env             map[string]string // doc: Environment variables for local execution (key: value map)
	Post            PostAction        // doc: Remote command after cue, or restart:<svc> / reload:<svc> shorthand
	Preserve        StringOrList      // doc: Remote keys never overwritten during merge (secret only)
	Mode            string            // doc: Remote file permissions, e.g. "600"
	If              string            // doc: Boolean rule or probe — skip cue when false
	AffectsRelease  bool              // doc: Mark remote action cue as release-affecting (default false for actions)
	ChangedWhen     WhenExpr          // doc: Override change detection — defaults: binary/config/secret/render=MD5 diff, action=always changed, generate=always equal; expressions: "stdout contains X", "stdout !contains X", "stderr contains X", "exit == N", "exit != N"; or changed_when: true to force
	FailedWhen      WhenExpr          // doc: Override failure detection — defaults: action/generate=exit != 0, binary/config/secret=upload error; expressions: "exit != 0", "stdout contains ERROR", "stderr !contains OK"
	ContinueOnError bool              // doc: true = cue failure does not halt deployment (default false)
	Sudo            bool              // doc: Per-cue sudo override
	DiffMode        string            // doc: Diff strategy for file upload: auto | text | binary — pack default: binary; config/render default: auto
	Prune           *bool             // doc: Delete remote files absent from local set (pack default: true — safe because tier-1 uses managed manifest; render default: false; set prune: false to disable for pack)
	LocalDest       string            // doc: Local path for rendered output; $ARTIFACT_PATH points here during render and fetch (render only)
	Reverse         string            // doc: Shell command run during fetch to transform the downloaded artifact into local source files; $ARTIFACT_PATH = downloaded path (render only)

	// Service cue fields (nature: service — inferred when Manager is set)
	Manager     string            // doc: systemd | crontab (built-in), or any custom string (e.g. pm2); presence infers nature: service
	Binary      string            // doc: Binary filename relative to target.dir (service, crontab)
	ServiceFile string            // doc: Local path to systemd unit file; uploaded to /etc/systemd/system/<name>.service when changed
	Health      string            // doc: Health-check command (crontab watchdog)
	Commands    map[string]string // doc: Override or extend manager commands (start, stop, restart, reload, deploy, status). Template vars: {name}, {binary}, {dir}, {service_file}. Action refs: {restart}, {reload}, etc. expand to the pre-override base command
	Rollback    *CueRollback      // doc: Per-cue compensation on rollback — rollback: true restores previous file state for file natures; rollback: "cmd" or {shell, sudo} runs a command for action natures; infers on_error: rollback for the scenario
}
