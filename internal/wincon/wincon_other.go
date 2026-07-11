//go:build !windows

package wincon

// AttachParent is a no-op off Windows; stdio is already wired to the
// terminal. Exists so the rest of the code can call it unconditionally.
func AttachParent() {}
