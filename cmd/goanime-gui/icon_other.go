//go:build !windows

package main

// applyWindowIcon is a no-op outside Windows: Linux and macOS take the icon
// from options.App (linux.Options.Icon / mac.AboutInfo.Icon), so there is no
// resource table to patch after the window appears.
func applyWindowIcon() {}
