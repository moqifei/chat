package digitaltwin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const EnvConfigStorePath = "OPENIM_DIGITAL_TWIN_CONFIG_STORE"

type UserConfigStore interface {
	LoadUserConfig(ctx context.Context, userID string) (UserConfig, bool, error)
	SaveUserConfig(ctx context.Context, userID string, cfg UserConfig, now time.Time) error
}

type configStoreFile struct {
	Version   int                   `json:"version"`
	UpdatedAt int64                 `json:"updatedAt"`
	Users     map[string]UserConfig `json:"users"`
}

var (
	configStoreMu      sync.Mutex
	primaryConfigStore UserConfigStore
)

func SetPrimaryConfigStore(store UserConfigStore) {
	configStoreMu.Lock()
	defer configStoreMu.Unlock()
	primaryConfigStore = store
}

func DefaultConfigStorePath() string {
	if path := os.Getenv(EnvConfigStorePath); path != "" {
		return path
	}
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return filepath.Join(".", "digital_twin_configs.json")
	}
	return filepath.Join(configDir, "openim-chat", "digital_twin_configs.json")
}

func LoadUserConfigFromStore(userID string) (UserConfig, bool, error) {
	cfg, ok, _, err := LoadUserConfigFromStoreWithSource(userID)
	return cfg, ok, err
}

func LoadUserConfigFromStoreWithSource(userID string) (UserConfig, bool, string, error) {
	if userID == "" {
		return UserConfig{}, false, "", nil
	}
	if store := getPrimaryConfigStore(); store != nil {
		cfg, ok, err := store.LoadUserConfig(context.Background(), userID)
		if err != nil || ok {
			return cfg, ok, "mongo_store", err
		}
	}

	cfg, ok, err := fileConfigStore{}.LoadUserConfig(context.Background(), userID)
	if err != nil || ok {
		return cfg, ok, "file_store", err
	}
	return cfg, ok, "", nil
}

func SaveUserConfigToStore(userID string, cfg UserConfig, now time.Time) error {
	if userID == "" {
		return nil
	}
	if store := getPrimaryConfigStore(); store != nil {
		return store.SaveUserConfig(context.Background(), userID, cfg, now)
	}
	return fileConfigStore{}.SaveUserConfig(context.Background(), userID, cfg, now)
}

func getPrimaryConfigStore() UserConfigStore {
	configStoreMu.Lock()
	defer configStoreMu.Unlock()
	return primaryConfigStore
}

type fileConfigStore struct{}

func (fileConfigStore) LoadUserConfig(_ context.Context, userID string) (UserConfig, bool, error) {
	if userID == "" {
		return UserConfig{}, false, nil
	}
	configStoreMu.Lock()
	defer configStoreMu.Unlock()

	store, err := readConfigStoreFile(DefaultConfigStorePath())
	if err != nil {
		return UserConfig{}, false, err
	}
	cfg, ok := store.Users[userID]
	return cfg, ok, nil
}

func (fileConfigStore) SaveUserConfig(_ context.Context, userID string, cfg UserConfig, now time.Time) error {
	if userID == "" {
		return nil
	}
	configStoreMu.Lock()
	defer configStoreMu.Unlock()

	path := DefaultConfigStorePath()
	store, err := readConfigStoreFile(path)
	if err != nil {
		return err
	}
	if store.Users == nil {
		store.Users = make(map[string]UserConfig)
	}
	store.Version = 1
	store.UpdatedAt = now.UnixMilli()
	store.Users[userID] = cfg

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readConfigStoreFile(path string) (configStoreFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configStoreFile{Version: 1, Users: make(map[string]UserConfig)}, nil
		}
		return configStoreFile{}, err
	}
	if len(data) == 0 {
		return configStoreFile{Version: 1, Users: make(map[string]UserConfig)}, nil
	}

	var store configStoreFile
	if err := json.Unmarshal(data, &store); err != nil {
		return configStoreFile{}, err
	}
	if store.Users == nil {
		store.Users = make(map[string]UserConfig)
	}
	return store, nil
}
