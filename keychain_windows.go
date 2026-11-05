package main

import (
	"errors"
	"syscall"
	"unsafe"
)

// Windows Credential Manager (generic credentials), called directly; the
// target name is the entry's name.

var (
	advapi32   = syscall.NewLazyDLL("advapi32.dll")
	credRead   = advapi32.NewProc("CredReadW")
	credWrite  = advapi32.NewProc("CredWriteW")
	credDelete = advapi32.NewProc("CredDeleteW")
	credFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	errorNotFound           = 1168
)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func notFound(err error) bool {
	var en syscall.Errno
	return errors.As(err, &en) && en == errorNotFound
}

func osKeychainGet(name string) (string, error) {
	target, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	var c *credential
	r, _, err := credRead.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		if notFound(err) {
			return "", errNoEntry
		}
		return "", err
	}
	defer credFree.Call(uintptr(unsafe.Pointer(c)))
	return string(unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize)), nil
}

func osKeychainSet(name, key string) error {
	target, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	user, _ := syscall.UTF16PtrFromString("zotero-backup")
	blob := []byte(key)
	c := credential{Type: credTypeGeneric, TargetName: target, UserName: user, Persist: credPersistLocalMachine,
		CredentialBlobSize: uint32(len(blob)), CredentialBlob: &blob[0]}
	if r, _, err := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return err
	}
	return nil
}

func osKeychainDelete(name string) error {
	target, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	if r, _, err := credDelete.Call(uintptr(unsafe.Pointer(target)), credTypeGeneric, 0); r == 0 {
		if notFound(err) {
			return errNoEntry
		}
		return err
	}
	return nil
}
