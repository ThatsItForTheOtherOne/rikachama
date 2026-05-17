package main

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	_ "modernc.org/sqlite"
)

const postsPerPage = 10

type Config struct {
	Title          string        `json:"title"`
	Home           string        `json:"home"`
	AddInfo        template.HTML `json:"add_info"`
	MaxKB          int           `json:"max_kb"`
	MaxW           int           `json:"max_w"`
	MaxH           int           `json:"max_h"`
	SanitizerImage string        `json:"sanitizer_image"`
}

type Post struct {
	ID                                             int
	PostedAt                                       time.Time
	Author                                         string
	Email                                          string
	Subject                                        string
	Body                                           string
	FilePath                                       string
	ThumbnailPath                                  string
	Width, Height, ThumbnailWidth, ThumbnailHeight int64
	FileSize                                       int64
	MimeType                                       string
	Replies                                        []Post
}

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

type App struct {
	db  *sql.DB
	cfg Config
}

type mimeSpec struct {
	sanitizeCommand, thumbnailCommand string
	ext                               string
}

const postColumns = `id, posted_at, author, email, subject, body,
    file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type, width, height`

type postScanner interface {
	Scan(dest ...any) error
}

type unknownTypeError struct {
	MimeType string
}

func (e *unknownTypeError) Error() string {
	return fmt.Sprintf("unknown file type: %s", e.MimeType)
}

func scanPost(s postScanner) (Post, error) {
	var p Post
	var postedAt int64
	if err := s.Scan(
		&p.ID, &postedAt, &p.Author, &p.Email,
		&p.Subject, &p.Body, &p.FilePath, &p.ThumbnailPath,
		&p.ThumbnailWidth, &p.ThumbnailHeight, &p.FileSize, &p.MimeType,
		&p.Width, &p.Height,
	); err != nil {
		return Post{}, err
	}
	p.PostedAt = time.Unix(postedAt, 0)
	return p, nil
}

//go:embed templates/*.html static/*
var files embed.FS

//go:embed schema.sql
var schema string

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"formatBody": formatBody,
	"formatTime": formatTime,
	"pageURL":    pageURL,
	"pageRange":  pageRange,
}).ParseFS(files, "templates/*.html"))

var mimeSpecs = map[string]mimeSpec{
	"image/jpeg":                    {"image", "image-thumb", ".jpg"},
	"image/png":                     {"image", "image-thumb", ".png"},
	"image/gif":                     {"image-gif", "image-thumb", ".gif"},
	"image/webp":                    {"image", "image-thumb", ".webp"},
	"video/mp4":                     {"video", "video-thumb", ".mp4"},
	"video/webm":                    {"video", "video-thumb", ".webm"},
	"application/pdf":               {"pdf", "pdf-thumb", ".pdf"},
	"application/x-shockwave-flash": {"", "", ".swf"},
}

var quoteLineRe = regexp.MustCompile(`(^|<br>)(&gt;[^<]*)`)

func main() {
	if runtime.GOOS != "linux" {
		log.Fatalf("unsupported OS: %s (gvisor requires linux)", runtime.GOOS)
	}
	f, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("failed to open config: %v", err)
	}
	defer f.Close()
	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}
	u, err := url.Parse(cfg.Home)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("invalid home URL %q: must be an absolute http(s) URL", cfg.Home)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		log.Fatalf("home URL scheme must be http or https, got %q", u.Scheme)
	}
	if err := buildImage(cfg.SanitizerImage); err != nil {
		log.Fatalf("failed to build sanitizer image: %v", err)
	}

	db, err := sql.Open("sqlite", "./rikachama.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	fmt.Println("Successfully connected to SQLite database")
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	app := &App{db: db, cfg: cfg}

	http.Handle("/static/", http.FileServerFS(files))
	http.Handle("/upload/", http.StripPrefix("/upload/", http.FileServer(http.Dir("upload"))))

	http.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		posts, total, err := app.getThreads(0)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			log.Println(err)
			return
		}
		render(w, "board.html", app.newBoardPage(posts, 0, total))
	})
	http.HandleFunc("GET /{page}", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("page")
		page, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		posts, total, err := app.getThreads(page)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			log.Println(err)
			return
		}
		if total > 0 && page > maxPage(total) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		render(w, "board.html", app.newBoardPage(posts, page, total))
	})

	http.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, int64(cfg.MaxFileSize()))
		if err := app.handlePost(r, 0); err != nil {
			log.Println(err)
			var maxErr *http.MaxBytesError
			var ufe *unknownTypeError
			if errors.As(err, &maxErr) {
				http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
				return
			}
			if errors.As(err, &ufe) {
				http.Error(w, "Unknown file type", http.StatusUnsupportedMediaType)
				return
			}
			if errors.Is(err, errors.ErrUnsupported) {
				http.Error(w, "Unimplemented media type", http.StatusNotImplemented)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	http.HandleFunc("GET /thread/{id}", func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		thread, err := app.getThread(id)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Not Found", http.StatusNotFound)
			log.Println(err)
			return
		} else if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			log.Println(err)
			return
		}
		render(w, "thread.html", ThreadPage{Config: app.cfg, Thread: thread})
	})
	http.HandleFunc("POST /thread/{id}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, int64(cfg.MaxFileSize()))
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		if err := app.handlePost(r, id); err != nil {
			log.Println(err)
			var maxErr *http.MaxBytesError
			var ufe *unknownTypeError
			if errors.As(err, &maxErr) {
				http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
				return
			}
			if errors.As(err, &ufe) {
				http.Error(w, "Unknown file type", http.StatusUnsupportedMediaType)
				return
			}
			if errors.Is(err, errors.ErrUnsupported) {
				http.Error(w, "Unimplemented media type", http.StatusNotImplemented)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/thread/"+strconv.Itoa(id), http.StatusSeeOther)

	})
	log.Println("Server is serving requests!")
	log.Fatal(http.ListenAndServe(":3200", nil))
}

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

func buildImage(sanitizerImage string) error {
	cmd := exec.Command("podman", "build", "-t", sanitizerImage, "sanitizer/")
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return err
}

// App methods

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

func (a *App) getThreads(page int) ([]Post, int, error) {
	var total int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE reply_to = 0`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := a.db.Query(`
	SELECT `+postColumns+`
	FROM posts
	WHERE reply_to = 0
	ORDER BY bumped_at DESC
	LIMIT ?
	OFFSET ?
	`, postsPerPage, page*postsPerPage)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	ops := []Post{}
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, 0, err
		}
		ops = append(ops, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range ops {
		rows, err := a.db.Query(`
		SELECT * FROM (
			SELECT `+postColumns+`
			FROM posts
			WHERE reply_to = ?
			ORDER BY id DESC
			LIMIT 3
		) ORDER BY id ASC`, ops[i].ID)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			p, err := scanPost(rows)
			if err != nil {
				rows.Close()
				return nil, 0, err
			}
			ops[i].Replies = append(ops[i].Replies, p)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, 0, err
		}
	}

	return ops, total, nil
}

func (a *App) getThread(threadID int) (Post, error) {
	op, err := scanPost(a.db.QueryRow(`
	SELECT `+postColumns+`
	FROM posts
	WHERE id = ?
	`, threadID))
	if err != nil {
		return Post{}, err
	}

	rows, err := a.db.Query(`
	SELECT `+postColumns+`
	FROM posts
	WHERE reply_to = ?
	ORDER BY id ASC
	`, threadID)
	if err != nil {
		return Post{}, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			rows.Close()
			return Post{}, err
		}
		op.Replies = append(op.Replies, p)
	}
	if err := rows.Err(); err != nil {
		return Post{}, err
	}
	return op, nil
}

func (a *App) handlePost(r *http.Request, threadID int) error {
	err := r.ParseMultipartForm(int64(a.cfg.MaxFileSize()))
	if err != nil {
		return err
	}
	author := r.FormValue("name")
	if author == "" {
		author = "名無しさん"
	}
	email := r.FormValue("email")
	subject := r.FormValue("sub")
	body := r.FormValue("com")
	var filePath, thumbnailPath, mimeType string
	var height, width, thumbnailWidth, thumbnailHeight, fileSize int64
	file, _, err := r.FormFile("upfile")
	if err == nil {
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		mimeType = detectMime(buf[:n])
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if mimeType == "application/x-shockwave-flash" {
			return fmt.Errorf("SWF: %w", errors.ErrUnsupported)
		}
		if _, ok := mimeSpecs[mimeType]; !ok {
			return &unknownTypeError{MimeType: mimeType}
		}

		filePath, thumbnailPath, err = a.saveFile(file, mimeType)
		if err != nil {
			return err
		}
		info, err := os.Stat("upload/" + filePath)
		if err != nil {
			return err
		}
		fileSize = info.Size()
		if thumbnailPath != "" {
			tf, err := os.Open("upload/" + thumbnailPath)
			if err != nil {
				return err
			}
			imgCfg, _, err := image.DecodeConfig(tf)
			tf.Close()
			if err != nil {
				return err
			}
			thumbnailWidth = int64(imgCfg.Width)
			thumbnailHeight = int64(imgCfg.Height)
		}
		if filePath != "" && strings.HasPrefix(mimeType, "image/") {
			f, err := os.Open("upload/" + filePath)
			if err != nil {
				return err
			}
			imgCfg, _, err := image.DecodeConfig(f)
			f.Close()
			if err != nil {
				return err
			}
			width = int64(imgCfg.Width)
			height = int64(imgCfg.Height)
		}

	}
	now := time.Now().Unix()
	_, err = a.db.Exec(`
	INSERT INTO posts
    	(reply_to, posted_at, bumped_at, author, email, subject, body,
    	 file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type,
		 width, height)
	VALUES
    	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		threadID, now, now,
		author, email, subject, body,
		filePath, thumbnailPath,
		thumbnailWidth, thumbnailHeight, fileSize, mimeType,
		width, height,
	)
	if err != nil {
		return err
	}
	if threadID != 0 && !strings.EqualFold(email, "sage") {
		_, err = a.db.Exec(`UPDATE posts SET bumped_at = ? WHERE id = ?`, now, threadID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) saveFile(file multipart.File, mimeType string) (string, string, error) {
	ft, ok := mimeSpecs[mimeType]
	if !ok {
		return "", "", fmt.Errorf("unsupported file type: %s", mimeType)
	}

	var fileData, thumbData []byte
	if ft.sanitizeCommand != "" {
		var err error
		fileData, thumbData, err = a.cfg.process(file, ft.sanitizeCommand, ft.thumbnailCommand)
		if err != nil {
			return "", "", fmt.Errorf("process %s: %w", mimeType, err)
		}
	} else {
		data, err := io.ReadAll(file)
		if err != nil {
			return "", "", fmt.Errorf("read upload: %w", err)
		}
		if mimeType == "application/x-shockwave-flash" && !isSWF(data) {
			return "", "", fmt.Errorf("invalid SWF file")
		}
		fileData = data
	}

	base := fmt.Sprintf("%d", time.Now().UnixNano())
	filePath := base + ft.ext
	if err := os.WriteFile("upload/"+filePath, fileData, 0644); err != nil {
		return "", "", fmt.Errorf("write file: %w", err)
	}

	var thumbPath string
	if thumbData != nil {
		thumbPath = base + "_thumb.jpg"
		if err := os.WriteFile("upload/"+thumbPath, thumbData, 0644); err != nil {
			return "", "", fmt.Errorf("write thumb: %w", err)
		}
	}

	return filePath, thumbPath, nil
}

// Config methods

func (c Config) MaxFileSize() int {
	return c.MaxKB * 1024
}

func (c Config) podmanRun(args ...string) *exec.Cmd {
	base := []string{
		"run", "--rm", "-i",
		"--runtime=runsc",
		"--network=none",
		"--pull=never",
		"--cap-drop=all",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs", "/tmp:rw,noexec,nosuid,size=256m",
		"--pids-limit=100",
		"--memory=512m",
		c.SanitizerImage,
	}
	return exec.Command("podman", append(base, args...)...)
}

func (c Config) process(r io.Reader, fileCmd, thumbCmd string) ([]byte, []byte, error) {
	input, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	var fileOut, thumbOut bytes.Buffer
	g := new(errgroup.Group)

	g.Go(func() error {
		fc := c.podmanRun(fileCmd)
		fc.Stdin, fc.Stdout, fc.Stderr = bytes.NewReader(input), &fileOut, os.Stderr
		return fc.Run()
	})

	g.Go(func() error {
		tc := c.podmanRun(thumbCmd)
		tc.Stdin, tc.Stdout, tc.Stderr = bytes.NewReader(input), &thumbOut, os.Stderr
		return tc.Run()
	})

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	return fileOut.Bytes(), thumbOut.Bytes(), nil
}

// File detection helpers

func isSWF(b []byte) bool {
	if len(b) < 3 {
		return false
	}
	sig := string(b[:3])
	return sig == "FWS" || sig == "CWS" || sig == "ZWS"
}

func detectMime(buf []byte) string {
	if isSWF(buf) {
		return "application/x-shockwave-flash"
	}
	return http.DetectContentType(buf)
}

// Template helpers

func formatTime(t time.Time) string {
	weekdays := [...]string{"日", "月", "火", "水", "木", "金", "土"}
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

// Page navigation helpers

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
