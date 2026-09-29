package api

import (
	"mime"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/raulsh/tfy/internal/pipeline"
	"github.com/raulsh/tfy/internal/store/db"
)

// ArtifactView is one version of a file under a unit's docs/artifacts.
type ArtifactView struct {
	Path        string    `json:"path"`
	Version     int64     `json:"version"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	Removed     bool      `json:"removed,omitempty"`
	Author      string    `json:"author"`
	RunID       string    `json:"run_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// artifactCSP confines an artifact however it is opened, in tfy's frame or
// in a tab of its own. The sandbox gives it an opaque origin, so it has no
// cookie and cannot use tfy's API; it may run scripts, but loads nothing
// from the network and can neither submit forms nor open windows.
const artifactCSP = "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; font-src data:; media-src data: blob:; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"

func (s *Server) artifactRoutes(r fiber.Router) {
	r.Get("/units/:id/artifact-versions", s.artifactVersions)
	r.Get("/units/:id/artifacts/:version/*", s.getArtifact)
}

func (s *Server) unitArtifacts(c fiber.Ctx, unitID string) ([]ArtifactView, error) {
	rows, err := s.Store.Q.LatestArtifacts(c.Context(), unitID)
	if err != nil {
		return nil, err
	}
	out := []ArtifactView{}
	for _, a := range rows {
		if !a.Removed {
			out = append(out, ArtifactView{Path: a.Path, Version: a.Version, ContentType: a.ContentType, Size: a.Size, Author: a.Author, RunID: a.RunID, CreatedAt: a.CreatedAt})
		}
	}
	return out, nil
}

func (s *Server) artifactVersions(c fiber.Ctx) error {
	rows, err := s.Store.Q.ListArtifactVersions(c.Context(), db.ListArtifactVersionsParams{UnitID: c.Params("id"), Path: c.Query("path")})
	if err != nil {
		return err
	}
	out := make([]ArtifactView, 0, len(rows))
	for _, a := range rows {
		out = append(out, ArtifactView{Path: a.Path, Version: a.Version, ContentType: a.ContentType, Size: a.Size, Removed: a.Removed, Author: a.Author, RunID: a.RunID, CreatedAt: a.CreatedAt})
	}
	return ok(c, out)
}

// getArtifact serves one version of an artifact as the file itself.
func (s *Server) getArtifact(c fiber.Ctx) error {
	version, err := strconv.ParseInt(c.Params("version"), 10, 64)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "a version number is required")
	}
	name, err := url.PathUnescape(c.Params("*"))
	if err != nil || !pipeline.ValidArtifactPath(name) {
		return fiber.NewError(fiber.StatusBadRequest, "not an artifact path")
	}
	a, err := s.Store.Q.GetArtifactVersion(c.Context(), db.GetArtifactVersionParams{UnitID: c.Params("id"), Path: name, Version: version})
	if err != nil {
		return err
	}
	if a.Removed {
		return fiber.NewError(fiber.StatusNotFound, "this version records that the artifact was removed")
	}
	return sendArtifact(c, a.Path, a.ContentType, a.Content)
}

// sendArtifact sends an artifact's bytes with the headers that confine it.
// A version never changes, so it may be cached.
func sendArtifact(c fiber.Ctx, name, contentType string, body []byte) error {
	c.Set(fiber.HeaderContentType, contentType)
	c.Set(fiber.HeaderContentSecurityPolicy, artifactCSP)
	c.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	c.Set(fiber.HeaderReferrerPolicy, "no-referrer")
	c.Set(fiber.HeaderCacheControl, "private, max-age=31536000, immutable")
	c.Set(fiber.HeaderContentDisposition, mime.FormatMediaType("inline", map[string]string{"filename": path.Base(name)}))
	return c.Send(body)
}
