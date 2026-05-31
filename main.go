package main

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

type Config struct {
	Title          string        `json:"title"`
	Home           string        `json:"home"`
	AddInfo        template.HTML `json:"add_info"`
	MaxKB          int           `json:"max_kb"`
	MaxW           int           `json:"max_w"`
	MaxH           int           `json:"max_h"`
	SanitizerImage string        `json:"sanitizer_image"`
	Lang           string        `json:"lang"`
}

type App struct {
	db  *sql.DB
	cfg Config
	dev bool
}

//go:embed schema.sql
var schema string

func main() {
	createAdmin := flag.String("create-admin", "", "Create an admin with the given username, then exit")
	devMode := flag.Bool("dev", false, "Enable developer mode")
	flag.Parse()
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
	m, err := loadMessages(cfg.Lang)
	if err != nil {
		log.Fatalf("i18n: %v", err)
	}
	messages = m
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
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	_, err = db.Exec("DELETE FROM admin_sessions WHERE expires_at < ?", time.Now().Unix())
	if err != nil {
		log.Fatalf("failed to set foreign key pragma: %v", err)
	}
	app := &App{db: db, cfg: cfg, dev: *devMode}
	if *createAdmin != "" {
		err := app.makeAdmin(*createAdmin)
		if err != nil {
			log.Fatalf("Failed to create admin account: %v", err)
		}
		fmt.Printf("admin %q created\n", *createAdmin)
		return
	}
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
			if errors.Is(err, ErrEmptyPost) {
				http.Error(w, "Post body and file empty", http.StatusUnprocessableEntity)
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
	http.Handle("GET /admin", app.adminAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts, filesize, err := app.getAllPosts()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		render(w, "admin_panel.html", AdminPage{Config: app.cfg, Posts: posts, TotalFileBytes: filesize})
	})))
	http.Handle("POST /admin/delete", app.adminAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	})))
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

	http.HandleFunc("GET /admin/logout", func(w http.ResponseWriter, r *http.Request) {
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
	if app.dev {
		log.Println("Server running in developer mode! Do not use in production!!!")
	}
	log.Println("Server is serving requests!")
	log.Fatal(http.ListenAndServe(":3200", nil))
}

func (c Config) MaxFileSize() int {
	return c.MaxKB * 1024
}
