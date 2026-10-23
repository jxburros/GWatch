//go:build !windows

package main

// shareStatusFile has nothing to do outside Windows: the status file's mode
// bits already make it readable by anyone who can reach its directory.
func shareStatusFile(string) {}
