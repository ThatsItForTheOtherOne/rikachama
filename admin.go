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

func (a *App) authedAdmin(r *http.Request) (int, error) {
	c, err := r.Cookie("session")
	if err != nil {
		return 0, err
	}
	var adminID int
	err = a.db.QueryRow(
		`SELECT admin_id FROM admin_sessions WHERE token = ? AND expires_at > ?`,
		c.Value, time.Now().Unix(),
	).Scan(&adminID)
	return adminID, err
}

func (a *App) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := a.authedAdmin(r)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, http.ErrNoCookie) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			log.Println(err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
