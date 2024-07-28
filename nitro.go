package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"text/template"
	"time"
)

var (
	port      int
	dir, file string
)

func main() {
	// var port int
	// var dir, file string
	var wanthelp bool

	flag.IntVar(&port, "p", 80, "port to listen on")
	flag.StringVar(&dir, "d", ".", "directory to serve")
	flag.StringVar(&file, "f", "index.html", "default file to look in a directory")
	flag.BoolVar(&wanthelp, "h", false, "help")
	flag.Parse()

	if wanthelp {
		help()
	}

	fmt.Println("Nitro !!")
	fmt.Println("Listening on port:", port)
	fmt.Println("Serving files at: ", dir)
	fmt.Println("defaulting to:    ", file)

	http.HandleFunc("/", serveFiles)
	http.HandleFunc("/time", serveTime)
	http.HandleFunc("/version", version)

	log.Printf("Serving %s on HTTP port: %v\n", dir, port)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(port), nil))
}

func help() {
	fmt.Printf(`
So you want some help here.

nitro   by default will listen on port 80 and serve files in the current directory
nitro -h        this help...
nitro -p 1000   listen on port 1000
nitro -d /test  serve files in the /test directory
`)
	os.Exit(0)
}

func serveFiles(w http.ResponseWriter, r *http.Request) {
	fmt.Println("HandleFunc `/`")
	url := dir + r.URL.Path
	fmt.Printf("%q is serving file: %s\n", "/", url)
	http.ServeFile(w, r, url)
}

func version(w http.ResponseWriter, r *http.Request) {
	log.Println("version")
	url := "version.html"

	tmpl, err := template.ParseFiles(url)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := struct {
		Version string
	}{
		Version: "0.0.1",
	}
	tmpl.Execute(w, data)
}

func serveTime(w http.ResponseWriter, r *http.Request) {
	log.Println("Time")
	w.Write([]byte("Server Time: " + time.Now().Format(time.RFC1123Z)))
}
