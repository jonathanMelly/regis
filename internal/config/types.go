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
	State       StateConfig         `yaml:"state"`
	Concurrency ConcurrencyConfig   `yaml:"concurrency"`

	// ScenarioNames preserves YAML declaration order (not from YAML tag; set by UnmarshalYAML).
	ScenarioNames []string `yaml:"-"`
	// SourceFiles lists all files loaded in merge order (not from YAML).
	SourceFiles []string `yaml:"-"`
	// BaseDir is the directory of the primary config file (not from YAML).
	BaseDir string `yaml:"-"`
}

type Target struct {
	Name     string `yaml:"name"`     // doc: Unique identifier; drives auto-discovery of .env.<name>
	Host     string `yaml:"host"`     // doc: SSH hostname or IP; accepts ${VAR}
	User     string `yaml:"user"`     // doc: SSH login user
	Port     string `yaml:"port"`     // doc: SSH port (default 22); accepts ${VAR} for env-driven port
	Dir      string `yaml:"dir"`      // doc: Remote working directory; relative dest paths anchor here
	Sudo     bool   `yaml:"sudo"`     // doc: Global sudo default for this target; overridable per cue
	Dotenv   string `yaml:"dotenv"`   // doc: Explicit env file path; overrides auto-discovery of .env.<name>
	Password string `yaml:"password"` // doc: SSH password (fallback when public-key auth fails); accepts ${VAR} — use password: ${APP_PASSWORD} and inject via .env or shell env
}

type RunConfig struct {
	Mode        string `yaml:"mode"`         // doc: How selected targets are called: sequential | batched | parallel (default: sequential)
	BatchSize   int    `yaml:"batch_size"`   // doc: Used only with mode: batched
	MaxFailures int    `yaml:"max_failures"` // doc: Allowed target failures before halting
	OnFailure   string `yaml:"on_failure"`   // doc: Reaction when a target fails: halt | continue (default: halt)
	SuccessWhen string `yaml:"success_when"` // doc: How success is determined: auto | command | service | checks (default: auto)
	OnError     string `yaml:"on_error"`     // doc: Reaction on error: compensate | halt | continue (default: halt)
}

type StateConfig struct {
	Enabled  *bool  `yaml:"enabled"`    // doc: Record deployment state after each deploy (default: true)
	Dir      string `yaml:"dir"`        // doc: Remote directory for legacy file archives; default <target.dir>/.regis-states (rarely needed)
	LocalDir string `yaml:"local_dir"`  // doc: Local directory for state records (default: .regis-states)
	Keep     int    `yaml:"keep"`       // doc: State records to retain when --prune is used (default 5)
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
	Describe         string            // doc: Human-readable label shown in output
	Env              map[string]string // doc: Env vars for all cues in this scenario; overrides defaults.env, overridden by cue-level env
	Post             PostAction        // doc: Remote command (or restart:/reload: shorthand) run once if any remote cue changed
	Requires         StringOrList      // doc: Scenario names that must complete first (alias: needs) — deduplicated
	CompensationHint string            // doc: Hint shown by 'regis state hint' — describes how to roll back this scenario. Supports {prev_sha} placeholder.
	Cues             []CueRef          // doc: Ordered list of cues or scenario references
	Checks           []CueRef          // doc: Terminal acceptance phase — read-only probes, run after cues complete
	Compensate       []CueRef          // doc: Actions run on the remote when on_error: compensate triggers; ${STATE_ID} is available
	SuccessWhen      string            // doc: Override for run.success_when: auto | command | service | checks
	OnError          string            // doc: Override for run.on_error: compensate | halt | continue (default: halt)
	SourceFile       string            // which file this came from (set during load)
}

// CueCompensation declares what to do when this cue has already executed and a later
// cue fails (on_error: compensate). Having any compensation: field infers on_error: compensate
// for the scenario.
//
// Action/service natures: compensation: "shell cmd" or {shell, sudo} runs a compensation command
// in reverse execution order. compensation: defer skips the reverse phase and re-runs the cue's
// own shell after all regular compensations complete.
// compensation: interactive drops to an operator shell instead of running a preset command.
//
// File natures (binary/config/secret/render/pack): a compensation shell is allowed but warns at
// validate time — file state is not automatically restored; use `regis state hint` for guidance.
//
// Custom UnmarshalYAML handles the polymorphic forms.
type CueCompensation struct {
	Enabled     bool   // true = compensation active for this cue
	Shell       string // compensation shell command
	Sudo        bool   // run compensation with sudo
	Defer       bool   // skip reverse compensation; re-run cue shell after all regular compensations complete
	Interactive bool   // drop to operator shell instead of running a preset command
}

// CueRef is one entry in a cue list — either an inline cue or a scenario reference.
// If ScenarioRef != "", this is a reference. Custom UnmarshalYAML handles shell/cmd alias.
type CueRef struct {
	// Scenario reference fields
	ScenarioRef string       // yaml:"scenario"
	ScenarioName string      // set during validation — lowercase scenario name, used as service name fallback
	NarrowCue   string       // yaml:"cue" — narrow to one cue within a scenario reference
	CueNames    StringOrList // yaml:"cues" — narrow to subset

	// Inline cue fields
	Name            string            // doc: Unique within the scenario
	Nature          string            // doc: binary | config | secret | action | generate | render | pack | service — inferred: manager: set → service; managed_by: + src: → binary; git: true → pack
	Local           bool              // doc: true = run on local machine (action only); default = run on SSH target
	Src             StringOrList      // doc: Local source path — binary/secret: single file only; config: scalar path, glob string, or YAML list
	Git             bool              // doc: Use git-tracked files from HEAD commit as the pack source (git ls-tree -r HEAD) — mutually exclusive with src:; .regisignore still applied; nature: pack inferred (pack only)
	Dest            string            // doc: Remote destination; folder (trailing /) when src is glob or list
	Shell           string            // doc: Shell command to execute (canonical field; cmd: is an alias)
	Env             map[string]string // doc: Environment variables for local execution (key: value map)
	Post            PostAction        // doc: Remote command after cue, or restart:<svc> / reload:<svc> shorthand
	Preserve        StringOrList      // doc: Remote keys never overwritten during merge (secret only)
	Mode            string            // doc: Remote file permissions, e.g. "600"
	If              string            // doc: Boolean rule or probe — skip cue when false
	AffectsState    bool              // doc: Mark remote action cue as state-affecting so it is recorded in the deploy state (default false for actions)
	ChangedWhen     WhenExpr          // doc: Override change detection — defaults: binary/config/secret/render=MD5 diff, action=always changed, generate=always equal; expressions: "stdout contains X", "stdout !contains X", "stderr contains X", "exit == N", "exit != N"; or changed_when: true to force
	FailedWhen      WhenExpr          // doc: Override failure detection — defaults: action/generate=exit != 0, binary/config/secret=upload error; expressions: "exit != 0", "stdout contains ERROR", "stderr !contains OK"
	ContinueOnError bool              // doc: true = cue failure does not halt deployment (default false)
	Sudo            bool              // doc: Per-cue sudo override
	DiffMode        string            // doc: Diff strategy for file upload: auto | text | binary — pack default: binary; config/render default: auto
	Prune           *bool             // doc: Delete remote files absent from local set (pack default: true — safe because tier-1 uses managed manifest; render default: false; set prune: false to disable for pack)
	LocalDest       string            // doc: Local path for rendered output; $ARTIFACT_PATH points here during render and fetch (render only)
	Reverse         string            // doc: Shell command run during fetch to transform the downloaded artifact into local source files; $ARTIFACT_PATH = downloaded path (render only)

	// Service cue fields (nature: service — inferred when Manager is set)
	Manager     string            // doc: systemd | crontab (built-in), or any custom string (e.g. pm2); presence infers nature: service; for custom binaries prefer managed_by: on the binary cue
	Binary      string            // doc: Binary filename relative to target.dir (crontab); required for crontab services
	ServiceFile string            // doc: Local path to systemd unit file; uploaded to /etc/systemd/system/<basename>.service when changed; basename without extension is used as service name
	ServiceName string            // doc: Explicit systemd service unit name (e.g. nginx) — required when service_file is absent; used for systemctl is-enabled / deploy post-action
	Health      string            // doc: Health-check command (crontab watchdog)
	Commands    map[string]string // doc: Override or extend manager commands (start, stop, restart, reload, deploy, status). Template vars: {name}, {binary}, {dir}, {service_file}. Action refs: {restart}, {reload}, etc. expand to the pre-override base command

	// managed_by: merges binary upload + service registration into one cue.
	ManagedBy *ManagedBy // doc: combine binary upload with service registration in one cue — scalar: managed_by: crontab; struct: managed_by: {manager: systemd, service_file: path/to/unit, sudo: true}; crontab derives binary name from dest:; replaces a separate service cue for custom binaries; nature: binary inferred when src: is also present

	Compensation     *CueCompensation  // doc: Per-cue compensation on error — compensation: "cmd" runs a command; compensation: {shell, sudo} for sudo; compensation: defer re-runs the cue shell after all compensations; compensation: interactive drops to operator shell; file natures warn (no automated file restore — use regis state hint)
	CompensationHint string            // doc: Hint shown by 'regis state hint' for this cue — e.g. DB migration reversal command. Supports {prev_sha} placeholder.
}

// ManagedBy declares that a binary cue is also a managed service.
// Scalar form: managed_by: crontab
// Struct form: managed_by: {manager: systemd, service_file: path/to/unit.service, sudo: true}
// Custom UnmarshalYAML is in yaml.go.
type ManagedBy struct {
	Manager     string            `yaml:"manager"`      // doc: systemd | crontab (built-in), or any custom manager string
	ServiceFile string            `yaml:"service_file"` // doc: local path to systemd unit file; uploaded to /etc/systemd/system/<basename>.service when changed (systemd only)
	ServiceName string            `yaml:"service_name"` // doc: explicit service name — required when service_file absent (systemd); overrides dest-derived name (crontab)
	Health      string            `yaml:"health"`       // doc: health-check command (crontab watchdog)
	Commands    map[string]string `yaml:"commands"`     // doc: override manager commands (start/stop/restart/reload/deploy); template vars: {name}, {binary}, {dir}, {service_file}; action refs: {restart}, {reload}, etc. expand to pre-override base command
	Sudo        bool              `yaml:"sudo"`         // doc: run service-lifecycle operations (enable, daemon-reload, crontab install) with sudo
	Restart     *bool             `yaml:"restart"`      // doc: restart the service after binary upload when changed (default true); set restart: false to skip (e.g. blue-green deployments managed externally)
}
