package server

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

func frontendHandler(frontend fs.FS) http.Handler {
	dir := http.FS(frontend)
	files := http.FileServer(dir)
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		file, err := dir.Open("index.html")
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "failed to access frontend index", http.StatusInternalServerError)
			}
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			http.Error(w, "failed to access frontend index", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", info.ModTime(), file)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		cleanPath := path.Clean("/" + r.URL.Path)
		if cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/") || cleanPath == "/health" || strings.HasPrefix(cleanPath, "/health/") {
			http.NotFound(w, r)
			return
		}
		if cleanPath == "/" {
			serveIndex(w, r)
			return
		}

		file, err := dir.Open(cleanPath)
		if err == nil {
			info, statErr := file.Stat()
			closeErr := file.Close()
			if statErr != nil || closeErr != nil {
				http.Error(w, "failed to access frontend file", http.StatusInternalServerError)
				return
			}
			if info.IsDir() {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
			return
		}
		if !errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "failed to access frontend file", http.StatusInternalServerError)
			return
		}

		firstSegment := strings.SplitN(strings.TrimPrefix(cleanPath, "/"), "/", 2)[0]
		if path.Ext(cleanPath) != "" || !strings.Contains(r.Header.Get("Accept"), "text/html") ||
			firstSegment == "assets" || firstSegment == "mediapipe" ||
			firstSegment == "vad" || firstSegment == "onnxruntime" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r)
	})
}
