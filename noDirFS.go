package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

type noDirFileSystem struct {
	fs http.FileSystem
}

func (ndfs noDirFileSystem) Open(path string) (http.File, error) {
	file, err := ndfs.fs.Open(path)
	if err != nil {
		return nil, err
	}

	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	// If request points to a directory, check if index.html exists in there.
	// Return it if it does, otherwise 404.
	if fileInfo.IsDir() {
		index := path + "/index.html"
		if _, err := ndfs.fs.Open(index); err != nil {
			file.Close()
			return nil, os.ErrNotExist
		}
	}

	return file, nil
}

func createFileServerHandler(fileServer http.Handler, dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Wrap the response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		if r.URL.Path == "/" {
			fs := http.Dir(dir)
			_, err := fs.Open("index.html")
			if err == nil {
				// index.html exist, so serve that normally
				fileServer.ServeHTTP(wrapped, r)
				log.Printf("[%s] %s (%d %s) %v", r.Method, r.URL.Path, wrapped.statusCode, http.StatusText(wrapped.statusCode), time.Since(start))
				return
			}
			// index.html doesn't exist - serve custom page
			serveCustomRootPage(w, r)
			log.Printf("[%s] %s (%d %s) %v", r.Method, r.URL.Path, wrapped.statusCode, http.StatusText(wrapped.statusCode), time.Since(start))
			return

		}

		fileServer.ServeHTTP(wrapped, r)
		log.Printf("[%s] %s (%d %s) %v", r.Method, r.URL.Path, wrapped.statusCode, http.StatusText(wrapped.statusCode), time.Since(start))
	}
}
