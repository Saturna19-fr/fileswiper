package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// moveFile renames src to dst, falling back to copy+delete across volumes.
// The modification time is preserved either way.
func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	tmp := dst + ".fileswiper-part"
	out, err := os.Create(tmp)
	if err != nil {
		in.Close()
		return err
	}
	n, err := io.Copy(out, in)
	in.Close()
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != st.Size() {
		err = fmt.Errorf("copie incomplète de %s", src)
	}
	if err == nil {
		err = os.Chtimes(tmp, st.ModTime(), st.ModTime())
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Remove(src)
}

// uniquePath returns p, or "name (2).ext", "name (3).ext"… if p is taken.
func uniquePath(p string) string {
	if _, err := os.Lstat(p); err != nil {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Lstat(c); err != nil {
			return c
		}
	}
}

const headSize = 64 << 10

// hashFile returns the SHA-256 of the first limit bytes (whole file if limit <= 0).
func hashFile(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	if _, err := io.CopyBuffer(h, r, make([]byte, 1<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashCache persists hashes keyed by path+size+mtime so restarts are cheap.
type hashCache struct {
	mu    sync.Mutex
	path  string
	m     map[string]string
	dirty int
}

func loadHashCache(path string) *hashCache {
	c := &hashCache{path: path, m: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

func cacheKey(kind, abs string, size int64, mod time.Time) string {
	return fmt.Sprintf("%s|%s|%d|%d", kind, abs, size, mod.UnixNano())
}

func (c *hashCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *hashCache) put(key, hash string) {
	c.mu.Lock()
	c.m[key] = hash
	c.dirty++
	flush := c.dirty >= 500
	c.mu.Unlock()
	if flush {
		c.save()
	}
}

func (c *hashCache) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirty == 0 {
		return
	}
	b, _ := json.Marshal(c.m)
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil && os.Rename(tmp, c.path) == nil {
		c.dirty = 0
	}
}
