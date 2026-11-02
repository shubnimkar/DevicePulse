//go:build !linux

package main

// maintainWindowService only applies to the Linux per-user window unit.
func maintainWindowService() {}
