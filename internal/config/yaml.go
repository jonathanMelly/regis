// internal/config/yaml.go
package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
)

func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Targets     []Target          `yaml:"targets"`
		Defaults    DefaultsConfig    `yaml:"defaults"`
		Pre         []PrePost         `yaml:"pre"`
		Post        []PrePost         `yaml:"post"`
		Includes    []string          `yaml:"includes"`
		Run         RunConfig         `yaml:"run"`
		State       StateConfig       `yaml:"state"`
		Concurrency ConcurrencyConfig `yaml:"concurrency"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	c.Targets = p.Targets
	c.Defaults = p.Defaults
	c.Pre = p.Pre
	c.Post = p.Post
	c.Includes = p.Includes
	c.Run = p.Run
	c.State = p.State
	c.Concurrency = p.Concurrency

	c.Scenarios = make(map[string]Scenario)
	for i := 0; i+1 < len(value.Content); i += 2 {
		if value.Content[i].Value != "scenarios" {
			continue
		}
		node := value.Content[i+1]
		for j := 0; j+1 < len(node.Content); j += 2 {
			key := node.Content[j].Value
			var sc Scenario
			if err := node.Content[j+1].Decode(&sc); err != nil {
				return fmt.Errorf("scenario %q: %w", key, err)
			}
			c.Scenarios[key] = sc
			c.ScenarioNames = append(c.ScenarioNames, key)
		}
	}
	return nil
}

func (t *Target) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Name   string    `yaml:"name"`
		Host   string    `yaml:"host"`
		User   string    `yaml:"user"`
		Port   yaml.Node `yaml:"port"`
		Dir    string    `yaml:"dir"`
		Sudo   bool      `yaml:"sudo"`
		Dotenv string    `yaml:"dotenv"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	t.Name = p.Name
	t.Host = p.Host
	t.User = p.User
	t.Dir = p.Dir
	t.Sudo = p.Sudo
	t.Dotenv = p.Dotenv
	// Port accepts both integer (22) and string ("${NODE_PORT}") YAML values.
	if p.Port.Kind == yaml.ScalarNode {
		t.Port = p.Port.Value
	}
	return nil
}

func (s *StringOrList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = StringOrList{value.Value}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

func (p *PrePost) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		p.Cmd = value.Value
		return nil
	}
	type plain struct {
		Cmd   string `yaml:"cmd"`
		Local bool   `yaml:"local"`
		If    string `yaml:"if"`
	}
	var pl plain
	if err := value.Decode(&pl); err != nil {
		return err
	}
	p.Cmd = pl.Cmd
	p.Local = pl.Local
	p.If = pl.If
	return nil
}

func (p *PostAction) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		p.Cmd = value.Value
		return nil
	}
	type plain struct {
		Cmd  string `yaml:"cmd"`
		Sudo bool   `yaml:"sudo"`
	}
	var pl plain
	if err := value.Decode(&pl); err != nil {
		return err
	}
	p.Cmd = pl.Cmd
	p.Sudo = pl.Sudo
	return nil
}

func (w *WhenExpr) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!bool" {
			b := value.Value == "true"
			w.BoolLiteral = &b
			return nil
		}
		w.Expression = value.Value
		return nil
	case yaml.MappingNode:
		type plain struct {
			Shell    string `yaml:"shell"`
			Scenario string `yaml:"scenario"`
			Cue      string `yaml:"cue"`
		}
		var pl plain
		if err := value.Decode(&pl); err != nil {
			return err
		}
		// Count how many exclusive fields are set
		set := 0
		if pl.Shell != "" {
			set++
		}
		if pl.Scenario != "" {
			set++
		}
		if set > 1 {
			return fmt.Errorf("when expression: 'shell:' and 'scenario:' are mutually exclusive")
		}
		if pl.Cue != "" && pl.Scenario == "" {
			return fmt.Errorf("when expression: 'cue:' requires 'scenario:' to also be set")
		}
		w.Shell = pl.Shell
		w.ScenarioRef = pl.Scenario
		w.CueRef = pl.Cue
		return nil
	}
	return fmt.Errorf("unexpected YAML node kind %v for when expression", value.Kind)
}

func (s *Scenario) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Describe         string            `yaml:"describe"`
		Env              map[string]string `yaml:"env"`
		Post             PostAction        `yaml:"post"`
		Requires         StringOrList      `yaml:"requires"`
		Needs            StringOrList      `yaml:"needs"`
		CompensationHint string            `yaml:"compensation_hint"`
		Cues             []CueRef          `yaml:"cues"`
		Checks           []CueRef          `yaml:"checks"`
		Compensate       []CueRef          `yaml:"compensate"`
		SuccessWhen      string            `yaml:"success_when"`
		OnError          string            `yaml:"on_error"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	s.Describe = p.Describe
	s.Env = p.Env
	s.Post = p.Post
	s.Cues = p.Cues
	s.Checks = p.Checks
	s.Compensate = p.Compensate
	s.CompensationHint = p.CompensationHint
	s.SuccessWhen = p.SuccessWhen
	s.OnError = p.OnError
	if len(p.Requires) > 0 {
		s.Requires = p.Requires
	} else {
		s.Requires = p.Needs
	}
	return nil
}

// UnmarshalYAML for CueCompensation handles the polymorphic forms:
//   - compensation: true              → {Enabled: true}
//   - compensation: false             → {Enabled: false}
//   - compensation: "shell cmd"       → {Enabled: true, Shell: "shell cmd"}
//   - compensation: defer             → {Enabled: true, Defer: true}
//   - compensation: interactive       → {Enabled: true, Interactive: true}
//   - compensation: {shell: ..., sudo: ...} → full object
func (r *CueCompensation) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		switch value.Value {
		case "true":
			r.Enabled = true
		case "false":
			// explicit false: disabled
		case "defer":
			r.Enabled = true
			r.Defer = true
		case "interactive":
			r.Enabled = true
			r.Interactive = true
		default:
			r.Enabled = true
			r.Shell = value.Value
		}
		return nil
	}
	type plain struct {
		Shell string `yaml:"shell"`
		Sudo  bool   `yaml:"sudo"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	r.Enabled = true
	r.Shell = p.Shell
	r.Sudo = p.Sudo
	return nil
}

func (m *ManagedBy) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		m.Manager = value.Value
		return nil
	}
	type plain struct {
		Manager     string            `yaml:"manager"`
		ServiceFile string            `yaml:"service_file"`
		ServiceName string            `yaml:"service_name"`
		Health      string            `yaml:"health"`
		Commands    map[string]string `yaml:"commands"`
		Sudo        bool              `yaml:"sudo"`
		Restart     *bool             `yaml:"restart"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	m.Manager = p.Manager
	m.ServiceFile = p.ServiceFile
	m.ServiceName = p.ServiceName
	m.Health = p.Health
	m.Commands = p.Commands
	m.Sudo = p.Sudo
	m.Restart = p.Restart
	return nil
}

func (c *CueRef) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Scenario        string            `yaml:"scenario"`
		Cue             string            `yaml:"cue"`
		Cues            StringOrList      `yaml:"cues"`
		Name            string            `yaml:"name"`
		Nature          string            `yaml:"nature"`
		Local           bool              `yaml:"local"`
		Src             StringOrList      `yaml:"src"`
		Git             bool              `yaml:"git"`
		Dest            string            `yaml:"dest"`
		Shell           string            `yaml:"shell"`
		Cmd             string            `yaml:"cmd"`
		Env             map[string]string `yaml:"env"`
		Post            PostAction        `yaml:"post"`
		Preserve        StringOrList      `yaml:"preserve"`
		Mode            string            `yaml:"mode"`
		If              string            `yaml:"if"`
		AffectsState    bool              `yaml:"affects_state"`
		ChangedWhen     WhenExpr          `yaml:"changed_when"`
		FailedWhen      WhenExpr          `yaml:"failed_when"`
		ContinueOnError bool              `yaml:"continue_on_error"`
		Sudo            bool              `yaml:"sudo"`
		DiffMode        string            `yaml:"diff_mode"`
		Prune           *bool             `yaml:"prune"`
		LocalDest       string            `yaml:"local_dest"`
		Reverse         string            `yaml:"reverse"`
		Manager         string            `yaml:"manager"`
		Binary          string            `yaml:"binary"`
		ServiceFile     string            `yaml:"service_file"`
		ServiceName     string            `yaml:"service_name"`
		Health          string            `yaml:"health"`
		Commands        map[string]string `yaml:"commands"`
		ManagedBy       *ManagedBy        `yaml:"managed_by"`
		Compensation    *CueCompensation  `yaml:"compensation"`
		CompensationHint string           `yaml:"compensation_hint"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	if p.Shell != "" && p.Cmd != "" {
		return fmt.Errorf("cue %q: cannot specify both 'shell:' and 'cmd:'", p.Name)
	}
	c.ScenarioRef = p.Scenario
	c.NarrowCue = p.Cue
	c.CueNames = p.Cues
	c.Name = p.Name
	c.Nature = p.Nature
	c.Local = p.Local
	c.Src = p.Src
	c.Git = p.Git
	c.Dest = p.Dest
	c.Shell = p.Shell
	if p.Cmd != "" {
		c.Shell = p.Cmd
	}
	c.Env = p.Env
	c.Post = p.Post
	c.Preserve = p.Preserve
	c.Mode = p.Mode
	c.If = p.If
	c.AffectsState = p.AffectsState
	c.ChangedWhen = p.ChangedWhen
	c.FailedWhen = p.FailedWhen
	c.ContinueOnError = p.ContinueOnError
	c.Sudo = p.Sudo
	c.DiffMode = p.DiffMode
	c.Prune = p.Prune
	c.LocalDest = p.LocalDest
	c.Reverse = p.Reverse
	c.Manager = p.Manager
	c.Binary = p.Binary
	c.ServiceFile = p.ServiceFile
	c.ServiceName = p.ServiceName
	c.Health = p.Health
	c.Commands = p.Commands
	c.ManagedBy = p.ManagedBy
	c.Compensation = p.Compensation
	c.CompensationHint = p.CompensationHint
	// Infer nature: service when manager: is set and nature is not specified.
	if c.Nature == "" && c.Manager != "" {
		c.Nature = "service"
	}
	// Infer nature: binary when managed_by: is set with src: present.
	if c.Nature == "" && c.ManagedBy != nil && len(c.Src) > 0 {
		c.Nature = "binary"
	}
	return nil
}
