//go:build !darwin && !linux && !windows

package main

import "errors"

var errNoKeychain = errors.New("no keychain support on this system: set ZOTERO_API_KEY")

func osKeychainGet(string) (string, error) { return "", errNoKeychain }
func osKeychainSet(string, string) error   { return errNoKeychain }
func osKeychainDelete(string) error        { return errNoKeychain }
