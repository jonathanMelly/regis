// internal/ssh/export_test.go
// Exports internal helpers for white-box unit tests.
package ssh

// ConnWithHome returns a minimal Conn with the given cached home value.
// Use in tests to exercise ExpandHome without a real SSH connection.
func ConnWithHome(home string) *Conn { return &Conn{home: home} }
