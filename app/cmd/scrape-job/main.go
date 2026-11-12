// Command scrape-job is the entrypoint the Booth API runs as a Kubernetes batch
// Job (plan §0.2, §3.2): POST /v1/scrape-jobs creates a Job whose pod runs this
// binary. It fetches one reference with the ISI-7145 scraper, chunks it with the
// deterministic ISI-7145 chunker (TEXT ONLY — no embeddings; those are deferred
// to review-accept), and writes the result as ScrapedSource + ScrapedChunk rows
// (status pending) into the Booth-owned Postgres schema (app/review), then marks
// the owning scrape_jobs row succeeded/failed.
//
// It is an app-module binary on purpose: the scraper and chunker live in
// app/internal, reachable only inside this module. Booth never runs the scraper
// itself — it launches this Job and reads the rows back through app/review.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/chunk"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/scrape"
	"github.com/ContentRemixer/Contentremixer/app/review"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("scrape-job failed", slog.Any("err", err))
		os.Exit(1)
	}
}

// jobEnv is the contract the Booth launcher fills via the Job's env (see
// booth/internal/scrapejobs launcher). All fields are required.
type jobEnv struct {
	jobID         string
	ownerSubject  string
	referenceRepo string
	personaName   string
	sourceURL     string
	sourceKind    string
	// sourceText carries the inline body for the no-fetch teleprompter-script
	// kind (plan §5): the DevRel pastes a script, Booth stores it, and the
	// launcher passes it here as SCRAPE_SOURCE_TEXT. Empty for fetch kinds.
	sourceText string
	// githubToken optionally authenticates github-repo fetches (lifts the 60-
	// req/hr anonymous rate limit). Optional; anonymous works for public repos.
	githubToken string
	dsn         string
}

func readEnv() (jobEnv, error) {
	e := jobEnv{
		jobID:         os.Getenv("SCRAPE_JOB_ID"),
		ownerSubject:  os.Getenv("SCRAPE_OWNER_SUBJECT"),
		referenceRepo: os.Getenv("SCRAPE_REFERENCE_REPO"),
		personaName:   os.Getenv("SCRAPE_PERSONA_NAME"),
		sourceURL:     os.Getenv("SCRAPE_SOURCE_URL"),
		sourceKind:    os.Getenv("SCRAPE_SOURCE_KIND"),
		sourceText:    os.Getenv("SCRAPE_SOURCE_TEXT"),
		githubToken:   os.Getenv("SCRAPE_GITHUB_TOKEN"),
		dsn:           os.Getenv("REVIEW_DSN"),
	}
	var missing []string
	for k, v := range map[string]string{
		"SCRAPE_JOB_ID": e.jobID, "SCRAPE_OWNER_SUBJECT": e.ownerSubject,
		"SCRAPE_PERSONA_NAME": e.personaName, "SCRAPE_SOURCE_URL": e.sourceURL,
		"SCRAPE_SOURCE_KIND": e.sourceKind, "REVIEW_DSN": e.dsn,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return e, fmt.Errorf("scrape-job: missing required env: %s", strings.Join(missing, ", "))
	}
	if !review.ValidSourceKind(e.sourceKind) {
		return e, fmt.Errorf("scrape-job: unsupported source kind %q", e.sourceKind)
	}
	// The no-fetch kind has no URL to fall back on: its body must be supplied.
	if e.sourceKind == v1alpha1.ReferenceKindTeleprompterScript && strings.TrimSpace(e.sourceText) == "" {
		return e, fmt.Errorf("scrape-job: source kind %q requires SCRAPE_SOURCE_TEXT (the inline script body)", e.sourceKind)
	}
	return e, nil
}

func run(log *slog.Logger) error {
	env, err := readEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	st, err := review.Open(ctx, env.dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	// Claim the job (queued -> running). A missing row means the API record was
	// deleted; fail loudly rather than write orphaned sources.
	if err := st.SetJobStatus(ctx, env.jobID, review.JobRunning, ""); err != nil {
		return fmt.Errorf("scrape-job: mark running: %w", err)
	}

	if err := doScrape(ctx, st, env, log); err != nil {
		// Record the failure on the job row, then surface it so the pod exits
		// non-zero (Job backoff / visibility).
		if serr := st.SetJobStatus(ctx, env.jobID, review.JobFailed, err.Error()); serr != nil {
			log.Error("scrape-job: could not record failure", slog.Any("err", serr))
		}
		return err
	}
	return st.SetJobStatus(ctx, env.jobID, review.JobSucceeded, "")
}

func doScrape(ctx context.Context, st *review.Store, env jobEnv, log *slog.Logger) error {
	scraper := scrape.New(&scrape.Options{
		SourceText:  env.sourceText,
		GitHubToken: env.githubToken,
	})
	chunker := chunk.New()

	docs, err := scraper.Scrape(ctx, v1alpha1.PersonaReference{URL: env.sourceURL, Kind: env.sourceKind})
	if err != nil {
		return fmt.Errorf("scrape-job: scrape %s: %w", env.sourceURL, err)
	}

	for _, doc := range docs {
		ragChunks := chunker.Chunk(doc, env.ownerSubject, env.personaName)
		src := review.ScrapedSource{
			ID:            sourceID(env.jobID, doc.SourceURL),
			JobID:         env.jobID,
			OwnerSubject:  env.ownerSubject,
			ReferenceRepo: env.referenceRepo,
			PersonaName:   env.personaName,
			SourceURL:     doc.SourceURL,
			SourceKind:    doc.SourceKind,
			Title:         doc.Title,
		}
		chunks := make([]review.ScrapedChunk, len(ragChunks))
		for i, rc := range ragChunks {
			chunks[i] = review.ScrapedChunk{
				ID:       rc.ID,
				SourceID: src.ID,
				Ordinal:  rc.Ordinal,
				Text:     rc.Text,
				Metadata: rc.Metadata,
			}
		}
		if err := st.InsertSource(ctx, src, chunks); err != nil {
			return fmt.Errorf("scrape-job: persist source %s: %w", doc.SourceURL, err)
		}
		log.Info("scraped source persisted",
			slog.String("jobId", env.jobID), slog.String("sourceId", src.ID),
			slog.String("url", doc.SourceURL), slog.Int("chunks", len(chunks)))
	}
	return nil
}

// sourceID is a deterministic id for a (job, resolved-url) pair so a job that
// fans out to several docs gets distinct, stable source rows.
func sourceID(jobID, url string) string {
	return review.DeterministicID(jobID, url)
}
