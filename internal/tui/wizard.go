// internal/tui/wizard.go
package tui

// WizardStep identifies which field is being collected.
type WizardStep int

const (
	StepHost WizardStep = iota
	StepUser
	StepPort
	StepDir
	StepScenarioName
	StepDone
)

// WizardModel holds the state of the creation wizard.
// It is a pure value type — no side effects — for testability.
type WizardModel struct {
	step     WizardStep
	host     string
	user     string
	port     string
	dir      string
	scenario string
}

func NewWizardModel() WizardModel {
	return WizardModel{step: StepHost, port: "22"}
}

func (m WizardModel) Step() WizardStep { return m.step }
func (m WizardModel) Host() string     { return m.host }
func (m WizardModel) User() string     { return m.user }
func (m WizardModel) Port() string     { return m.port }
func (m WizardModel) Dir() string      { return m.dir }
func (m WizardModel) Scenario() string { return m.scenario }

func (m WizardModel) SetHost(v string) WizardModel     { m.host = v; m.step = StepUser; return m }
func (m WizardModel) SetUser(v string) WizardModel     { m.user = v; m.step = StepPort; return m }
func (m WizardModel) SetPort(v string) WizardModel     { m.port = v; m.step = StepDir; return m }
func (m WizardModel) SetDir(v string) WizardModel      { m.dir = v; m.step = StepScenarioName; return m }
func (m WizardModel) SetScenario(v string) WizardModel { m.scenario = v; m.step = StepDone; return m }

// GenerateYAML produces the minimal regis.yml content from collected values.
func (m WizardModel) GenerateYAML() string {
	port := m.port
	if port == "" {
		port = "22"
	}
	return "targets:\n" +
		"  - name: prod\n" +
		"    host: " + m.host + "\n" +
		"    user: " + m.user + "\n" +
		"    port: " + port + "\n" +
		"    dir: " + m.dir + "\n\n" +
		"scenarios:\n" +
		"  " + m.scenario + ":\n" +
		"    describe: \"My first scenario\"\n" +
		"    cues:\n" +
		"      - name: hello\n" +
		"        nature: action\n" +
		"        cmd: echo 'Hello from regis!'\n"
}
