//go:build !darwin && !linux

package hostcheck

func systemReaders() Readers { return Readers{} }
