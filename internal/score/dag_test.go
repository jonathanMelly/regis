// internal/score/dag_test.go
package score_test

import (
	"strings"
	"testing"

	"git.disroot.org/jmy/regis/internal/config"
	"git.disroot.org/jmy/regis/internal/score"
)

func makeTestConfig() *config.Config {
	return &config.Config{
		Targets: []config.Target{{Name: "prod", Host: "h", User: "u", Dir: "/opt"}},
		Scenarios: map[string]config.Scenario{
			"checks": {Describe: "Pre-deploy checks", Cues: []config.CueRef{
				{Name: "go", Nature: "action", Local: true, Shell: "go version"},
			}},
			"build": {
				Describe: "Build",
				Requires: config.StringOrList{"checks"},
				Cues:     []config.CueRef{{Name: "bins", Nature: "action", Local: true}},
			},
			"saver": {
				Describe: "Saver daemon",
				Requires: config.StringOrList{"build"},
				Cues: []config.CueRef{
					{Name: "saver", Nature: "service", Manager: "crontab", Binary: "saver"},
					{Name: "bin", Nature: "binary", Src: config.StringOrList{"bin/saver"}, Dest: "saver"},
				},
			},
			"Full": {Describe: "Full deploy", Cues: []config.CueRef{
				{ScenarioRef: "saver"},
			}},
		},
		ScenarioNames: []string{"Full", "checks", "build", "saver"},
	}
}

func TestRenderDAG_containsScenarioNames(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderDAG(cfg)
	for _, name := range []string{"checks", "build", "saver", "Full"} {
		if !strings.Contains(out, name) {
			t.Errorf("DAG missing scenario %q", name)
		}
	}
}

func TestRenderDAG_containsLegend(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderDAG(cfg)
	if !strings.Contains(out, "legend") {
		t.Errorf("DAG missing legend section")
	}
}

func TestRenderTree_treeBranches(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderTree(cfg, nil, "yaml")
	if !strings.Contains(out, "├─") && !strings.Contains(out, "└─") {
		t.Error("RenderTree must contain tree branch characters ├─ or └─")
	}
	// requires shown as ** at bottom of scenario tree
	if !strings.Contains(out, "**") {
		t.Error("RenderTree must show ** requires lines for scenarios with requires")
	}
	for _, name := range []string{"checks", "build", "saver", "Full"} {
		if !strings.Contains(out, name) {
			t.Errorf("RenderTree missing scenario %q", name)
		}
	}
}

func TestRenderTree_uppercaseFirst(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderTree(cfg, nil, "yaml")
	// "Full" starts with uppercase → public entry-point, must appear before lowercase "checks"
	checksPos := strings.Index(out, "checks")
	fullPos := strings.Index(out, "Full")
	if checksPos < 0 || fullPos < 0 {
		t.Fatal("missing Full or checks in output")
	}
	if fullPos > checksPos {
		t.Error("uppercase scenario 'Full' must appear before lowercase 'checks'")
	}
}

func TestRenderTree_filter(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderTree(cfg, []string{"checks", "build"}, "yaml")
	if !strings.Contains(out, "checks") || !strings.Contains(out, "build") {
		t.Error("filter must include requested scenarios")
	}
	if strings.Contains(out, "Full") {
		t.Error("filter must exclude unrequested scenario 'Full'")
	}
}

func TestRenderFlow_pipelineStages(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderFlow(cfg, nil, "yaml")
	if !strings.Contains(out, "stage") {
		t.Error("RenderFlow must contain 'stage' labels")
	}
	if !strings.Contains(out, "pipeline") {
		t.Error("RenderFlow must contain 'pipeline' section")
	}
	if !strings.Contains(out, "scenarios") {
		t.Error("RenderFlow must contain 'scenarios' detail section")
	}
	// Collect all lines belonging to each stage block (first labeled line + continuation lines).
	lines := strings.Split(out, "\n")
	stage0block, stage1block := "", ""
	inStage0, inStage1 := false, false
	for _, l := range lines {
		if strings.Contains(l, "stage 0") {
			inStage0, inStage1 = true, false
			stage0block += l + "\n"
			continue
		}
		if strings.Contains(l, "stage 1") {
			inStage0, inStage1 = false, true
			stage1block += l + "\n"
			continue
		}
		if strings.Contains(l, "stage ") {
			inStage0, inStage1 = false, false
		}
		if inStage0 {
			stage0block += l + "\n"
		} else if inStage1 {
			stage1block += l + "\n"
		}
	}
	if !strings.Contains(stage0block, "checks") {
		t.Errorf("stage 0 block must contain 'checks', got:\n%s", stage0block)
	}
	if !strings.Contains(stage1block, "build") {
		t.Errorf("stage 1 block must contain 'build', got:\n%s", stage1block)
	}
}

func TestRenderCompact_oneLinerPerScenario(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderCompact(cfg, nil, "yaml")
	for _, name := range []string{"checks", "build", "saver", "Full"} {
		if !strings.Contains(out, name) {
			t.Errorf("RenderCompact missing scenario %q", name)
		}
	}
	if !strings.Contains(out, "legend") {
		t.Error("RenderCompact must contain legend")
	}
}

func TestRenderMermaid_graphTD(t *testing.T) {
	cfg := makeTestConfig()
	out := score.RenderMermaid(cfg)
	if !strings.HasPrefix(strings.TrimSpace(out), "graph TD") {
		t.Errorf("Mermaid must start with 'graph TD', got:\n%s", out)
	}
	if !strings.Contains(out, "checks") || !strings.Contains(out, "build") {
		t.Error("Mermaid missing scenario names")
	}
}

func TestRenderDAG_legendSymbols(t *testing.T) {
	// natureSymbol is private — verify all symbols appear in the legend section.
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"all": {Cues: []config.CueRef{
				{Name: "b", Nature: "binary"},
				{Name: "c", Nature: "config"},
				{Name: "s", Nature: "secret"},
				{Name: "r", Nature: "render"},
				{Name: "g", Nature: "generate"},
				{Name: "a", Nature: "action"},
				{Name: "svc", Nature: "service", Manager: "systemd"},
			}},
		},
		ScenarioNames: []string{"all"},
	}
	out := score.RenderDAG(cfg)
	for _, sym := range []string{"!", "#", "§", "~", "+", "@", "&"} {
		if !strings.Contains(out, sym) {
			t.Errorf("legend missing symbol %q for its nature; output:\n%s", sym, out)
		}
	}
}

func TestRenderCompact_allNatures(t *testing.T) {
	cfg := &config.Config{
		Scenarios: map[string]config.Scenario{
			"deploy": {Cues: []config.CueRef{
				{Name: "app", Nature: "binary"},
				{Name: "cfg", Nature: "config"},
				{Name: "env", Nature: "secret"},
				{Name: "tmpl", Nature: "render"},
				{Name: "gen", Nature: "generate"},
				{Name: "post", Nature: "action"},
				{Name: "svc", Nature: "service", Manager: "systemd"},
			}},
		},
		ScenarioNames: []string{"deploy"},
	}
	out := score.RenderCompact(cfg, nil, "yaml")
	if !strings.Contains(out, "deploy") {
		t.Errorf("RenderCompact must show scenario name; got:\n%s", out)
	}
}
