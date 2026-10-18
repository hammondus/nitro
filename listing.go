package main

import (
	"bytes"
	"cmp"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// serveListing renders the directory listing for rel (canon is the URL
// path, always ending in "/"). Rendered into a buffer first, so a
// failure is a clean 500 rather than half a page with a 200.
func (h *staticHandler) serveListing(w http.ResponseWriter, r *http.Request, rel, canon string) {
	f, err := h.root.Open(rel)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	key, desc := sortParams(r)
	data := listingData{
		Path:     canon,
		Crumbs:   breadcrumbs(canon),
		Entries:  listEntries(entries, key, desc),
		NameLink: headerLink("name", key, desc),
		SizeLink: headerLink("size", key, desc),
		TimeLink: headerLink("time", key, desc),
	}
	data.Marker = func(col string) string {
		if col != key {
			return ""
		}
		if desc {
			return " ▼"
		}
		return " ▲"
	}

	var buf bytes.Buffer
	if err := listingTmpl.Execute(&buf, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setCachePolicy(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes())
}

// sortParams reads ?sort= and ?desc=; anything unrecognised falls back
// to a name sort rather than erroring — a listing URL is often typed.
func sortParams(r *http.Request) (key string, desc bool) {
	key = r.URL.Query().Get("sort")
	if key != "size" && key != "time" {
		key = "name"
	}
	return key, r.URL.Query().Get("desc") == "1"
}

// headerLink builds the query string for a column header: clicking the
// current sort column reverses it, any other column sorts ascending.
func headerLink(col, key string, desc bool) string {
	if col == key && !desc {
		return "?sort=" + col + "&desc=1"
	}
	return "?sort=" + col
}

type listingData struct {
	Path                         string
	Crumbs                       []crumb
	Entries                      []listEntry
	NameLink, SizeLink, TimeLink string
	Marker                       func(string) string
}

type crumb struct {
	Name string
	Href string
}

type listEntry struct {
	Name  string
	Href  string
	IsDir bool
	Size  string
	Time  string
}

// breadcrumbs turns "/a/b/" into a root crumb plus one linked crumb per
// segment, each pointing at its own listing.
func breadcrumbs(canon string) []crumb {
	crumbs := []crumb{{Name: "/", Href: "/"}}
	href := "/"
	for seg := range strings.SplitSeq(strings.Trim(canon, "/"), "/") {
		if seg == "" {
			continue
		}
		href += url.PathEscape(seg) + "/"
		crumbs = append(crumbs, crumb{Name: seg, Href: href})
	}
	return crumbs
}

// listEntries filters dotfiles out, sorts directories ahead of files,
// and orders each group by the requested column.
func listEntries(entries []fs.DirEntry, key string, desc bool) []listEntry {
	type row struct {
		name  string
		isDir bool
		size  int64
		info  fs.FileInfo
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // deleted between ReadDir and Info
		}
		rows = append(rows, row{e.Name(), e.IsDir(), info.Size(), info})
	}

	slices.SortFunc(rows, func(a, b row) int {
		if a.isDir != b.isDir {
			if a.isDir {
				return -1
			}
			return 1
		}
		var c int
		switch key {
		case "size":
			c = cmp.Compare(a.size, b.size)
		case "time":
			c = a.info.ModTime().Compare(b.info.ModTime())
		}
		if c == 0 {
			c = strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
		}
		if desc {
			c = -c
		}
		return c
	})

	out := make([]listEntry, 0, len(rows))
	for _, r := range rows {
		e := listEntry{
			Name: r.name,
			Href: url.PathEscape(r.name),
			Time: r.info.ModTime().Format("2006-01-02 15:04"),
		}
		if r.isDir {
			e.IsDir = true
			e.Name += "/"
			e.Href += "/"
			e.Size = "—"
		} else {
			e.Size = humanSize(r.size)
		}
		out = append(out, e)
	}
	return out
}

// humanSize renders a byte count the way a person reads one: exact bytes
// under a kilobyte, one decimal place above.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n)/unit, 0
	for value >= unit && exp < 3 {
		value /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", value, "KMGT"[exp])
}

var listingTmpl = template.Must(template.New("listing").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Index of {{.Path}}</title>
<style>
  :root { color-scheme: light dark; }
  body {
    font: 15px/1.5 system-ui, sans-serif;
    margin: 2rem auto; max-width: 46rem; padding: 0 1rem;
    background: Canvas; color: CanvasText;
  }
  h1 { font-size: 1.1rem; font-weight: 600; word-break: break-all; }
  h1 a { color: inherit; }
  table { border-collapse: collapse; width: 100%; }
  th, td { text-align: left; padding: .3rem .75rem .3rem 0; }
  th { border-bottom: 1px solid color-mix(in srgb, CanvasText 25%, transparent); }
  th a { color: inherit; text-decoration: none; }
  td.size, th.size { text-align: right; white-space: nowrap; }
  td.time, th.time { white-space: nowrap; }
  td.name { word-break: break-all; }
  a { color: LinkText; text-decoration: none; }
  a:hover { text-decoration: underline; }
  p.empty { color: color-mix(in srgb, CanvasText 60%, transparent); }
</style>
<h1>{{range .Crumbs}}<a href="{{.Href}}">{{.Name}}</a>{{if ne .Name "/"}}/{{end}}{{end}}</h1>
{{if .Entries}}<table>
<tr>
  <th class="name"><a href="{{.NameLink}}">Name{{call .Marker "name"}}</a></th>
  <th class="size"><a href="{{.SizeLink}}">Size{{call .Marker "size"}}</a></th>
  <th class="time"><a href="{{.TimeLink}}">Modified{{call .Marker "time"}}</a></th>
</tr>
{{range .Entries}}<tr>
  <td class="name"><a href="{{.Href}}">{{.Name}}</a></td>
  <td class="size">{{.Size}}</td>
  <td class="time">{{.Time}}</td>
</tr>
{{end}}</table>
{{else}}<p class="empty">Empty directory.</p>
{{end}}</html>
`))
