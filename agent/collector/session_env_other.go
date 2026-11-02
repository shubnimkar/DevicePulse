//go:build !linux

package collector

// AdoptSessionEnv is only needed for the Linux per-user window service.
func AdoptSessionEnv() {}
