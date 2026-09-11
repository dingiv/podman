//go:build linux

package main

// easytidy: machine feature removed; no provider to report.
func getProvider() (string, error) { return "native", nil }
