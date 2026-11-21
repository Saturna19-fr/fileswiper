package main

import (
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	errNotFound  = errors.New("fichier inconnu")
	errGone      = errors.New("le fichier n'existe plus sur le disque")
	errBadAction = errors.New("action invalide")
	errNoUndo    = errors.New("rien à annuler")
)

type entry struct {
	ID   string
	Rel  string // slash-separated, relative to its root (source or Validé)
	Size int64
	Mod  time.Time
	Hash string // full SHA-256, only set when another file has the same size and head
}

type journalItem struct {
	Action   string // keep | delete
	E        *entry
	From, To string
	ValRel   string
}

type dupGroup struct {
	pending   []*entry
	validated []*entry
}

type FileItem struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	RelPath         string   `json:"relPath"`
	Path            string   `json:"path"`
	Size            int64    `json:"size"`
	Modified        int64    `json:"modified"`
	Ext             string   `json:"ext"`
	Kind            string   `json:"kind"`
	DupCount        int      `json:"dupCount"`
	ValidatedCopies []string `json:"validatedCopies"`
}

type DuplicateGroup struct {
	Hash            string     `json:"hash"`
	Size            int64      `json:"size"`
	Files           []FileItem `json:"files"`
	ValidatedCopies []string   `json:"validatedCopies"`
}

type Status struct {
	Name            string `json:"name"`
	Source          string `json:"source"`
	Validated       string `json:"validated"`
	Trash           string `json:"trash"`
	Pending         int    `json:"pending"`
	Kept            int    `json:"kept"`
	Trashed         int    `json:"trashed"`
	ValidatedTotal  int    `json:"validatedTotal"`
	Scanning        bool   `json:"scanning"`
	Hashing         bool   `json:"hashing"`
	HashDone        int    `json:"hashDone"`
	HashTotal       int    `json:"hashTotal"`
	DuplicateGroups int    `json:"duplicateGroups"`
	CanUndo         bool   `json:"canUndo"`
}

type Store struct {
	cfg     Config
	dataDir string
	cache   *hashCache

	mu        sync.Mutex
	pending   map[string]*entry
	order     []string
	validated map[string]*entry // keyed by Rel
	journal   []journalItem
	kept      int
	trashed   int
	scanning  bool
	hashing   bool
	hashDone  int
	hashTotal int
	gen       int  // bumped on every rescan; stale hash passes stop
	moved     bool // a file moved while hashing: run another pass
	dups      map[string]*dupGroup
	dupDirty  bool
}

func NewStore(cfg Config, dataDir string) *Store {
	return &Store{
		cfg:       cfg,
		dataDir:   dataDir,
		cache:     loadHashCache(filepath.Join(dataDir, "fileswiper-hashcache.json")),
		pending:   map[string]*entry{},
		validated: map[string]*entry{},
		dupDirty:  true,
	}
}

func idFor(rel string) string {
	h := sha1.Sum([]byte(rel))
	return hex.EncodeToString(h[:8])
}

var junkNames = map[string]bool{
	"thumbs.db": true, "desktop.ini": true, ".ds_store": true,
	"$recycle.bin": true, "system volume information": true,
}

func walk(root string, skip []string) []*entry {
	var out []*entry
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() {
			if junkNames[name] && p != root {
				return filepath.SkipDir
			}
			for _, s := range skip {
				if samePath(p, s) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() || junkNames[name] || strings.HasPrefix(name, "~$") ||
			strings.HasSuffix(name, ".fileswiper-part") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		out = append(out, &entry{ID: idFor(rel), Rel: rel, Size: info.Size(), Mod: info.ModTime()})
		return nil
	})
	return out
}

// Rescan rebuilds the queue from disk, then looks for duplicates.
func (s *Store) Rescan() {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return
	}
	s.scanning = true
	s.gen++
	gen := s.gen
	s.mu.Unlock()

	// The data dir (config, cache, journal) may live inside the source.
	pend := walk(s.cfg.Source, []string{s.cfg.Validated, s.cfg.Trash})
	val := walk(s.cfg.Validated, nil)

	s.mu.Lock()
	s.pending = make(map[string]*entry, len(pend))
	s.order = s.order[:0]
	for _, e := range pend {
		if s.isOwnFile(filepath.Join(s.cfg.Source, filepath.FromSlash(e.Rel))) {
			continue
		}
		s.pending[e.ID] = e
		s.order = append(s.order, e.ID)
	}
	s.validated = make(map[string]*entry, len(val))
	for _, e := range val {
		s.validated[e.Rel] = e
	}
	s.scanning = false
	s.dupDirty = true
	s.mu.Unlock()

	for s.hashPass(gen) {
	}
}

func (s *Store) isOwnFile(abs string) bool {
	return samePath(filepath.Dir(abs), s.dataDir) && strings.HasPrefix(strings.ToLower(filepath.Base(abs)), "fileswiper")
}

type hashJob struct {
	e    *entry
	abs  string
	size int64
	mod  time.Time
	head string
}

// hashPass narrows candidates by size, then by the hash of the first 64 KB,
// and only then hashes whole files. Returns true if another pass is needed.
func (s *Store) hashPass(gen int) bool {
	s.mu.Lock()
	if s.gen != gen {
		s.mu.Unlock()
		return false
	}
	bySize := map[int64][]*hashJob{}
	add := func(root string, e *entry) {
		if e.Size == 0 || e.Hash != "" {
			return
		}
		bySize[e.Size] = append(bySize[e.Size], &hashJob{
			e: e, abs: filepath.Join(root, filepath.FromSlash(e.Rel)), size: e.Size, mod: e.Mod,
		})
	}
	// Already-hashed files still count as size peers for the others.
	hashedSizes := map[int64]bool{}
	for _, e := range s.pending {
		if e.Hash != "" {
			hashedSizes[e.Size] = true
		}
		add(s.cfg.Source, e)
	}
	for _, e := range s.validated {
		if e.Hash != "" {
			hashedSizes[e.Size] = true
		}
		add(s.cfg.Validated, e)
	}
	s.moved = false
	s.hashing = true
	s.hashDone, s.hashTotal = 0, 0
	s.mu.Unlock()

	var heads []*hashJob
	for size, jobs := range bySize {
		if len(jobs) >= 2 || hashedSizes[size] {
			heads = append(heads, jobs...)
		}
	}
	s.setHashTotal(len(heads))

	type headKey struct {
		size int64
		head string
	}
	byHead := map[headKey][]*hashJob{}
	for _, j := range heads {
		if s.stale(gen) {
			return false
		}
		key := cacheKey("head", j.abs, j.size, j.mod)
		h, ok := s.cache.get(key)
		if !ok {
			var err error
			if h, err = hashFile(j.abs, headSize); err != nil {
				continue
			}
			s.cache.put(key, h)
		}
		j.head = h
		k := headKey{j.size, h}
		byHead[k] = append(byHead[k], j)
	}

	var full []*hashJob
	for k, jobs := range byHead {
		if len(jobs) >= 2 || hashedSizes[k.size] {
			full = append(full, jobs...)
		}
	}
	sort.Slice(full, func(i, j int) bool { return full[i].abs < full[j].abs })
	s.setHashTotal(len(full))

	for _, j := range full {
		if s.stale(gen) {
			return false
		}
		h := j.head
		if j.size > headSize {
			key := cacheKey("full", j.abs, j.size, j.mod)
			var ok bool
			if h, ok = s.cache.get(key); !ok {
				var err error
				if h, err = hashFile(j.abs, 0); err != nil {
					s.bumpHashDone()
					continue
				}
				s.cache.put(key, h)
			}
		}
		s.mu.Lock()
		j.e.Hash = h
		s.dupDirty = true
		s.hashDone++
		s.mu.Unlock()
	}
	s.cache.save()

	s.mu.Lock()
	defer s.mu.Unlock()
	again := s.moved && s.gen == gen
	if !again {
		s.hashing = false
	}
	return again
}

func (s *Store) stale(gen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen != gen
}

func (s *Store) setHashTotal(n int) {
	s.mu.Lock()
	s.hashDone, s.hashTotal = 0, n
	s.mu.Unlock()
}

func (s *Store) bumpHashDone() {
	s.mu.Lock()
	s.hashDone++
	s.mu.Unlock()
}

func (s *Store) dupIndexLocked() map[string]*dupGroup {
	if !s.dupDirty && s.dups != nil {
		return s.dups
	}
	m := map[string]*dupGroup{}
	group := func(h string) *dupGroup {
		g := m[h]
		if g == nil {
			g = &dupGroup{}
			m[h] = g
		}
		return g
	}
	for _, id := range s.order {
		if e := s.pending[id]; e.Hash != "" {
			g := group(e.Hash)
			g.pending = append(g.pending, e)
		}
	}
	for _, e := range s.validated {
		if e.Hash != "" {
			g := group(e.Hash)
			g.validated = append(g.validated, e)
		}
	}
	for h, g := range m {
		if len(g.pending) == 0 || len(g.pending)+len(g.validated) < 2 {
			delete(m, h)
			continue
		}
		sort.Slice(g.validated, func(i, j int) bool { return g.validated[i].Rel < g.validated[j].Rel })
	}
	s.dups, s.dupDirty = m, false
	return m
}

func (s *Store) itemLocked(e *entry) FileItem {
	it := FileItem{
		ID:              e.ID,
		Name:            filepath.Base(filepath.FromSlash(e.Rel)),
		RelPath:         e.Rel,
		Path:            filepath.Join(s.cfg.Source, filepath.FromSlash(e.Rel)),
		Size:            e.Size,
		Modified:        e.Mod.Unix(),
		Ext:             strings.TrimPrefix(strings.ToLower(filepath.Ext(e.Rel)), "."),
		ValidatedCopies: []string{},
	}
	it.Kind = kindOf(it.Ext)
	if e.Hash != "" {
		if g := s.dupIndexLocked()[e.Hash]; g != nil {
			it.DupCount = len(g.pending) - 1
			for _, v := range g.validated {
				it.ValidatedCopies = append(it.ValidatedCopies, v.Rel)
			}
		}
	}
	return it
}

func kindOf(ext string) string {
	switch ext {
	case "jpg", "jpeg", "png", "gif", "bmp", "webp", "heic", "heif", "tif", "tiff", "svg", "raw", "cr2", "nef", "arw", "dng":
		return "image"
	case "mp4", "mov", "m4v", "avi", "mkv", "webm", "wmv", "mpg", "mpeg", "3gp", "mts":
		return "video"
	case "mp3", "wav", "flac", "aac", "m4a", "ogg", "wma", "aiff":
		return "audio"
	case "pdf":
		return "pdf"
	case "txt", "md", "log", "csv", "json", "xml", "yml", "yaml", "ini", "html", "css", "js", "ts", "py", "go", "c", "h", "cpp", "java", "cs", "sh", "bat", "ps1", "sql", "rtf":
		return "text"
	case "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "pages", "numbers", "key":
		return "document"
	case "zip", "rar", "7z", "tar", "gz", "iso":
		return "archive"
	}
	return "other"
}

// Files returns the head of the queue.
func (s *Store) Files(limit int) ([]FileItem, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit > len(s.order) {
		limit = len(s.order)
	}
	out := make([]FileItem, 0, limit)
	for _, id := range s.order[:limit] {
		out = append(out, s.itemLocked(s.pending[id]))
	}
	return out, len(s.order)
}

func (s *Store) PathOf(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.pending[id]
	if !ok {
		return "", false
	}
	return filepath.Join(s.cfg.Source, filepath.FromSlash(e.Rel)), true
}

func (s *Store) removeFromOrderLocked(id string) {
	for i, x := range s.order {
		if x == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}

func (s *Store) Decide(id, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decideLocked(id, action)
}

func (s *Store) decideLocked(id, action string) error {
	e, ok := s.pending[id]
	if !ok {
		return errNotFound
	}
	if action == "skip" {
		s.removeFromOrderLocked(id)
		s.order = append(s.order, id)
		return nil
	}
	var root string
	switch action {
	case "keep":
		root = s.cfg.Validated
	case "delete":
		root = s.cfg.Trash
	default:
		return errBadAction
	}
	src := filepath.Join(s.cfg.Source, filepath.FromSlash(e.Rel))
	if _, err := os.Stat(src); err != nil {
		delete(s.pending, id)
		s.removeFromOrderLocked(id)
		s.dupDirty = true
		return errGone
	}
	dst := uniquePath(filepath.Join(root, filepath.FromSlash(e.Rel)))
	if err := moveFile(src, dst); err != nil {
		return err
	}
	delete(s.pending, id)
	s.removeFromOrderLocked(id)
	j := journalItem{Action: action, E: e, From: src, To: dst}
	if action == "keep" {
		rel, _ := filepath.Rel(s.cfg.Validated, dst)
		j.ValRel = filepath.ToSlash(rel)
		s.validated[j.ValRel] = &entry{ID: idFor(j.ValRel), Rel: j.ValRel, Size: e.Size, Mod: e.Mod, Hash: e.Hash}
		if e.Hash != "" {
			s.cache.put(cacheKey("full", dst, e.Size, e.Mod), e.Hash)
		}
		s.kept++
	} else {
		s.trashed++
	}
	s.journal = append(s.journal, j)
	if len(s.journal) > 500 {
		s.journal = s.journal[len(s.journal)-500:]
	}
	s.dupDirty, s.moved = true, true
	s.logAction(action, src, dst)
	return nil
}

// Undo puts the last kept/deleted file back at the front of the queue.
func (s *Store) Undo() (FileItem, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.journal) == 0 {
		return FileItem{}, "", errNoUndo
	}
	j := s.journal[len(s.journal)-1]
	if _, err := os.Lstat(j.From); err == nil {
		return FileItem{}, "", errors.New("l'emplacement d'origine est de nouveau occupé")
	}
	if err := moveFile(j.To, j.From); err != nil {
		return FileItem{}, "", err
	}
	s.journal = s.journal[:len(s.journal)-1]
	if j.Action == "keep" {
		delete(s.validated, j.ValRel)
		s.kept--
	} else {
		s.trashed--
	}
	s.pending[j.E.ID] = j.E
	s.removeFromOrderLocked(j.E.ID)
	s.order = append([]string{j.E.ID}, s.order...)
	s.dupDirty, s.moved = true, true
	s.logAction("undo", j.To, j.From)
	return s.itemLocked(j.E), j.Action, nil
}

func (s *Store) Duplicates(limit int) []DuplicateGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []DuplicateGroup{}
	for h, g := range s.dupIndexLocked() {
		dg := DuplicateGroup{Hash: h, Size: g.pending[0].Size, ValidatedCopies: []string{}}
		for _, e := range g.pending {
			dg.Files = append(dg.Files, s.itemLocked(e))
		}
		for _, v := range g.validated {
			dg.ValidatedCopies = append(dg.ValidatedCopies, v.Rel)
		}
		out = append(out, dg)
	}
	wasted := func(g DuplicateGroup) int64 {
		return g.Size * int64(len(g.Files)+len(g.ValidatedCopies)-1)
	}
	sort.Slice(out, func(i, j int) bool {
		if wi, wj := wasted(out[i]), wasted(out[j]); wi != wj {
			return wi > wj
		}
		return out[i].Hash < out[j].Hash
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ResolveDuplicates keeps keepID and deletes the other pending copies.
// With an empty keepID a validated copy must already exist.
func (s *Store) ResolveDuplicates(hash, keepID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.dupIndexLocked()[hash]
	if g == nil {
		return errNotFound
	}
	found := false
	for _, e := range g.pending {
		found = found || e.ID == keepID
	}
	if keepID != "" && !found {
		return errNotFound
	}
	if keepID == "" && len(g.validated) == 0 {
		return errors.New("aucune copie validée : choisis le fichier à garder")
	}
	pending := append([]*entry(nil), g.pending...)
	if keepID != "" {
		if err := s.decideLocked(keepID, "keep"); err != nil {
			return err
		}
	}
	for _, e := range pending {
		if e.ID == keepID {
			continue
		}
		if err := s.decideLocked(e.ID, "delete"); err != nil && !errors.Is(err, errGone) {
			return err
		}
	}
	return nil
}

func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	host, _ := os.Hostname()
	return Status{
		Name: host, Source: s.cfg.Source, Validated: s.cfg.Validated, Trash: s.cfg.Trash,
		Pending: len(s.order), Kept: s.kept, Trashed: s.trashed, ValidatedTotal: len(s.validated),
		Scanning: s.scanning, Hashing: s.hashing, HashDone: s.hashDone, HashTotal: s.hashTotal,
		DuplicateGroups: len(s.dupIndexLocked()), CanUndo: len(s.journal) > 0,
	}
}

// logAction appends to a CSV journal so every move can be traced later.
func (s *Store) logAction(action, from, to string) {
	f, err := os.OpenFile(filepath.Join(s.dataDir, "fileswiper-journal.csv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("journal : %v", err)
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{time.Now().Format(time.RFC3339), action, from, to})
	w.Flush()
}
