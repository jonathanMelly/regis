// internal/cue/when.go
package cue

import (
	"fmt"
	"strconv"
	"strings"
)

// EvalWhenExpr evaluates a string expression form of changed_when / failed_when.
// Supported patterns:
//
//	stdout contains <text>
//	stdout !contains <text>
//	stderr contains <text>
//	stderr !contains <text>
//	exit == <n>
//	exit != <n>
func EvalWhenExpr(expr, stdout, stderr string, exitCode int) (bool, error) {
	expr = strings.TrimSpace(expr)

	for _, src := range []struct{ prefix, val string }{
		{"stdout", stdout},
		{"stderr", stderr},
	} {
		if rest, ok := strings.CutPrefix(expr, src.prefix+" contains "); ok {
			return strings.Contains(src.val, rest), nil
		}
		if rest, ok := strings.CutPrefix(expr, src.prefix+" !contains "); ok {
			return !strings.Contains(src.val, rest), nil
		}
	}

	if rest, ok := strings.CutPrefix(expr, "exit == "); ok {
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return false, fmt.Errorf("invalid exit code in expression %q: %w", expr, err)
		}
		return exitCode == n, nil
	}
	if rest, ok := strings.CutPrefix(expr, "exit != "); ok {
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return false, fmt.Errorf("invalid exit code in expression %q: %w", expr, err)
		}
		return exitCode != n, nil
	}

	return false, fmt.Errorf("unknown when expression %q (supported: stdout/stderr contains, exit == N, exit != N)", expr)
}
