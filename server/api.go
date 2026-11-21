package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

func newAPI(s *Store) http.Handler {
	mux := http.NewServeMux()
	thumbSem := make(chan struct{}, 3)

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.Status())
	})
	mux.HandleFunc("GET /api/files", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 200 {
			limit = 20
		}
		files, pending := s.Files(limit)
		writeJSON(w, 200, map[string]any{"files": files, "pending": pending})
	})
	mux.HandleFunc("POST /api/files/{id}/decision", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeErr(w, errBadAction)
			return
		}
		if err := s.Decide(r.PathValue("id"), body.Action); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/undo", func(w http.ResponseWriter, r *http.Request) {
		item, action, err := s.Undo()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"file": item, "action": action})
	})
	mux.HandleFunc("GET /api/files/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		abs, ok := s.PathOf(r.PathValue("id"))
		if !ok {
			writeErr(w, errNotFound)
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			writeErr(w, errGone)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			writeErr(w, errGone)
			return
		}
		http.ServeContent(w, r, filepath.Base(abs), st.ModTime(), f)
	})
	mux.HandleFunc("GET /api/files/{id}/thumb", func(w http.ResponseWriter, r *http.Request) {
		abs, ok := s.PathOf(r.PathValue("id"))
		if !ok {
			writeErr(w, errNotFound)
			return
		}
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		if size < 64 || size > 2000 {
			size = 900
		}
		thumbSem <- struct{}{}
		defer func() { <-thumbSem }()
		img, err := thumbnail(abs, size)
		if err != nil {
			http.Error(w, `{"error":"aperçu indisponible"}`, http.StatusUnsupportedMediaType)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_ = jpeg.Encode(w, img, &jpeg.Options{Quality: 82})
	})
	mux.HandleFunc("GET /api/duplicates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"groups": s.Duplicates(200)})
	})
	mux.HandleFunc("POST /api/duplicates/{hash}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			KeepID string `json:"keepId"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
		if err := s.ResolveDuplicates(r.PathValue("hash"), body.KeepID); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/rescan", func(w http.ResponseWriter, r *http.Request) {
		go s.Rescan()
		writeJSON(w, 202, map[string]bool{"ok": true})
	})

	return auth(s.cfg.Token, mux)
}

func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("token") // media players cannot set headers
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "code d'accès invalide"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func thumbnail(abs string, size int) (image.Image, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > 120_000_000 {
		return nil, errors.New("image trop grande")
	}
	img, err := imaging.Open(abs, imaging.AutoOrientation(true))
	if err != nil {
		return nil, err
	}
	return imaging.Fit(img, size, size, imaging.Linear), nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, errNotFound):
		code = http.StatusNotFound
	case errors.Is(err, errGone):
		code = http.StatusGone
	case errors.Is(err, errBadAction):
		code = http.StatusBadRequest
	case errors.Is(err, errNoUndo):
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
