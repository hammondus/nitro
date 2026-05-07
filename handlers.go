package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"
)

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
