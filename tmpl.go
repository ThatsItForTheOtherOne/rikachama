package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type BoardPage struct {
	Config
	Posts   []Post
	Page    int
	Total   int
	PrevURL string
	NextURL string
}

type ThreadPage struct {
	Config
	Thread Post
}

type AdminPage struct {
	Config
	Posts          []Post
	TotalFileBytes int64
}

type AdminLoginPage struct {
	Config
	Error string
}

type AdminPasswordPage struct {
	Config
	Error   string
	Success bool
}

//go:embed templates/*.html static/*
var files embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"T":                 T,
	"jsMessages":        jsMessages,
	"acceptedFileTypes": acceptedFileTypes,
	"formatBody":        formatBody,
	"formatTime":        formatTime,
	"pageURL":           pageURL,
	"pageRange":         pageRange,
}).ParseFS(files, "templates/*.html"))

var quoteLineRe = regexp.MustCompile(`(^|<br>)(&gt;[^<]*)`)

func render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		log.Println(err)
		return
	}
	if _, err := buf.WriteTo(w); err != nil {
		log.Println(err)
	}
}

func (a *App) newBoardPage(posts []Post, page, total int) BoardPage {
	var prevURL, nextURL string
	if page > 0 {
		prevURL = pageURL(page - 1)
	}
	if page < maxPage(total) {
		nextURL = pageURL(page + 1)
	}
	return BoardPage{
		Config:  a.cfg,
		Posts:   posts,
		Page:    page,
		Total:   total,
		PrevURL: prevURL,
		NextURL: nextURL,
	}
}

// Template helpers

func formatTime(t time.Time) string {
	weekdays := strings.Split(T("date.weekdays"), ",")
	jst := t.UTC().Add(9 * time.Hour)
	return fmt.Sprintf("%s(%s)%s",
		jst.Format("06/01/02"),
		weekdays[jst.Weekday()],
		jst.Format("15:04"),
	)
}

func formatBody(s string) template.HTML {
	s = html.EscapeString(s)
	s = newlinesToBreaks(s)
	s = colorizeQuotes(s)
	return template.HTML(s)
}

func newlinesToBreaks(s string) string {
	return strings.ReplaceAll(s, "\n", "<br>")
}

func colorizeQuotes(s string) string {
	return quoteLineRe.ReplaceAllString(s, `${1}<span class="quote">${2}</span>`)
}

func pageURL(n int) string {
	if n == 0 {
		return "/"
	}
	return fmt.Sprintf("/%d", n)
}

func pageRange(total int) []int {
	n := (total + postsPerPage - 1) / postsPerPage
	s := make([]int, n)
	for i := range s {
		s[i] = i
	}
	return s
}

func maxPage(total int) int {
	return (total - 1) / postsPerPage
}

func jsMessages() (template.JS, error) {
	clientMessages := map[string]string{
		"post.minimize": T("post.minimize"),
	}
	jsonedMessages, err := json.Marshal(clientMessages)
	if err != nil {
		return "", err
	}
	return template.JS(jsonedMessages), nil
}
