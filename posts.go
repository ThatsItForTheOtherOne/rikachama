package main

import (
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

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

const postsPerPage = 10

const postColumns = `id, posted_at, author, email, subject, body,
    file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type, width, height`

const maxNameLen = 50
const maxEmailLen = 100
const maxSubjectLen = 100
const maxBodyLen = 4000

type postScanner interface {
	Scan(dest ...any) error
}

type unknownTypeError struct {
	MimeType string
}

var ErrEmptyPost = errors.New("post must contain a body or file")
var ErrTooLong = errors.New("post field too long")

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

func (a *App) getThreads(page int) ([]Post, int, error) {
	var total int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE reply_to IS NULL`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := a.db.Query(`
	SELECT `+postColumns+`
	FROM posts
	WHERE reply_to IS NULL
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
	var reply_to any = threadID
	if threadID == 0 {
		reply_to = nil
	}
	err := r.ParseMultipartForm(int64(a.cfg.MaxFileSize()))
	if err != nil {
		return err
	}
	author := r.FormValue("name")
	if author == "" {
		author = T("post.anonymous")
	}
	if utf8.RuneCountInString(author) > maxNameLen {
		return ErrTooLong
	}
	email := r.FormValue("email")
	if utf8.RuneCountInString(email) > maxEmailLen {
		return ErrTooLong
	}
	subject := r.FormValue("sub")
	if utf8.RuneCountInString(subject) > maxSubjectLen {
		return ErrTooLong
	}
	body := r.FormValue("com")
	if utf8.RuneCountInString(body) > maxBodyLen {
		return ErrTooLong
	}
	var filePath, thumbnailPath, mimeType string
	var height, width, thumbnailWidth, thumbnailHeight, fileSize int64
	file, _, err := r.FormFile("upfile")
	if err != nil && len(body) == 0 {
		return ErrEmptyPost
	}
	if err == nil {
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		mimeType = detectMime(buf[:n])
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, ok := mimeSpecs[mimeType]; !ok {
			return &unknownTypeError{MimeType: mimeType}
		}
		f, err := a.saveFile(file, mimeType)
		if err != nil {
			return err
		}
		filePath = f.Path
		thumbnailPath = f.ThumbPath
		height = int64(f.Height)
		width = int64(f.Width)
		thumbnailHeight = int64(f.ThumbHeight)
		thumbnailWidth = int64(f.ThumbWidth)
		fileSize = f.FileSize
	}
	now := time.Now().Unix()
	_, err = a.db.Exec(`
	INSERT INTO posts
    	(reply_to, posted_at, bumped_at, author, email, subject, body,
    	 file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type,
		 width, height)
	VALUES
    	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		reply_to, now, now,
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

func (p Post) Kind() string {
	switch {
	case strings.HasPrefix(p.MimeType, "video/"):
		return "video"
	case p.MimeType == "application/x-shockwave-flash":
		return "flash"
	case strings.HasPrefix(p.MimeType, "image/"):
		return "image"
	default:
		return "document"
	}
}
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

func (a *App) getAllPosts() ([]Post, int64, error) {
	var total int64
	var posts []Post
	err := a.db.QueryRow(`SELECT COALESCE(SUM(file_size), 0) FROM posts`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := a.db.Query(`
	SELECT ` + postColumns + `
	FROM posts
	ORDER BY id DESC`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return posts, total, nil
}

func (a *App) deletePost(id int, imgOnly bool) error {
	var n int
	err := a.db.QueryRow("SELECT reply_to IS NULL FROM posts WHERE id = ?", id).Scan(&n)
	if err != nil {
		return err
	}
	isThread := n != 0
	switch {
	case imgOnly:
		return a.deleteFile(id)
	case isThread:
		return a.deleteThread(id)
	default:
		return a.deleteSinglePost(id)
	}
}

func (a *App) deleteThread(threadID int) error {
	rows, err := a.db.Query(`SELECT file_path, thumbnail_path FROM posts WHERE id = ? OR reply_to = ?`, threadID, threadID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var thumbPath string
		var filePath string
		err := rows.Scan(&filePath, &thumbPath)
		if err != nil {
			return err
		}
		unlinkPostFiles(filePath, thumbPath)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = a.db.Exec(`DELETE FROM posts WHERE id = ? OR reply_to = ?`, threadID, threadID)
	return err
}

func (a *App) deleteSinglePost(postID int) error {
	var filePath, thumbPath string
	err := a.db.QueryRow(`SELECT file_path, thumbnail_path FROM posts WHERE id = ?`, postID).Scan(&filePath, &thumbPath)
	if err != nil {
		return err
	}
	unlinkPostFiles(filePath, thumbPath)
	_, err = a.db.Exec(`DELETE FROM posts WHERE id = ?`, postID)
	return err
}

func (a *App) deleteFile(postID int) error {
	var filePath, thumbPath string
	err := a.db.QueryRow(`SELECT file_path, thumbnail_path FROM posts WHERE id = ?`, postID).Scan(&filePath, &thumbPath)
	if err != nil {
		return err
	}
	unlinkPostFiles(filePath, thumbPath)
	_, err = a.db.Exec(`UPDATE posts SET file_path = '', thumbnail_path = '/static/deleted_file.png' WHERE id = ?`, postID)
	return err
}

func unlinkPostFiles(filePath, thumbPath string) {
	if filePath != "" {
		_ = os.Remove("upload/" + filePath)
	}
	if strings.HasPrefix(thumbPath, "/upload/") {
		_ = os.Remove(strings.TrimPrefix(thumbPath, "/"))
	}
}
