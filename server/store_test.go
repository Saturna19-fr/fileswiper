package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func setup(t *testing.T) (*Store, Config) {
	t.Helper()
	root := t.TempDir()
	cfg := Config{Source: filepath.Join(root, "src"), Token: "secret"}
	big := strings.Repeat("x", headSize+10)
	write(t, filepath.Join(cfg.Source, "a/b/photo.txt"), "hello")
	write(t, filepath.Join(cfg.Source, "a/copy.txt"), "hello")
	write(t, filepath.Join(cfg.Source, "c/unique.txt"), "other")
	write(t, filepath.Join(cfg.Source, "c/same-size.txt"), "hellO")
	write(t, filepath.Join(cfg.Source, "big1.bin"), big+"A")
	write(t, filepath.Join(cfg.Source, "big2.bin"), big+"A")
	write(t, filepath.Join(cfg.Source, "big3.bin"), big+"B") // same size and head, different tail
	write(t, filepath.Join(cfg.Source, "Thumbs.db"), "junk")
	if err := cfg.normalize(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(cfg.Validated, "old/kept.txt"), "hello")
	s := NewStore(cfg, root)
	s.Rescan()
	return s, cfg
}

func find(t *testing.T, s *Store, rel string) FileItem {
	t.Helper()
	files, _ := s.Files(100)
	for _, f := range files {
		if f.RelPath == rel {
			return f
		}
	}
	t.Fatalf("%s not in queue", rel)
	return FileItem{}
}

func TestScanAndDuplicates(t *testing.T) {
	s, _ := setup(t)
	files, pending := s.Files(100)
	if pending != 7 || len(files) != 7 {
		t.Fatalf("pending = %d, want 7 (junk skipped)", pending)
	}
	p := find(t, s, "a/b/photo.txt")
	if p.DupCount != 1 || len(p.ValidatedCopies) != 1 || p.ValidatedCopies[0] != "old/kept.txt" {
		t.Fatalf("photo dup info = %+v", p)
	}
	if f := find(t, s, "c/same-size.txt"); f.DupCount != 0 || len(f.ValidatedCopies) != 0 {
		t.Fatalf("same-size file flagged as duplicate: %+v", f)
	}
	if f := find(t, s, "big1.bin"); f.DupCount != 1 {
		t.Fatalf("big1 dupCount = %d, want 1", f.DupCount)
	}
	if f := find(t, s, "big3.bin"); f.DupCount != 0 {
		t.Fatalf("big3 differs only in its tail and must not be a duplicate")
	}
	if n := len(s.Duplicates(10)); n != 2 {
		t.Fatalf("duplicate groups = %d, want 2", n)
	}
}

func TestKeepDeleteUndo(t *testing.T) {
	s, cfg := setup(t)
	p := find(t, s, "a/b/photo.txt")
	if err := s.Decide(p.ID, "keep"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(cfg.Validated, "a/b/photo.txt")) || exists(filepath.Join(cfg.Source, "a/b/photo.txt")) {
		t.Fatal("keep must move the file into Validé with its tree")
	}
	if c := find(t, s, "a/copy.txt"); len(c.ValidatedCopies) != 2 || c.DupCount != 0 {
		t.Fatalf("copy should now see 2 validated copies: %+v", c)
	}

	u := find(t, s, "c/unique.txt")
	if err := s.Decide(u.ID, "delete"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(cfg.Trash, "c/unique.txt")) {
		t.Fatal("delete must move the file into Supprimé")
	}

	item, action, err := s.Undo()
	if err != nil || action != "delete" || item.RelPath != "c/unique.txt" {
		t.Fatalf("undo = %+v %s %v", item, action, err)
	}
	if !exists(filepath.Join(cfg.Source, "c/unique.txt")) {
		t.Fatal("undo must restore the file")
	}
	if files, _ := s.Files(1); files[0].ID != u.ID {
		t.Fatal("undone file must be back at the front")
	}
	if _, _, err := s.Undo(); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(cfg.Source, "a/b/photo.txt")) {
		t.Fatal("second undo must restore the kept file")
	}
	if st := s.Status(); st.Kept != 0 || st.Trashed != 0 || st.Pending != 7 || st.CanUndo {
		t.Fatalf("status after undo = %+v", st)
	}
}

func TestKeepNameCollision(t *testing.T) {
	s, cfg := setup(t)
	write(t, filepath.Join(cfg.Validated, "c/unique.txt"), "already there")
	if err := s.Decide(find(t, s, "c/unique.txt").ID, "keep"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(cfg.Validated, "c/unique.txt"))
	if string(b) != "already there" || !exists(filepath.Join(cfg.Validated, "c/unique (2).txt")) {
		t.Fatal("an existing validated file must never be overwritten")
	}
}

func TestSkipAndResolve(t *testing.T) {
	s, cfg := setup(t)
	files, _ := s.Files(1)
	first := files[0]
	if err := s.Decide(first.ID, "skip"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Files(100); all[len(all)-1].ID != first.ID {
		t.Fatal("skip must send the file to the back")
	}

	b1 := find(t, s, "big1.bin")
	var hash string
	for _, g := range s.Duplicates(10) {
		if g.Files[0].Name == "big1.bin" || g.Files[0].Name == "big2.bin" {
			hash = g.Hash
			if err := s.ResolveDuplicates(g.Hash, ""); err == nil {
				t.Fatal("resolving without a kept copy must fail")
			}
		}
	}
	if err := s.ResolveDuplicates(hash, b1.ID); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(cfg.Validated, "big1.bin")) || !exists(filepath.Join(cfg.Trash, "big2.bin")) {
		t.Fatal("resolve must keep one copy and trash the other")
	}
	if !exists(filepath.Join(cfg.Source, "big3.bin")) {
		t.Fatal("big3 must be untouched")
	}
}

func TestAPI(t *testing.T) {
	s, cfg := setup(t)
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 200)))
	write(t, filepath.Join(cfg.Source, "img/pic.png"), buf.String())
	s.Rescan()
	srv := httptest.NewServer(newAPI(s))
	defer srv.Close()

	do := func(method, path, token, body string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := do("GET", "/api/files", "", ""); res.StatusCode != 401 {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if res := do("GET", "/api/files", "wrong", ""); res.StatusCode != 401 {
		t.Fatalf("bad token: %d", res.StatusCode)
	}
	res := do("GET", "/api/files?limit=50", "secret", "")
	var list struct {
		Files   []FileItem
		Pending int
	}
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil || list.Pending != 8 {
		t.Fatalf("files: %v %+v", err, list)
	}
	pic := find(t, s, "img/pic.png")
	if pic.Kind != "image" {
		t.Fatalf("kind = %s", pic.Kind)
	}
	res = do("GET", "/api/files/"+pic.ID+"/thumb?size=100&token=secret", "", "")
	img, _, err := image.Decode(res.Body)
	if res.StatusCode != 200 || err != nil || img.Bounds().Dx() != 100 || img.Bounds().Dy() != 50 {
		t.Fatalf("thumb: %d %v", res.StatusCode, err)
	}
	if res := do("GET", "/api/files/"+find(t, s, "c/unique.txt").ID+"/thumb", "secret", ""); res.StatusCode != 415 {
		t.Fatalf("thumb of text: %d", res.StatusCode)
	}
	if res := do("GET", "/api/files/"+pic.ID+"/content", "secret", ""); res.StatusCode != 200 || res.ContentLength != int64(buf.Len()) {
		t.Fatalf("content: %d", res.StatusCode)
	}
	if res := do("POST", "/api/files/"+pic.ID+"/decision", "secret", `{"action":"keep"}`); res.StatusCode != 200 {
		t.Fatalf("decision: %d", res.StatusCode)
	}
	if res := do("POST", "/api/files/"+pic.ID+"/decision", "secret", `{"action":"keep"}`); res.StatusCode != 404 {
		t.Fatalf("second decision: %d", res.StatusCode)
	}
	if res := do("POST", "/api/files/nope/decision", "secret", `{"action":"burn"}`); res.StatusCode != 404 {
		t.Fatalf("unknown id: %d", res.StatusCode)
	}
	if res := do("POST", "/api/undo", "secret", ""); res.StatusCode != 200 {
		t.Fatalf("undo: %d", res.StatusCode)
	}
	if res := do("POST", "/api/undo", "secret", ""); res.StatusCode != 409 {
		t.Fatalf("empty undo: %d", res.StatusCode)
	}
}
