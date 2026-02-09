package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const (
	credentialStoreModeAuto      = "auto"
	credentialStoreModeFile      = "file"
	credentialStoreModeEphemeral = "ephemeral"

	defaultCredentialStoreMode = credentialStoreModeAuto
	credentialFileVersion      = 1
	credentialFileSuffix       = "_credentials.json"
)

type credentialRecord struct {
	Type         string `json:"type,omitempty"`
	Token        string `json:"token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiryUnix   int64  `json:"expiry_unix,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type credentialFile struct {
	Version     int                         `json:"version"`
	Credentials map[string]credentialRecord `json:"credentials,omitempty"`
}

type credentialStore struct {
	path string
	data credentialFile
}

func normalizeCredentialStoreMode(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "", credentialStoreModeAuto:
		return credentialStoreModeAuto
	case credentialStoreModeFile:
		return credentialStoreModeFile
	case credentialStoreModeEphemeral:
		return credentialStoreModeEphemeral
	default:
		return credentialStoreModeAuto
	}
}

func loadOrInitCredentialStore(appName, mode string) (*credentialStore, error) {
	mode = normalizeCredentialStoreMode(mode)
	if mode == credentialStoreModeEphemeral {
		return nil, nil
	}

	path, err := credentialPath(appName)
	if err != nil {
		return nil, err
	}
	store := &credentialStore{
		path: path,
		data: credentialFile{
			Version:     credentialFileVersion,
			Credentials: map[string]credentialRecord{},
		},
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("credential store: read %q: %w", path, err)
		}
		if err := store.save(); err != nil {
			return nil, err
		}
		return store, nil
	}
	var loaded credentialFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("credential store: parse %q: %w", path, err)
	}
	if loaded.Version <= 0 {
		loaded.Version = credentialFileVersion
	}
	if loaded.Credentials == nil {
		loaded.Credentials = map[string]credentialRecord{}
	}
	store.data = loaded
	if err := ensureFilePermission(path, 0o600); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *credentialStore) Upsert(ref string, record credentialRecord) error {
	if s == nil {
		return nil
	}
	key := normalizeCredentialRef(ref)
	if key == "" {
		return fmt.Errorf("credential store: credential ref is required")
	}
	record.Token = strings.TrimSpace(record.Token)
	record.RefreshToken = strings.TrimSpace(record.RefreshToken)
	record.Type = strings.TrimSpace(record.Type)
	if record.Token == "" && record.RefreshToken == "" {
		delete(s.data.Credentials, key)
		return s.save()
	}
	if record.UpdatedAt == "" {
		record.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	s.data.Credentials[key] = record
	return s.save()
}

func (s *credentialStore) Get(ref string) (credentialRecord, bool) {
	if s == nil {
		return credentialRecord{}, false
	}
	key := normalizeCredentialRef(ref)
	if key == "" {
		return credentialRecord{}, false
	}
	record, ok := s.data.Credentials[key]
	if !ok {
		return credentialRecord{}, false
	}
	record.Token = strings.TrimSpace(record.Token)
	record.RefreshToken = strings.TrimSpace(record.RefreshToken)
	if record.Token == "" && record.RefreshToken == "" {
		return credentialRecord{}, false
	}
	return record, true
}

func (s *credentialStore) save() error {
	if s == nil {
		return nil
	}
	if s.data.Version <= 0 {
		s.data.Version = credentialFileVersion
	}
	if s.data.Credentials == nil {
		s.data.Credentials = map[string]credentialRecord{}
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("credential store: create dir: %w", err)
	}
	if err := ensureDirPermission(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("credential store: marshal: %w", err)
	}
	raw = append(raw, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("credential store: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("credential store: rename: %w", err)
	}
	return ensureFilePermission(s.path, 0o600)
}

func credentialPath(appName string) (string, error) {
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	name := normalizedAppName(appName)
	return filepath.Join(root, name+credentialFileSuffix), nil
}

func ensureFilePermission(path string, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode().Perm() == perm {
		return nil
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("credential store: chmod %q: %w", path, err)
	}
	return nil
}

func ensureDirPermission(path string, perm os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() == perm {
		return nil
	}
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("credential store: chmod dir %q: %w", path, err)
	}
	return nil
}

func defaultCredentialRef(provider, baseURL string) string {
	providerPart := normalizeCredentialRef(provider)
	if providerPart == "" {
		return ""
	}
	hostPart := ""
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil {
		host := strings.TrimSpace(parsed.Hostname())
		port := strings.TrimSpace(parsed.Port())
		if host != "" {
			hostPart = normalizeCredentialRef(host)
			if port != "" {
				hostPart = hostPart + "_" + normalizeCredentialRef(port)
			}
		}
	}
	if hostPart == "" {
		return providerPart
	}
	return providerPart + "_" + hostPart
}

func normalizeCredentialRef(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(input))
	lastUnderscore := false
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func credentialRefForProvider(cfg modelproviders.Config) string {
	if ref := normalizeCredentialRef(cfg.Auth.CredentialRef); ref != "" {
		return ref
	}
	if ref := defaultCredentialRef(cfg.Provider, cfg.BaseURL); ref != "" {
		return ref
	}
	return normalizeCredentialRef(cfg.Alias)
}

func hydrateProviderAuthToken(cfg modelproviders.Config, credentials *credentialStore) modelproviders.Config {
	if credentials == nil {
		return cfg
	}
	if strings.TrimSpace(cfg.Auth.Token) != "" {
		return cfg
	}
	if env := strings.TrimSpace(cfg.Auth.TokenEnv); env != "" && strings.TrimSpace(os.Getenv(env)) != "" {
		return cfg
	}
	ref := credentialRefForProvider(cfg)
	if ref == "" {
		return cfg
	}
	stored, ok := credentials.Get(ref)
	if !ok {
		return cfg
	}
	token := strings.TrimSpace(stored.Token)
	if token == "" {
		return cfg
	}
	cfg.Auth.CredentialRef = ref
	cfg.Auth.Token = token
	if cfg.Auth.Type == "" && strings.TrimSpace(stored.Type) != "" {
		cfg.Auth.Type = modelproviders.AuthType(strings.TrimSpace(stored.Type))
	}
	return cfg
}

func migrateInlineProviderTokens(configStore *appConfigStore, credentials *credentialStore) error {
	if configStore == nil || credentials == nil {
		return nil
	}
	changed := false
	for i := range configStore.data.Providers {
		rec := &configStore.data.Providers[i]
		token := strings.TrimSpace(rec.Auth.Token)
		if token == "" {
			continue
		}
		ref := normalizeCredentialRef(rec.Auth.CredentialRef)
		if ref == "" {
			ref = defaultCredentialRef(rec.Provider, rec.BaseURL)
		}
		if ref == "" {
			ref = normalizeCredentialRef(rec.Alias)
		}
		if ref == "" {
			continue
		}
		if err := credentials.Upsert(ref, credentialRecord{
			Type:  strings.TrimSpace(rec.Auth.Type),
			Token: token,
		}); err != nil {
			return err
		}
		rec.Auth.CredentialRef = ref
		rec.Auth.Token = ""
		changed = true
	}
	if !changed {
		return nil
	}
	return configStore.save()
}
