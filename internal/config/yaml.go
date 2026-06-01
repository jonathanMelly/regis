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
		Release     ReleaseConfig     `yaml:"release"`
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
	c.Release = p.Release
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
		Describe    string            `yaml:"describe"`
		Env         map[string]string `yaml:"env"`
		Post        PostAction        `yaml:"post"`
		Requires    StringOrList      `yaml:"requires"`
		Needs       StringOrList      `yaml:"needs"`
		Cues        []CueRef          `yaml:"cues"`
		Checks      []CueRef          `yaml:"checks"`
		Rollback    []CueRef          `yaml:"rollback"`
		SuccessWhen string            `yaml:"success_when"`
		OnError     string            `yaml:"on_error"`
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
	s.Rollback = p.Rollback
	s.SuccessWhen = p.SuccessWhen
	s.OnError = p.OnError
	if len(p.Requires) > 0 {
		s.Requires = p.Requires
	} else {
		s.Requires = p.Needs
	}
	return nil
}

// UnmarshalYAML for CueRollback handles the polymorphic forms:
//   - rollback: true         → {Enabled: true}
//   - rollback: false        → {Enabled: false}
//   - rollback: "shell cmd"  → {Enabled: true, Shell: "shell cmd"}
//   - rollback: {shell: ..., sudo: ...} → full object
func (r *CueRollback) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		switch value.Value {
		case "true":
			r.Enabled = true
		case "false":
			// explicit false: disabled
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

func (c *CueRef) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Scenario        string            `yaml:"scenario"`
		Cue             string            `yaml:"cue"`
		Cues            StringOrList      `yaml:"cues"`
		Name            string            `yaml:"name"`
		Nature          string            `yaml:"nature"`
		Local           bool              `yaml:"local"`
		Src             StringOrList      `yaml:"src"`
		Dest            string            `yaml:"dest"`
		Shell           string            `yaml:"shell"`
		Cmd             string            `yaml:"cmd"`
		Env             map[string]string `yaml:"env"`
		Post            PostAction        `yaml:"post"`
		Preserve        StringOrList      `yaml:"preserve"`
		Mode            string            `yaml:"mode"`
		If              string            `yaml:"if"`
		AffectsRelease  bool              `yaml:"affects_release"`
		ChangedWhen     WhenExpr          `yaml:"changed_when"`
		FailedWhen      WhenExpr          `yaml:"failed_when"`
		ContinueOnError bool              `yaml:"continue_on_error"`
		Sudo            bool              `yaml:"sudo"`
		Prune           *bool             `yaml:"prune"`
		LocalDest       string            `yaml:"local_dest"`
		Reverse         string            `yaml:"reverse"`
		Manager         string            `yaml:"manager"`
		Binary          string            `yaml:"binary"`
		ServiceFile     string            `yaml:"service_file"`
		Health          string            `yaml:"health"`
		Commands        map[string]string `yaml:"commands"`
		Rollback        *CueRollback      `yaml:"rollback"`
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
	c.AffectsRelease = p.AffectsRelease
	c.ChangedWhen = p.ChangedWhen
	c.FailedWhen = p.FailedWhen
	c.ContinueOnError = p.ContinueOnError
	c.Sudo = p.Sudo
	c.Prune = p.Prune
	c.LocalDest = p.LocalDest
	c.Reverse = p.Reverse
	c.Manager = p.Manager
	c.Binary = p.Binary
	c.ServiceFile = p.ServiceFile
	c.Health = p.Health
	c.Commands = p.Commands
	c.Rollback = p.Rollback
	// Infer nature: service when manager: is set and nature is not specified.
	if c.Nature == "" && c.Manager != "" {
		c.Nature = "service"
	}
	return nil
}
