package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type Config struct {
	Title              string        `json:"title"`
	Home               string        `json:"home"`
	AddInfo            template.HTML `json:"add_info"`
	MaxKB              int           `json:"max_kb"`
	MaxW               int           `json:"max_w"`
	MaxH               int           `json:"max_h"`
	SanitizerImageBase string        `json:"sanitizer_image_base"`
	Lang               string        `json:"lang"`
	Database           string        `json:"database"`
	UploadPath         string        `json:"upload_path"`
}

type App struct {
	db             *sql.DB
	cfg            Config
	dev            bool
	sanitizerImage string
}

//go:embed schema.sql
var schema string

func main() {
	createAdmin := flag.String("create-admin", "", "Create an admin with the given username, then exit")
	devMode := flag.Bool("dev", false, "Enable developer mode")
	configName := flag.String("config", "config.json", "Specify a file to use as a config")
	bindPort := flag.Int("port", 3200, "Bind to a specific port (3200 default)")
	flag.Parse()
	if runtime.GOOS != "linux" {
		log.Fatalf("unsupported OS: %s (gvisor requires linux)", runtime.GOOS)
	}
	if *bindPort < 1 || *bindPort > 65535 {
		log.Fatalf("invalid port %d", *bindPort)
	}
	f, err := os.Open(*configName)
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
	info, err := os.Stat(cfg.UploadPath)
	if errors.Is(err, fs.ErrNotExist) {
		log.Fatalf("upload path %q does not exist", cfg.UploadPath)
	}
	if err != nil {
		log.Fatalf("failed to stat upload path %q: %v", cfg.UploadPath, err)
	}
	if !info.IsDir() {
		log.Fatalf("upload path %q is not a folder", cfg.UploadPath)
	}
	probe := filepath.Join(cfg.UploadPath, fmt.Sprintf(".write-test-%d", os.Getpid()))
	probeF, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		log.Fatalf("upload path %q is not writable", cfg.UploadPath)
	}
	defer probeF.Close()
	err = os.Remove(probe)
	if err != nil {
		log.Printf("could not delete test file in upload path %q, skipping", cfg.UploadPath)
	}
	m, err := loadMessages(cfg.Lang)
	if err != nil {
		log.Fatalf("i18n: %v", err)
	}
	messages = m
	sanitizerImage, err := buildImage(cfg.SanitizerImageBase)
	if err != nil {
		log.Fatalf("failed to build sanitizer image: %v", err)
	}
	db, err := sql.Open("sqlite", cfg.Database+"?_foreign_keys=on")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}
	log.Println("Successfully connected to SQLite database")
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("failed to initialize database: %v", err)
	}
	// TODO: SQLite-only, Postgres enforces FK unconditionally, no verification needed. Gate when porting to Postgres.
	var fkEnabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		log.Fatalf("verify foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		log.Fatalf("foreign_keys is %d, expected 1 (DSN didn't apply the pragma)", fkEnabled)
	}

	_, err = db.Exec("DELETE FROM admin_sessions WHERE expires_at < ?", time.Now().Unix())
	if err != nil {
		log.Fatalf("failed to clear stale sessions: %v", err)
	}
	app := &App{db: db, cfg: cfg, dev: *devMode, sanitizerImage: sanitizerImage}
	if *createAdmin != "" {
		err := app.makeAdmin(*createAdmin)
		if err != nil {
			log.Fatalf("Failed to create admin account: %v", err)
		}
		log.Printf("admin %q created\n", *createAdmin)
		return
	}
	http.Handle("/static/", http.FileServerFS(files))
	http.Handle("/upload/", http.StripPrefix("/upload/", http.FileServer(http.Dir(cfg.UploadPath))))

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
			if errors.Is(err, ErrEmptyPost) {
				http.Error(w, "Post body and file empty", http.StatusUnprocessableEntity)
				return
			}
			if errors.Is(err, ErrTooLong) {
				http.Error(w, "Post fields too long", http.StatusUnprocessableEntity)
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
			if errors.Is(err, ErrEmptyPost) {
				http.Error(w, "Post body and file empty", http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/thread/"+strconv.Itoa(id), http.StatusSeeOther)

	})
	http.Handle("GET /admin", app.adminAuthMiddleware(func(w http.ResponseWriter, r *http.Request, admin adminInfo) {
		posts, filesize, err := app.getAllPosts()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		render(w, "admin_panel.html", AdminPage{Config: app.cfg, Posts: posts, TotalFileBytes: filesize})
	}))
	http.Handle("POST /admin/delete", app.adminAuthMiddleware(func(w http.ResponseWriter, r *http.Request, admin adminInfo) {
		err := r.ParseForm()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		ids := r.PostForm["delete"]
		onlyImg := r.PostForm.Get("onlyimgdel") == "on"
		for _, value := range ids {
			id, err := strconv.Atoi(value)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				log.Printf("delete post invalid id: %v", err)
				return
			}
			err = app.deletePost(id, onlyImg)
			if err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				log.Printf("delete post %d: %v", id, err)
				return
			}
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	}))
	http.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) {
		render(w, "admin_login.html", AdminLoginPage{Config: app.cfg, Error: ""})
	})
	http.HandleFunc("POST /admin/login", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		username := r.PostForm.Get("username")
		password := r.PostForm.Get("password")

		token, err := app.createSession(username, password)
		if errors.Is(err, ErrInvalidCredentials) {
			log.Printf("failed login for %q from %s", username, r.RemoteAddr)
			w.WriteHeader(http.StatusUnauthorized)
			render(w, "admin_login.html", AdminLoginPage{Config: app.cfg, Error: "Invalid credentials"})
			return
		}
		if err != nil {
			log.Printf("login failed for %q: %v", username, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		log.Printf("successful login for %q from %s", username, r.RemoteAddr)
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   !app.dev, // Secure mode is disabled in developer mode
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(ttl.Seconds()),
		})
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})

	http.HandleFunc("POST /admin/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil {
			_, _ = app.db.Exec("DELETE FROM admin_sessions WHERE token = ?", c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "session",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   !app.dev, // Secure mode is disabled in developer mode
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	http.Handle("GET /admin/password", app.adminAuthMiddleware(func(w http.ResponseWriter, r *http.Request, admin adminInfo) {
		render(w, "admin_password.html", AdminPasswordPage{Config: app.cfg, Error: "", Success: false})
	}))
	http.Handle("POST /admin/password", app.adminAuthMiddleware(func(w http.ResponseWriter, r *http.Request, admin adminInfo) {
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		cookie, err := r.Cookie("session")
		if err != nil {
			log.Printf("password change failed failed for %q: %v", admin.username, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		err = r.ParseForm()
		if err != nil {
			log.Printf("password change failed failed for %q: %v", admin.username, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		newPw := r.PostForm.Get("new")
		confirmPw := r.PostForm.Get("confirm")

		if newPw != confirmPw {
			w.WriteHeader(http.StatusUnprocessableEntity)
			render(w, "admin_password.html", AdminPasswordPage{Config: app.cfg, Error: "Passwords do not match", Success: false})
			return
		}
		if len(newPw) < 8 {
			w.WriteHeader(http.StatusUnprocessableEntity)
			render(w, "admin_password.html", AdminPasswordPage{Config: app.cfg, Error: "Passwords is too short", Success: false})
			return
		}
		origPw := r.PostForm.Get("current")
		err = app.verifyLogin(admin.username, origPw)
		if errors.Is(err, ErrInvalidCredentials) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			render(w, "admin_password.html", AdminPasswordPage{Config: app.cfg, Error: "Current password incorrect", Success: false})
			return
		}
		if err != nil {
			log.Printf("login during password change failed for %q: %v", admin.username, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		err = app.updatePassword(admin.username, newPw)
		if err != nil {
			log.Printf("password change failed failed for %q: %v", admin.username, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		_, err = app.db.Exec(
			"DELETE FROM admin_sessions WHERE admin_id = ? AND token != ?",
			admin.adminID, cookie.Value,
		)
		if err != nil {
			log.Printf("session purge failed for %q: %v", admin.username, err)
		}
		render(w, "admin_password.html", AdminPasswordPage{Config: app.cfg, Error: "", Success: true})
	}))

	if app.dev {
		log.Println("Server running in developer mode! Do not use in production!!!")
	}
	log.Printf("Server is serving requests on port %d!", *bindPort)
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", *bindPort),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func (c Config) MaxFileSize() int {
	return c.MaxKB * 1024
}
