package main

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/alexedwards/argon2id"
)

var params = &argon2id.Params{
	Memory:      47 * 1024,
	Iterations:  1,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}
var dummyHash = createDummyHash()
var ttl = time.Hour * 1
var ErrInvalidCredentials = errors.New("invalid credentials")

type adminInfo struct {
	adminID  int
	username string
}

type adminHandler func(w http.ResponseWriter, r *http.Request, admin adminInfo)

func (a *App) makeAdmin(username string) error {
	fmt.Print("Password: ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return errors.New("password cannot be less than 8 characters")
	}
	hash, err := argon2id.CreateHash(string(pw), params)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`INSERT INTO admins(username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, hash, time.Now().Unix())
	return err
}

func createDummyHash() string {
	hash, err := argon2id.CreateHash("Nipah~!", params)
	if err != nil {
		log.Fatalf("Cannot generate dummy hash: %v", err)
	}
	return hash
}

func (a *App) verifyLogin(username string, password string) error {
	var pwHash string
	err := a.db.QueryRow("SELECT password_hash FROM admins WHERE username = ?", username).Scan(&pwHash)
	if errors.Is(err, sql.ErrNoRows) {
		_, _ = argon2id.ComparePasswordAndHash(password, dummyHash)
		return ErrInvalidCredentials
	}
	if err != nil {
		return err
	}
	match, err := argon2id.ComparePasswordAndHash(password, pwHash)
	if err != nil {
		return err
	}
	if !match {
		return ErrInvalidCredentials
	}
	return nil
}

func (a *App) createSession(username string, password string) (string, error) {
	err := a.verifyLogin(username, password)
	if err != nil {
		return "", err
	}
	var adminID int
	err = a.db.QueryRow("SELECT id FROM admins WHERE username = ?", username).Scan(&adminID)
	if err != nil {
		return "", err
	}
	token := rand.Text()
	_, err = a.db.Exec("INSERT INTO admin_sessions(token, admin_id, expires_at) VALUES (?, ?, ?)", token, adminID, time.Now().Add(ttl).Unix())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (a *App) authedAdmin(r *http.Request) (adminInfo, error) {
	c, err := r.Cookie("session")
	if err != nil {
		return adminInfo{}, err
	}
	var adminID int
	var username string
	err = a.db.QueryRow(`
	    SELECT a.id, a.username
	    FROM admin_sessions s
	    JOIN admins a ON s.admin_id = a.id
	    WHERE s.token = ? AND s.expires_at > ?
	`, c.Value, time.Now().Unix()).Scan(&adminID, &username)
	return adminInfo{adminID: adminID, username: username}, err
}

func (a *App) adminAuthMiddleware(h adminHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, err := a.authedAdmin(r)
		if errors.Is(err, sql.ErrNoRows) {
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   !a.dev,
				SameSite: http.SameSiteStrictMode,
				MaxAge:   -1,
			})
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if errors.Is(err, http.ErrNoCookie) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			log.Println(err)
			return
		}
		h(w, r, info)
	})
}

func (a *App) updatePassword(username string, password string) error {
	hash, err := argon2id.CreateHash(password, params)
	if err != nil {
		return err
	}
	res, err := a.db.Exec("UPDATE admins SET password_hash = ? WHERE username = ?", hash, username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("database unchanged")
	}
	return err
}
