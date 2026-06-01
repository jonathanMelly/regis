// internal/runner/postaction.go
package runner

import "git.disroot.org/jmy/regis/internal/cue"

// DeduplicatePostActions deduplicates by command string.
// sudo:true wins when the same command appears with different sudo values.
// First-seen position in the sequence is kept (spec §4.3).
func DeduplicatePostActions(actions []cue.PostAction) []cue.PostAction {
	seen := make(map[string]int) // cmd -> index in result
	var result []cue.PostAction

	for _, a := range actions {
		if idx, ok := seen[a.Cmd]; ok {
			// Upgrade to sudo=true if more permissive
			if a.Sudo && !result[idx].Sudo {
				result[idx].Sudo = true
			}
			continue
		}
		seen[a.Cmd] = len(result)
		result = append(result, a)
	}
	return result
}
