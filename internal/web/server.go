package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"prr/internal/review"
	"prr/internal/store"
)

//go:embed static/*
var static embed.FS

type Server struct {
	svc   *review.Service
	store *store.Store
	mcp   *mcp.Server

	mu     sync.Mutex
	status map[string]cached
}

type cached struct {
	st  *review.Status
	err error
	at  time.Time
}

func New(svc *review.Service, st *store.Store, m *mcp.Server) *Server {
	return &Server{svc: svc, store: st, mcp: m, status: map[string]cached{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	files, _ := fs.Sub(static, "static")
	mux.Handle("/", http.FileServerFS(files))
	mux.HandleFunc("GET /api/sessions", s.list)
	mux.HandleFunc("GET /api/sessions/{id}", s.get)
	mux.HandleFunc("GET /api/sessions/{id}/status", s.getStatus)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil))
	return mux
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func oops(w http.ResponseWriter, code int, msg string) {
	reply(w, code, map[string]string{"error": msg})
}

type summary struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Repo      string    `json:"repo"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Author    string    `json:"author"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Events    int       `json:"events"`
	Posted    int       `json:"posted"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.store.List()
	if err != nil {
		oops(w, 500, err.Error())
		return
	}
	out := make([]summary, 0, len(sessions))
	for _, x := range sessions {
		out = append(out, summary{x.ID, x.Owner, x.Repo, x.Number, x.Title, x.Author, x.URL,
			x.StartedAt, x.UpdatedAt, len(x.Events), len(x.Posted)})
	}
	reply(w, 200, out)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		oops(w, 500, err.Error())
		return
	}
	if sess == nil {
		oops(w, 404, "no such session")
		return
	}
	reply(w, 200, sess)
}

/*
 * Status means five or six Gitea round trips, and the page polls, so
 * hold the result for a bit unless the page explicitly asks for fresh.
 */
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := s.store.Get(id)
	if err != nil || sess == nil {
		oops(w, 404, "no such session")
		return
	}

	s.mu.Lock()
	c, ok := s.status[id]
	s.mu.Unlock()

	if !ok || r.URL.Query().Get("refresh") == "1" || time.Since(c.at) > 15*time.Second {
		st, err := s.svc.Status(r.Context(), sess.Owner, sess.Repo, sess.Number)
		c = cached{st, err, time.Now()}
		s.mu.Lock()
		s.status[id] = c
		s.mu.Unlock()
	}
	if c.err != nil {
		oops(w, 502, c.err.Error())
		return
	}
	reply(w, 200, c.st)
}
