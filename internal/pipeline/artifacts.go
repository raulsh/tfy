package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/raulsh/tfy/internal/store"
	"github.com/raulsh/tfy/internal/store/db"
)

// ArtifactsDir holds, in a unit's workspace, the files its define and plan
// runs make for people to look at: mockups, diagrams, sample payloads.
const ArtifactsDir = "docs/artifacts"

// Limits on what a run's artifacts are kept of.
const (
	maxArtifactSize = 10 << 20
	maxArtifacts    = 50
)

// snapshotArtifacts stores what a run left under docs/artifacts: a new
// version of each file that is new or changed, and a removed version of
// each one it deleted. Only regular files are read, so a link cannot bring
// in a file from outside the workspace.
func (p *Pipeline) snapshotArtifacts(ctx context.Context, u db.Unit, author, runID string) error {
	files, skipped, err := readArtifacts(filepath.Join(u.WorkspacePath, ArtifactsDir))
	if err != nil {
		return fmt.Errorf("read %s: %w", ArtifactsDir, err)
	}
	latest, err := p.Store.Q.LatestArtifacts(ctx, u.ID)
	if err != nil {
		return err
	}
	known := map[string]db.LatestArtifactsRow{}
	for _, a := range latest {
		known[a.Path] = a
	}
	bg := context.WithoutCancel(ctx)
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		body := files[rel]
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		if a, ok := known[rel]; ok && !a.Removed && a.Sha256 == hash {
			continue
		}
		a, err := p.Store.Q.CreateArtifact(bg, db.CreateArtifactParams{
			ID: newID(), UnitID: u.ID, Path: rel, Content: body, ContentType: artifactType(rel, body),
			Size: int64(len(body)), Sha256: hash, Author: author, RunID: runID, Now: store.Now(),
		})
		if err != nil {
			return err
		}
		p.activity(ctx, u.ID, author, "artifact", fmt.Sprintf("artifact %s v%d by %s", rel, a.Version, author), map[string]any{"path": rel, "version": a.Version})
	}
	for _, a := range latest {
		if _, ok := files[a.Path]; ok || a.Removed {
			continue
		}
		if _, err := p.Store.Q.CreateArtifact(bg, db.CreateArtifactParams{
			ID: newID(), UnitID: u.ID, Path: a.Path, Content: []byte{}, ContentType: a.ContentType,
			Removed: true, Author: author, RunID: runID, Now: store.Now(),
		}); err != nil {
			return err
		}
		p.activity(ctx, u.ID, author, "artifact", fmt.Sprintf("artifact %s removed by %s", a.Path, author), map[string]any{"path": a.Path})
	}
	for _, s := range skipped {
		p.activity(ctx, u.ID, "system", "artifact", s, nil)
	}
	return nil
}

// readArtifacts reads the regular files under dir, by slash path relative
// to it. Hidden files, links and files over the limits are left out, and
// reported.
func readArtifacts(dir string) (map[string][]byte, []string, error) {
	files := map[string][]byte{}
	var skipped []string
	if info, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return files, nil, nil
	} else if err != nil {
		return nil, nil, err
	} else if !info.IsDir() {
		return files, []string{ArtifactsDir + " is not a directory: no artifacts kept"}, nil
	}
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, fmt.Sprintf("artifact %s left out: not a regular file", rel))
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch st, _ := info.Sys().(*syscall.Stat_t); {
		case st != nil && st.Nlink > 1:
			skipped = append(skipped, fmt.Sprintf("artifact %s left out: it is linked from elsewhere", rel))
			return nil
		case info.Size() > maxArtifactSize:
			skipped = append(skipped, fmt.Sprintf("artifact %s left out: larger than %d MB", rel, maxArtifactSize>>20))
			return nil
		case len(files) >= maxArtifacts:
			skipped = append(skipped, fmt.Sprintf("artifact %s left out: a unit keeps at most %d artifacts", rel, maxArtifacts))
			return nil
		}
		body, err := readNoFollow(full)
		if err != nil {
			return err
		}
		files[rel] = body
		return nil
	})
	return files, skipped, err
}

func readNoFollow(name string) ([]byte, error) {
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxArtifactSize+1))
}

// artifactType is the media type an artifact is served with.
func artifactType(name string, body []byte) string {
	t := mime.TypeByExtension(path.Ext(name))
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown":
		t = "text/markdown; charset=utf-8"
	case ".mmd", ".mermaid", ".txt", ".log", ".csv", ".yaml", ".yml", ".toml", ".sql", ".diff", ".patch":
		t = "text/plain; charset=utf-8"
	}
	if t == "" {
		t = http.DetectContentType(body)
	}
	return t
}

// restoreArtifacts writes a unit's latest artifacts back to its workspace.
func (p *Pipeline) restoreArtifacts(ctx context.Context, u db.Unit) error {
	latest, err := p.Store.Q.LatestArtifacts(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, a := range latest {
		if a.Removed || !ValidArtifactPath(a.Path) {
			continue
		}
		full, err := p.Store.Q.GetArtifactVersion(ctx, db.GetArtifactVersionParams{UnitID: u.ID, Path: a.Path, Version: a.Version})
		if err != nil {
			return err
		}
		name := filepath.Join(u.WorkspacePath, ArtifactsDir, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(name, full.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ValidArtifactPath reports whether p names a file under docs/artifacts.
func ValidArtifactPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && !strings.HasPrefix(p, "../") && p != ".."
}

// currentArtifact reports whether a unit has an artifact at p now.
func (p *Pipeline) currentArtifact(ctx context.Context, u db.Unit, rel string) bool {
	latest, err := p.Store.Q.LatestArtifacts(ctx, u.ID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(latest, func(a db.LatestArtifactsRow) bool { return a.Path == rel && !a.Removed })
}
