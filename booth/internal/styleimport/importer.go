// Package styleimport implements the one-time, idempotent migration importer
// for ContentStyle CRs (ISI-7965 W3).
//
// The content repo's styles/<format>.md guides move into ContentStyle CRs so
// the Admin UI owns them (board ask: a real migration + cutover, not a
// side-by-side copy). The importer is env-gated at boot, in the same pattern
// as the DevRel seed user (booth/auth): BOOTH_STYLE_IMPORT_DIR points at a
// directory holding one <format>.md per known output format (in-cluster this
// will be a mounted ConfigMap volume; the deploy wiring is W5). Unset =>
// importer off, boot behaviour unchanged.
//
// Idempotency contract: safe to run on EVERY boot while the env var is set.
// For each format the importer Gets the CR by name (name = format) and then
// Creates (absent), Updates (guide or description drifted), or skips (already
// current) — so a re-run converges without churn, and the repo directory
// remains the source of truth until the cutover (W5) removes the env var and
// deletes the repo files.
package styleimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// EnvImportDir names the directory holding one <format>.md per known output
// format. Unset or empty disables the importer (default).
const EnvImportDir = "BOOTH_STYLE_IMPORT_DIR"

// knownFormats is the closed set of writer output formats with a style guide.
// It mirrors v1alpha1.KnownOutputFormats and the ContentStyle outputFormat
// enum (lock-step: see app/api/v1alpha1/common_types.go).
var knownFormats = v1alpha1.KnownOutputFormats

// Result summarises one import run for the boot log.
type Result struct {
	Created   int
	Updated   int
	Unchanged int
	Missing   int
}

// Import reads <dir>/<format>.md for every known format and creates or
// refreshes the matching ContentStyle CR (name = format, one per outputFormat
// as the W1 validating webhook enforces) in namespace.
//
// Per-format failures are logged and skipped, never fatal: a flaky API call
// or a missing file must not take Booth down — the run is idempotent, so the
// next boot retries. The returned error is non-nil only for a directory-level
// configuration mistake (unreadable or empty directory).
func Import(ctx context.Context, c client.Client, namespace, dir string, log *slog.Logger) (Result, error) {
	var res Result
	if log == nil {
		log = slog.Default()
	}
	if dir == "" {
		return res, nil
	}
	if entries, err := os.ReadDir(dir); err != nil {
		return res, fmt.Errorf("styleimport: read %s: %w", dir, err)
	} else if len(entries) == 0 {
		return res, fmt.Errorf("styleimport: %s is empty", dir)
	}
	for _, format := range knownFormats {
		path := filepath.Join(dir, format+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				res.Missing++
				log.Warn("styleimport: no guide file for format",
					slog.String("format", format), slog.String("path", path))
				continue
			}
			log.Error("styleimport: read guide failed",
				slog.String("format", format), slog.Any("err", err))
			continue
		}
		guide := strings.TrimSpace(string(raw))
		if guide == "" {
			log.Error("styleimport: guide file is empty",
				slog.String("format", format), slog.String("path", path))
			continue
		}
		desc := firstHeading(raw)

		existing := &v1alpha1.ContentStyle{}
		err = c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: format}, existing)
		switch {
		case apierrors.IsNotFound(err):
			cs := &v1alpha1.ContentStyle{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: format},
				Spec: v1alpha1.ContentStyleSpec{
					OutputFormat: format,
					Guide:        guide,
					Description:  desc,
				},
			}
			if err := c.Create(ctx, cs); err != nil {
				log.Error("styleimport: create failed",
					slog.String("format", format), slog.Any("err", err))
				continue
			}
			res.Created++
			log.Info("styleimport: created ContentStyle",
				slog.String("name", format), slog.String("format", format))
		case err != nil:
			log.Error("styleimport: get failed",
				slog.String("format", format), slog.Any("err", err))
			continue
		default:
			if existing.Spec.Guide == guide && existing.Spec.Description == desc {
				res.Unchanged++
				continue
			}
			existing.Spec.OutputFormat = format
			existing.Spec.Guide = guide
			existing.Spec.Description = desc
			if err := c.Update(ctx, existing); err != nil {
				log.Error("styleimport: update failed",
					slog.String("format", format), slog.Any("err", err))
				continue
			}
			res.Updated++
			log.Info("styleimport: updated ContentStyle",
				slog.String("name", format), slog.String("format", format))
		}
	}
	return res, nil
}

// firstHeading returns the text of the first markdown H1 ("# ...") line in
// raw, or "" when the guide has none. It feeds the Admin-UI description field.
func firstHeading(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}
