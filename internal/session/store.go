// Package session persists resumable local conversations with private file
// permissions. Sessions live in the OS cache directory, never in the repository.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/yuzu-ux/ycode/internal/provider"
)

const currentVersion = 1

type State struct {
	Version   int                `json:"version"`
	ID        string             `json:"id"`
	Root      string             `json:"root"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Messages  []provider.Message `json:"messages"`
}

type Metadata struct {
	ID        string
	UpdatedAt time.Time
	Messages  int
}

type Store struct {
	root    string
	baseDir string
}

func NewStore(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cache := strings.TrimSpace(os.Getenv("YCODE_CACHE_DIR"))
	if cache == "" {
		cache, err = os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		cache = filepath.Join(cache, "ycode")
	} else {
		cache, err = filepath.Abs(cache)
		if err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256([]byte(absolute))
	dir := filepath.Join(cache, "sessions", hex.EncodeToString(sum[:6]))
	return &Store{root: absolute, baseDir: dir}, nil
}

func (s *Store) New() (*State, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &State{
		Version:   currentVersion,
		ID:        id,
		Root:      s.root,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *Store) Save(state *State) error {
	if s == nil || state == nil {
		return errors.New("session store/state is nil")
	}
	if !validID.MatchString(state.ID) {
		return errors.New("invalid session id")
	}
	state.Version = currentVersion
	state.Root = s.root
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(s.baseDir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(s.baseDir, ".session-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	target := s.path(state.ID)
	if err := os.Rename(tempPath, target); err != nil && runtime.GOOS == "windows" {
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tempPath, target)
	} else {
		return err
	}
}

func (s *Store) Load(id string) (*State, error) {
	if id == "" || id == "latest" {
		entries, err := s.List()
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return nil, errors.New("no saved sessions for this workspace")
		}
		id = entries[0].ID
	}
	if !validID.MatchString(id) {
		return nil, errors.New("invalid session id")
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Version != currentVersion {
		return nil, fmt.Errorf("unsupported session version %d", state.Version)
	}
	absolute, _ := filepath.Abs(state.Root)
	if absolute != s.root {
		return nil, errors.New("session belongs to a different workspace")
	}
	return &state, nil
}

func (s *Store) List() ([]Metadata, error) {
	files, err := filepath.Glob(filepath.Join(s.baseDir, "*.json"))
	if err != nil {
		return nil, err
	}
	metadata := make([]Metadata, 0, len(files))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var state State
		if json.Unmarshal(data, &state) != nil || !validID.MatchString(state.ID) {
			continue
		}
		metadata = append(metadata, Metadata{
			ID:        state.ID,
			UpdatedAt: state.UpdatedAt,
			Messages:  len(state.Messages),
		})
	}
	sort.Slice(metadata, func(i, j int) bool {
		return metadata[i].UpdatedAt.After(metadata[j].UpdatedAt)
	})
	return metadata, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.baseDir, id+".json")
}

var validID = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-f0-9]{6}$`)

func newID() (string, error) {
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + strings.ToLower(hex.EncodeToString(random)), nil
}
