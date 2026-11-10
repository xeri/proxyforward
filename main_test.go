package main

import "testing"

// TestDeepLinkArg pins how a pxf:// protocol launch is told apart from a normal CLI
// invocation. The OS hands the app the link as its sole argument; a subcommand (even
// `pair <code>`, which also carries a pxf:// string) must never be mistaken for one.
func TestDeepLinkArg(t *testing.T) {
	if got := deepLinkArg([]string{"pxf://gw:8474/v1/pair/tok#sha256:x"}); got == "" {
		t.Error("a lone pxf:// argument should be treated as a deep link")
	}
	if got := deepLinkArg([]string{"  pxf://gw:8474/v1/pair/tok  "}); got == "" {
		t.Error("a whitespace-padded pxf:// argument should still be a deep link")
	}
	if got := deepLinkArg([]string{"pair", "pxf://gw:8474/v1/pair/tok"}); got != "" {
		t.Errorf("the pair subcommand must not be read as a deep link, got %q", got)
	}
	if got := deepLinkArg([]string{"gateway"}); got != "" {
		t.Errorf("a subcommand is not a deep link, got %q", got)
	}
	if got := deepLinkArg(nil); got != "" {
		t.Errorf("no arguments is not a deep link, got %q", got)
	}
}
