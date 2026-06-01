// internal/runner/topo.go
package runner

import (
	"fmt"
	"git.disroot.org/jmy/regis/internal/config"
)

// TopoSort returns the ordered list of scenario names to execute for requested,
// including all transitive requires, deduplicated, in dependency order.
func TopoSort(scenarios map[string]config.Scenario, requested []string) ([]string, error) {
	var order []string
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var visit func(name string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if inStack[name] {
			return fmt.Errorf("cycle detected involving scenario %q", name)
		}
		inStack[name] = true
		sc, ok := scenarios[name]
		if !ok {
			return fmt.Errorf("scenario %q not defined", name)
		}
		for _, req := range sc.Requires {
			if err := visit(req); err != nil {
				return err
			}
		}
		delete(inStack, name)
		visited[name] = true
		order = append(order, name)
		return nil
	}

	for _, name := range requested {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}
