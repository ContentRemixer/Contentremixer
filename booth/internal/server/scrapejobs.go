package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ContentRemixer/Contentremixer/app/review"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/scrapejobs"
)

// mountScrapeJobs wires the scraper-review routes (plan §3.2). All are behind the
// auth Middleware; every handler reaches the domain ONLY through the §6.3-gating
// scrapejobs.Store. A nil store (e.g. a server built without Postgres) leaves the
// routes unmounted rather than panicking.
func (s *Server) mountScrapeJobs(mux *http.ServeMux) {
	if s.scrape == nil {
		return
	}
	mux.Handle("POST /v1/scrape-jobs", s.protect(s.createScrapeJob))
	mux.Handle("GET /v1/scrape-jobs/{id}", s.protect(s.getScrapeJob))
	mux.Handle("GET /v1/scraped-sources", s.protect(s.listScrapedSources))
	mux.Handle("GET /v1/scraped-sources/{id}/chunks", s.protect(s.getScrapedChunks))
	mux.Handle("POST /v1/scraped-sources/{id}/accept", s.protect(s.acceptScrapedSource))
	mux.Handle("POST /v1/scraped-sources/{id}/rescrape", s.protect(s.rescrapeScrapedSource))
	mux.Handle("POST /v1/scraped-sources/{id}/discard", s.protect(s.discardScrapedSource))
}

func (s *Server) createScrapeJob(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req scrapejobs.CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	job, err := s.scrape.CreateJob(r.Context(), sess, req)
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) getScrapeJob(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	job, err := s.scrape.GetJob(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) listScrapedSources(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	status := review.SourceStatus(r.URL.Query().Get("status"))
	sources, err := s.scrape.ListSources(r.Context(), sess, status)
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	if sources == nil {
		sources = []review.ScrapedSource{}
	}
	writeJSON(w, http.StatusOK, sources)
}

func (s *Server) getScrapedChunks(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	src, chunks, err := s.scrape.GetSourceChunks(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	if chunks == nil {
		chunks = []review.ScrapedChunk{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": src, "chunks": chunks})
}

func (s *Server) acceptScrapedSource(w http.ResponseWriter, r *http.Request) {
	s.sourceAction(w, r, s.scrape.Accept)
}

func (s *Server) discardScrapedSource(w http.ResponseWriter, r *http.Request) {
	s.sourceAction(w, r, s.scrape.Discard)
}

// sourceAction is the shared accept/discard shape: gate, transition, return the
// updated source.
func (s *Server) sourceAction(w http.ResponseWriter, r *http.Request,
	action func(ctx context.Context, sess auth.Session, id string) (review.ScrapedSource, error)) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	src, err := action(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, src)
}

func (s *Server) rescrapeScrapedSource(w http.ResponseWriter, r *http.Request) {
	sess, ok := session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	src, job, err := s.scrape.Rescrape(r.Context(), sess, r.PathValue("id"))
	if err != nil {
		s.writeScrapeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"source": src, "job": job})
}

// writeScrapeErr maps scraper-review domain errors to HTTP status codes.
func (s *Server) writeScrapeErr(w http.ResponseWriter, err error) {
	var invalid review.ErrInvalidTransition
	switch {
	case errors.Is(err, scrapejobs.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, review.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.As(err, &invalid):
		writeError(w, http.StatusConflict, invalid.Error())
	case scrapejobs.IsBadRequest(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("booth scrapejobs error", slog.Any("err", err))
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
