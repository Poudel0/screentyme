package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/Poudel0/screentyme/internal/analytics"
	"github.com/Poudel0/screentyme/internal/store"
)

type Server struct {
	store    *store.Store
	analyzer *analytics.Analyzer
	srv      *http.Server
}

func New(st *store.Store, addr string, webFS fs.FS) *Server {
	s := &Server{
		store:    st,
		analyzer: analytics.New(st.DB()),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)

	mux.HandleFunc("/api/keywords", s.handleKeywords)
	mux.HandleFunc("/api/keywords/totals", s.handleKeywordTotals)
	mux.HandleFunc("/api/keywords/{id}", s.handleKeywordByID)

	mux.HandleFunc("/api/analytics/today", s.handleTodayByApp)
	mux.HandleFunc("/api/analytics/recent", s.handleRecentByKeyword)
	mux.HandleFunc("/api/analytics/history", s.handleHistory)
	mux.HandleFunc("/api/analytics/apps/{app_class}", s.handleAppDetail)

	if webFS != nil {
		mux.Handle("/", http.FileServer(http.FS(webFS)))
	}

	s.srv = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	log.Printf("server: listening on %s", ln.Addr())

	go func() {
		<-ctx.Done()
		_ = s.srv.Shutdown(context.Background())
	}()

	if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

// queryInt reads an int query param, falling back to def. Values <= 0 also use def.
func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("server: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if err := s.store.Health(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleKeywords(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		kws, err := s.store.ListKeywords(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"keywords": kws})

	case http.MethodPost:
		var body struct {
			AppClass string `json:"app_class"`
			Keyword  string `json:"keyword"`
			Label    string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "bad request body")
			return
		}
		if body.AppClass == "" || body.Keyword == "" {
			writeError(w, http.StatusBadRequest, "app_class and keyword required")
			return
		}
		if err := s.store.AddKeyword(r.Context(), body.AppClass, body.Keyword, body.Label); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusCreated)

	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleKeywordByID(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodDelete) {
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := s.store.DeleteKeyword(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleKeywordTotals(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	days := queryInt(r, "days", 7)
	totals, err := s.analyzer.KeywordTotals(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days":     days,
		"keywords": totals,
	})
}

func (s *Server) handleTodayByApp(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	results, err := s.analyzer.TodayByApp(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": results})
}

func (s *Server) handleRecentByKeyword(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	days := queryInt(r, "days", 7)
	results, err := s.analyzer.RecentByKeyword(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days":    days,
		"entries": results,
	})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	days := queryInt(r, "days", 7)
	results, err := s.analyzer.History(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days": days,
		"rows": results,
	})
}

func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	appClass := r.PathValue("app_class")
	if appClass == "" {
		writeError(w, http.StatusBadRequest, "app_class required")
		return
	}
	days := queryInt(r, "days", 7)
	detail, err := s.analyzer.AppDetailFor(r.Context(), appClass, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}
