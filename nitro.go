package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"
)

var nitroVersion string = "0.0.3"

//go:embed static/*
var staticFiles embed.FS

func main() {

	type config struct {
		port string
		dir  string
	}

	var cfg config

	flag.StringVar(&cfg.port, "port", "8080", "Port to listen on")
	flag.StringVar(&cfg.dir, "dir", ".", "Directory to serve")
	flag.Parse()

	// if serving files from the current working dir, display what that dir is.
	if cfg.dir == "." {
		dir, err := os.Getwd()
		if err != nil {
			cfg.dir = "."
		}
		cfg.dir = dir
	}

	// Modified file system so that a directory without index.html doesn't return a directory listing to the user
	// which is the stupid default action of http.FileServer
	fs := noDirFileSystem{http.Dir(cfg.dir)}
	fileServer := http.FileServer(fs)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /nitro_time", serveTime)
	mux.HandleFunc("GET /nitro_version", version)
	mux.HandleFunc("/", createFileServerHandler(fileServer, cfg.dir))

	server := &http.Server{
		Addr:         ":" + cfg.port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Serving %s on http://localhost:%s", cfg.dir, cfg.port)
	log.Printf("Additional endpoints:")
	log.Printf("  GET  /time  - Get current server time")
	log.Fatal(server.ListenAndServe())
}

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

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
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

// Add this function for your custom root page
func serveCustomRootPage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Content-Type", "text/html")

	content, err := staticFiles.ReadFile("static/defaultIndex.html")
	if err != nil {
		http.Error(w, "Error loading default index", http.StatusInternalServerError)
		return
	}
	log.Printf("[%s] %s Custom Root Page (%d %s) %v", r.Method, r.URL.Path, 200, http.StatusText(200), time.Since(start))
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

func serveTime(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	currentTime := time.Now()
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "%s", []byte(currentTime.Format(time.RFC3339)))
}

func version(w http.ResponseWriter, r *http.Request) {
	log.Printf("doing the version thing")
	tmpl, err := template.ParseFS(staticFiles, "static/version.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := struct {
		Version string
	}{
		Version: nitroVersion,
	}
	tmpl.Execute(w, data)
}
