package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func cleanupDelegatedChildSessionCopies(eventStoreDir string, index *sessionIndex, workspace workspaceContext) error {
	entries, err := os.ReadDir(eventStoreDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := strings.TrimSpace(entry.Name())
		if sessionID == "" || strings.HasPrefix(sessionID, ".") {
			continue
		}
		dir := filepath.Join(eventStoreDir, sessionID)
		ok, err := isDelegatedChildSessionDir(filepath.Join(dir, "events.jsonl"), sessionID)
		if err != nil || !ok {
			continue
		}
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return removeErr
		}
		if index != nil {
			_ = index.DeleteWorkspaceSession(workspace.Key, sessionID)
		}
	}
	return nil
}

func isDelegatedChildSessionDir(eventsPath, sessionID string) (bool, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var ev session.Event
		if err := dec.Decode(&ev); err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		meta, ok := runtime.DelegationMetadataFromEvent(&ev)
		if !ok {
			continue
		}
		if strings.TrimSpace(meta.ParentSessionID) == "" {
			continue
		}
		if strings.TrimSpace(meta.ChildSessionID) != strings.TrimSpace(sessionID) {
			continue
		}
		return true, nil
	}
}
