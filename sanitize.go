package main

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

var mimeSpecs = map[string]mimeSpec{
	"image/jpeg":                    {"image", "image-thumb", ".jpg", ""},
	"image/png":                     {"image", "image-thumb", ".png", ""},
	"image/gif":                     {"image-gif", "image-thumb", ".gif", ""},
	"image/webp":                    {"image", "image-thumb", ".webp", ""},
	"video/mp4":                     {"video", "video-thumb", ".mp4", ""},
	"video/webm":                    {"video", "video-thumb", ".webm", ""},
	"application/pdf":               {"pdf", "pdf-thumb", ".pdf", ""},
	"application/x-shockwave-flash": {"", "", ".swf", "static/swf_thumb.png"},
}

type mimeSpec struct {
	sanitizeCommand, thumbnailCommand string
	ext                               string
	defaultThumbnail                  string
}

type savedFile struct {
	Path        string
	ThumbPath   string
	Width       int
	Height      int
	ThumbWidth  int
	ThumbHeight int
	FileSize    int64
}

//go:embed sanitizer/*
var containerFiles embed.FS

func buildImage(sanitizerImage string) error {
	dir, err := os.MkdirTemp("", "sanitizer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.CopyFS(dir, containerFiles); err != nil {
		return err
	}
	cmd := exec.Command("podman", "build", "-t", sanitizerImage, dir+"/sanitizer/")
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

func (a *App) saveFile(file multipart.File, mimeType string) (savedFile, error) {
	ft, ok := mimeSpecs[mimeType]
	if !ok {
		return savedFile{}, fmt.Errorf("unsupported file type: %s", mimeType)
	}

	var fileData, thumbData []byte
	if ft.sanitizeCommand != "" {
		var err error
		fileData, thumbData, err = a.cfg.process(file, ft.sanitizeCommand, ft.thumbnailCommand)
		if err != nil {
			return savedFile{}, fmt.Errorf("process %s: %w", mimeType, err)
		}
	} else {
		data, err := io.ReadAll(file)
		if err != nil {
			return savedFile{}, fmt.Errorf("read upload: %w", err)
		}
		if mimeType == "application/x-shockwave-flash" && !isSWF(data) {
			return savedFile{}, fmt.Errorf("invalid SWF file")
		}
		fileData = data
	}

	base := fmt.Sprintf("%d", time.Now().UnixNano())
	filePath := base + ft.ext
	if err := os.WriteFile("upload/"+filePath, fileData, 0644); err != nil {
		return savedFile{}, fmt.Errorf("write file: %w", err)
	}

	var thumbPath string
	if thumbData != nil {
		name := base + "_thumb.jpg"
		if err := os.WriteFile("upload/"+name, thumbData, 0644); err != nil {
			return savedFile{}, fmt.Errorf("write thumb: %w", err)
		}
		thumbPath = "/upload/" + name
	} else if ft.defaultThumbnail != "" {
		thumbPath = "/" + ft.defaultThumbnail
	}
	var width, height int
	if strings.HasPrefix(mimeType, "image/") {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(fileData))
		if err != nil {
			return savedFile{}, err
		}
		width, height = cfg.Width, cfg.Height
	}
	var thumbW, thumbH int
	if thumbData != nil {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(thumbData))
		if err != nil {
			return savedFile{}, err
		}
		thumbW, thumbH = cfg.Width, cfg.Height
	} else if thumbPath != "" {
		b, err := files.ReadFile(ft.defaultThumbnail)
		if err != nil {
			return savedFile{}, err
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			return savedFile{}, err
		}
		thumbW, thumbH = cfg.Width, cfg.Height
	}
	return savedFile{Path: filePath, ThumbPath: thumbPath, Width: width, Height: height, ThumbWidth: thumbW, ThumbHeight: thumbH, FileSize: int64(len(fileData))}, nil
}
