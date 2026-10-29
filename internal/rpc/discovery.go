// Package rpc implements the authenticated loopback protocol for IDE clients.
package rpc

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
)

type discovery struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func randomID() string { return rand.Text() }
func publish(path string, d discovery) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := protect(filepath.Dir(path), true); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".runtime-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = protect(tmp, false); err != nil {
		f.Close()
		return err
	}
	data, _ := json.Marshal(d)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replace(tmp, path)
}
