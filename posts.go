package main

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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
	ReplayPath                                     string
	Width, Height, ThumbnailWidth, ThumbnailHeight int64
	FileSize                                       int64
	MimeType                                       string
	Replies                                        []Post
}

type tgkrHeader struct {
	Magic         [3]byte
	Algorithm     [1]byte
	Length        uint32
	TegakiVersion [3]byte
	FormatVersion [1]byte
}

type form struct {
	Author    string
	Email     string
	Subject   string
	Body      string
	HasFile   bool
	File      []byte
	MimeType  string
	HasReplay bool
	Replay    []byte
	Sage      bool
}

const postsPerPage = 10

const postColumns = `id, posted_at, author, email, subject, body,
    file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type, width, height, replay_path`

const maxNameLen = 50
const maxEmailLen = 100
const maxSubjectLen = 100
const maxBodyLen = 4000

const tgkrMagic = "TGK"
const tgkrHeaderLen = 12
const maxDecompressedLen = 8 << 20

type postScanner interface {
	Scan(dest ...any) error
}

type unknownTypeError struct {
	MimeType string
}

var ErrEmptyPost = errors.New("post must contain a body or file")
var ErrTooLong = errors.New("post field too long")
var ErrOekakiDisabled = errors.New("oekaki is disabled for this board")
var ErrAttachmentTooLarge = errors.New("attachment exceeds size limit")
var ErrReplayTooLarge = errors.New("replay file exceeds size limit")
var ErrInvalidReplay = errors.New("replay file is invalid")

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
		&p.Width, &p.Height, &p.ReplayPath,
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

func (a *App) parseForm(r *http.Request) (form, error) {
	err := r.ParseMultipartForm(int64(a.cfg.MaxBodySize()))
	if err != nil {
		return form{}, err
	}
	_, hasFile := r.MultipartForm.File["upfile"]
	_, hasReplay := r.MultipartForm.File["replay_file"]
	if !a.cfg.OekakiEnabled && hasReplay {
		return form{}, ErrOekakiDisabled
	}
	author, email, subject, body, err := parseAndValidateFields(r)
	if err != nil {
		return form{}, err
	}
	if !hasFile && len(body) == 0 {
		return form{}, ErrEmptyPost
	}
	var fileBytes []byte
	var mimeType string
	if hasFile {
		f, fh, err := r.FormFile("upfile")
		if err != nil {
			return form{}, err
		}
		if fh.Size > int64(a.cfg.MaxAttachmentSize()) {
			return form{}, ErrAttachmentTooLarge
		}
		fileBytes, mimeType, err = readAndDetectMime(f)
		if err != nil {
			return form{}, err
		}
	}
	var replayFileBytes []byte
	if hasReplay {
		rf, rfh, err := r.FormFile("replay_file")
		if err != nil {
			return form{}, err
		}
		if rfh.Size > int64(a.cfg.MaxAttachmentSize()) {
			return form{}, ErrAttachmentTooLarge
		}
		replayFileBytes, err = io.ReadAll(rf)
		if err != nil {
			return form{}, err
		}
		if err := validateReplay(replayFileBytes); err != nil {
			return form{}, err
		}
	}
	sage := strings.EqualFold(email, "sage")
	return form{
		Author:    author,
		Email:     email,
		Subject:   subject,
		Body:      body,
		HasFile:   hasFile,
		File:      fileBytes,
		MimeType:  mimeType,
		HasReplay: hasReplay,
		Replay:    replayFileBytes,
		Sage:      sage,
	}, nil
}

func parseAndValidateFields(r *http.Request) (author, email, subject, body string, err error) {
	author = r.FormValue("name")
	if author == "" {
		author = T("post.anonymous")
	}
	if utf8.RuneCountInString(author) > maxNameLen {
		return "", "", "", "", ErrTooLong
	}
	email = r.FormValue("email")
	if utf8.RuneCountInString(email) > maxEmailLen {
		return "", "", "", "", ErrTooLong
	}
	subject = r.FormValue("sub")
	if utf8.RuneCountInString(subject) > maxSubjectLen {
		return "", "", "", "", ErrTooLong
	}
	body = r.FormValue("com")
	if utf8.RuneCountInString(body) > maxBodyLen {
		return "", "", "", "", ErrTooLong
	}
	return author, email, subject, body, nil
}

func validateReplay(data []byte) error {
	if len(data) < tgkrHeaderLen {
		return fmt.Errorf("%w: file too short", ErrInvalidReplay)
	}
	var hdr tgkrHeader
	if err := binary.Read(bytes.NewReader(data[:tgkrHeaderLen]), binary.BigEndian, &hdr); err != nil {
		return err
	}
	if string(hdr.Magic[:]) != tgkrMagic {
		return fmt.Errorf("%w: bad magic %x", ErrInvalidReplay, hdr.Magic)
	}
	if hdr.Length > maxDecompressedLen {
		return fmt.Errorf("%w: declared size %d exceeds cap %d", ErrInvalidReplay, hdr.Length, maxDecompressedLen)
	}
	zr := flate.NewReader(bytes.NewReader(data[tgkrHeaderLen:]))
	defer zr.Close()
	n, err := io.Copy(io.Discard, &io.LimitedReader{R: zr, N: int64(maxDecompressedLen) + 1})
	if err != nil {
		return err
	}
	if n > maxDecompressedLen {
		return fmt.Errorf("%w: decompressed payload %d exceeds cap", ErrInvalidReplay, n)
	}
	if n > int64(hdr.Length) {
		return fmt.Errorf("%w: decompressed payload %d exceeds declared size %d", ErrInvalidReplay, n, hdr.Length)
	}
	return nil
}

func readAndDetectMime(file multipart.File) ([]byte, string, error) {
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	mimeType := detectMime(buf[:n])
	if _, ok := mimeSpecs[mimeType]; !ok {
		return nil, "", &unknownTypeError{MimeType: mimeType}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}
	file.Close()
	return data, mimeType, nil
}

func (a *App) handlePost(r *http.Request, threadID int) error {
	base := fmt.Sprintf("%d", time.Now().UnixNano())
	var reply_to any = threadID
	if threadID == 0 {
		reply_to = nil
	}
	form, err := a.parseForm(r)
	if err != nil {
		return err
	}
	var file savedFile
	if form.HasFile {
		file, err = a.saveFile(form.File, form.MimeType, base)
		if err != nil {
			return err
		}
	}
	var replayPath string
	if form.HasReplay {
		replayPath, err = a.saveReplay(form.Replay, base)
		if err != nil {
			return err
		}
	}
	postedAt := time.Now().Unix()
	_, err = a.db.Exec(`
	INSERT INTO posts
    	(reply_to, posted_at, bumped_at, author, email, subject, body,
    	 file_path, thumbnail_path, thumbnail_width, thumbnail_height, file_size, mime_type,
		 width, height, replay_path)
	VALUES
    	(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		reply_to, postedAt, postedAt,
		form.Author, form.Email, form.Subject, form.Body,
		file.Path, file.ThumbPath,
		file.ThumbWidth, file.ThumbHeight, file.FileSize, form.MimeType,
		file.Width, file.Height, replayPath,
	)
	if err != nil {
		return err
	}
	if threadID != 0 && !form.Sage {
		_, err = a.db.Exec(`UPDATE posts SET bumped_at = ? WHERE id = ?`, postedAt, threadID)
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
	rows, err := a.db.Query(`SELECT file_path, thumbnail_path, replay_path FROM posts WHERE id = ? OR reply_to = ?`, threadID, threadID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var thumbPath string
		var filePath string
		var replayPath string
		err := rows.Scan(&filePath, &thumbPath, &replayPath)
		if err != nil {
			return err
		}
		a.unlinkPostFiles(filePath, thumbPath, replayPath)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = a.db.Exec(`DELETE FROM posts WHERE id = ? OR reply_to = ?`, threadID, threadID)
	return err
}

func (a *App) deleteSinglePost(postID int) error {
	var filePath, thumbPath, replayPath string
	err := a.db.QueryRow(`SELECT file_path, thumbnail_path, replay_path FROM posts WHERE id = ?`, postID).Scan(&filePath, &thumbPath, &replayPath)
	if err != nil {
		return err
	}
	a.unlinkPostFiles(filePath, thumbPath, replayPath)
	_, err = a.db.Exec(`DELETE FROM posts WHERE id = ?`, postID)
	return err
}

func (a *App) deleteFile(postID int) error {
	var filePath, thumbPath, replayPath string
	err := a.db.QueryRow(`SELECT file_path, thumbnail_path, replay_path FROM posts WHERE id = ?`, postID).Scan(&filePath, &thumbPath, &replayPath)
	if err != nil {
		return err
	}
	a.unlinkPostFiles(filePath, thumbPath, replayPath)
	_, err = a.db.Exec(`UPDATE posts SET file_path = '', thumbnail_path = '/static/deleted_file.png' WHERE id = ?`, postID)
	return err
}

func (a *App) unlinkPostFiles(filePath, thumbPath, replayPath string) {
	if filePath != "" {
		_ = os.Remove(filepath.Join(a.cfg.UploadPath, filePath))
	}
	if replayPath != "" {
		_ = os.Remove(filepath.Join(a.cfg.UploadPath, replayPath))
	}
	if strings.HasPrefix(thumbPath, "/upload/") {
		_ = os.Remove(strings.TrimPrefix(thumbPath, "/"))
	}
}

func (a *App) saveReplay(replay []byte, base string) (string, error) {
	replayPath := base + ".tgkr"
	return replayPath, os.WriteFile(filepath.Join(a.cfg.UploadPath, replayPath), replay, 0644)
}
