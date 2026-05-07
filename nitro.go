package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

var nitroVersion string = "0.0.4"

//go:embed static/*
var staticFiles embed.FS

func main() {
	fmt.Println("NITRO")
	var userCfg Config

	flag.StringVar(&userCfg.Port, "port", "", "Port to listen on (default '8080')")
	flag.StringVar(&userCfg.Dir, "dir", "", "Directory to serve (default .)")
	flag.Parse()

	config := loadConfig("config.toml", userCfg)

	// Modified file system so that a directory without index.html doesn't return a directory listing to the user
	// which is the stupid default action of http.FileServer
	fs := noDirFileSystem{http.Dir(config.Dir)}
	fileServer := http.FileServer(fs)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /nitro_time", serveTime)
	mux.HandleFunc("GET /nitro_version", version)
	mux.HandleFunc("/", createFileServerHandler(fileServer, config.Dir))

	server := &http.Server{
		Addr:         ":" + config.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Serving %s on http://localhost:%s", config.Dir, config.Port)
	log.Printf("Additional endpoints:")
	log.Printf("  GET /nitro_time    - Get current server time")
	log.Printf("  GET /nitro_version - get nitro version")
	log.Fatal(server.ListenAndServe())
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
