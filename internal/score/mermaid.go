// internal/score/mermaid.go
package score

import (
	"fmt"
	"strings"

	"git.disroot.org/jmy/regis/internal/config"
)

// RenderMermaid generates a Mermaid graph TD block from the scenario dependency graph.
func RenderMermaid(cfg *config.Config) string {
	var sb strings.Builder
	sb.WriteString("graph TD\n")

	names := SortedScenarioNames(cfg, "yaml")
	for _, name := range names {
		sc := cfg.Scenarios[name]
		for _, req := range sc.Requires {
			fmt.Fprintf(&sb, "  %s --> %s\n", req, name)
		}
		for _, cr := range sc.Cues {
			if cr.ScenarioRef != "" {
				fmt.Fprintf(&sb, "  %s -.-> %s\n", name, cr.ScenarioRef)
			}
		}
	}
	return sb.String()
}

// RenderMermaidFile wraps the graph in a markdown code block for .regis/score.md.
func RenderMermaidFile(cfg *config.Config) string {
	return "```mermaid\n" + RenderMermaid(cfg) + "```\n"
}
