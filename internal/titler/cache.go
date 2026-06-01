// Package titler generates short Japanese titles for Claude Code sessions
// using a local ollama model, and caches them on disk so generation only ever
// happens once per (unchanged) session.
package titler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// entry is one cached title plus the file fingerprint it was generated from.
// If a session file's size or mtime changes, the cached title is stale.
type entry struct {
	Title     string `json:"title"`
	Size      int64  `json:"size"`
	ModTimeNS int64  `json:"mtimeNs"`
}

// Cache is a concurrency-safe, disk-backed map of sessionID -> generated title.
type Cache struct {
	mu      sync.Mutex // guards entries
	saveMu  sync.Mutex // serializes disk writes so concurrent Saves can't clobber the tmp file
	path    string
	entries map[string]entry
}

// CachePath returns the on-disk cache location. Override with KIOKU_TITLE_CACHE;
// otherwise it follows the same app-support convention as the sync repo.
func CachePath() string {
	if v := os.Getenv("KIOKU_TITLE_CACHE"); v != "" {
		return v
	}
	return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "kioku", "titles.json")
}

// LoadCache reads the cache from disk. A missing or unreadable file yields an
// empty (but usable) cache rather than an error.
func LoadCache() *Cache {
	path := CachePath()
	c := &Cache{path: path, entries: map[string]entry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c.entries)
	if c.entries == nil {
		c.entries = map[string]entry{}
	}
	return c
}

// Get returns the cached title for id, but only if the cached fingerprint still
// matches the live (size, mtimeNS); otherwise it reports a miss so the caller
// regenerates.
func (c *Cache) Get(id string, size, mtimeNS int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || e.Size != size || e.ModTimeNS != mtimeNS || e.Title == "" {
		return "", false
	}
	return e.Title, true
}

// Set records a freshly generated title with its source fingerprint.
func (c *Cache) Set(id, title string, size, mtimeNS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = entry{Title: title, Size: size, ModTimeNS: mtimeNS}
}

// Save atomically writes the cache to disk (tmp file + rename), creating the
// parent directory if needed.
func (c *Cache) Save() error {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	c.mu.Lock()
	data, err := json.MarshalIndent(c.entries, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
