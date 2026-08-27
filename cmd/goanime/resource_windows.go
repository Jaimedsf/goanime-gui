//go:build windows

package main

// Windows resource generation for the CLI binary.
//
// Without a resource section goanime.exe ships as a nameless console app:
// the generic shell icon, and an empty Properties → Details tab with no
// publisher, version or description. That is not only cosmetic — SmartScreen
// weighs an unsigned binary with no version metadata more harshly than an
// unsigned one that identifies itself.
//
// The manifest half matters more than the icon. `long-path-aware` is what
// lets a download path built from an anime title survive MAX_PATH, and
// `as invoker` states the privilege level explicitly so Windows' installer
// detection heuristics cannot decide an unsigned exe wants elevation.
//
// This mirrors cmd/goanime-gui, which does the same through its own
// winres/winres.json — see the note there about icon IDs. The CLI needs
// only ID 1: it has no window for Wails to icon.
//
// The generated rsrc_windows_amd64.syso is committed so a plain `go build`
// on a fresh clone produces an identified binary with no extra tooling. To
// change the icon or the version, edit winres/winres.json and re-run:
//
//	go generate ./cmd/goanime
//
//go:generate go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64
