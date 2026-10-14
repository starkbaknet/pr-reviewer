package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Event struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Tool   string    `json:"tool,omitempty"`
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
}

type Posted struct {
	At       time.Time `json:"at"`
	Type     string    `json:"type"`
	ID       int64     `json:"id"`
	Event    string    `json:"event,omitempty"`
	Comments int       `json:"comments"`
	URL      string    `json:"url"`
	HeadSHA  string    `json:"head_sha"`
}

type Session struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Repo      string    `json:"repo"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	HeadRef   string    `json:"head_ref"`
	BaseRef   string    `json:"base_ref"`
	HeadSHA   string    `json:"head_sha"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Events    []Event   `json:"events"`
	Posted    []Posted  `json:"posted"`
}

func ID(owner, repo string, n int64) string {
	return fmt.Sprintf("%s__%s__%d", owner, repo, n)
}

/*
 * One JSON file per PR. The MCP process writes, the web process reads,
 * and they may not be the same process, so writes go through a rename
 * to stay atomic. The mutex only serialises writers in one process.
 */
type Store struct {
	dir string
	mu  sync.Mutex
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string {
	id = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(id)
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) Get(id string) (*Session, error) {
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (s *Store) write(sess *Session) error {
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	p := s.path(sess.ID)
	if err := os.WriteFile(p+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func (s *Store) Update(id string, create func() *Session, fn func(*Session)) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		if create == nil {
			return nil, fmt.Errorf("no session %s", id)
		}
		sess = create()
		sess.ID = id
		sess.StartedAt = time.Now()
	}
	if fn != nil {
		fn(sess)
	}
	return sess, s.write(sess)
}

func (s *Store) List() ([]*Session, error) {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		sess, err := s.Get(strings.TrimSuffix(name, ".json"))
		if err != nil || sess == nil {
			continue
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}
