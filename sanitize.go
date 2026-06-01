package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

var mimeSpecs = map[string]mimeSpec{
	"image/jpeg": {
		sanitizeCommand:  "image",
		thumbnailCommand: "image-thumb",
		outputExt:        ".jpg",
		thumbnailExt:     ".jpg",
		displayName:      "JPEG",
	},
	"image/png": {
		sanitizeCommand:  "image-png",
		thumbnailCommand: "image-png-thumb",
		outputExt:        ".png",
		thumbnailExt:     ".png",
		displayName:      "PNG",
	},
	"image/gif": {
		sanitizeCommand:  "image-gif",
		thumbnailCommand: "image-thumb",
		outputExt:        ".gif",
		thumbnailExt:     ".jpg",
		displayName:      "GIF",
	},
	"image/webp": {
		sanitizeCommand:  "image-png",
		thumbnailCommand: "image-png-thumb",
		outputExt:        ".png",
		thumbnailExt:     ".png",
		displayName:      "WebP",
	},
	"video/mp4": {
		sanitizeCommand:  "video",
		thumbnailCommand: "video-thumb",
		outputExt:        ".mp4",
		thumbnailExt:     ".jpg",
		displayName:      "MP4",
	},
	"video/webm": {
		sanitizeCommand:  "video",
		thumbnailCommand: "video-thumb",
		outputExt:        ".mp4",
		thumbnailExt:     ".jpg",
		displayName:      "WebM",
	},
	"application/pdf": {
		sanitizeCommand:  "pdf",
		thumbnailCommand: "pdf-thumb",
		outputExt:        ".pdf",
		thumbnailExt:     ".jpg",
		displayName:      "PDF",
	},
	"application/x-shockwave-flash": {
		outputExt:        ".swf",
		displayName:      "SWF",
		defaultThumbnail: "static/swf_thumb.png",
	},
}

type mimeSpec struct {
	sanitizeCommand, thumbnailCommand string
	outputExt                         string
	thumbnailExt                      string
	displayName                       string
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

func acceptedFileTypes() string {
	var acceptedTypes []string
	for _, v := range mimeSpecs {
		var extension = v.displayName
		acceptedTypes = append(acceptedTypes, extension)
	}
	sort.Strings(acceptedTypes)
	return strings.Join(acceptedTypes, ", ")
}

func buildImage(sanitizerImageBase string) (string, error) {
	var needToBuild bool
	sanitizerFS, err := fs.Sub(containerFiles, "sanitizer")
	if err != nil {
		return "", err
	}
	containerFile, err := sanitizerFS.Open("Containerfile")
	if err != nil {
		return "", err
	}
	defer containerFile.Close()
	entrypointFile, err := sanitizerFS.Open("entrypoint.sh")
	if err != nil {
		return "", err
	}
	defer entrypointFile.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, containerFile)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(hash, entrypointFile)
	if err != nil {
		return "", err
	}
	digest := hash.Sum(nil)
	dir, err := os.MkdirTemp("", "sanitizer-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	if err := os.CopyFS(dir, sanitizerFS); err != nil {
		return "", err
	}
	sanitizerImage := sanitizerImageBase + ":" + fmt.Sprintf("%x", digest)[:12]
	cmd := exec.Command("podman", "image", "exists", sanitizerImage)
	err = cmd.Run()
	if err != nil {
		needToBuild = true
	}
	if !needToBuild {
		created, err := imageCreatedAt(sanitizerImage)
		if err == nil && time.Since(created) > 7*24*time.Hour {
			log.Printf("sanitizer image is %v old, forcing rebuild", time.Since(created))
			needToBuild = true
		}
	}
	if needToBuild {
		log.Printf("Starting container build, name: %s", sanitizerImage)
		cmd = exec.Command("podman", "build", "-t", sanitizerImage, dir)
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			return "", err
		}
		log.Printf("Container build finished!")
	} else {
		log.Printf("Using cached container image!")
	}
	return sanitizerImage, nil
}

func (a *App) podmanRun(args ...string) *exec.Cmd {
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
		a.sanitizerImage,
	}
	return exec.Command("podman", append(base, args...)...)
}

func (a *App) process(r io.Reader, fileCmd, thumbCmd string) ([]byte, []byte, error) {
	input, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	var fileOut, thumbOut bytes.Buffer
	g := new(errgroup.Group)

	g.Go(func() error {
		fc := a.podmanRun(fileCmd)
		fc.Stdin, fc.Stdout, fc.Stderr = bytes.NewReader(input), &fileOut, os.Stderr
		return fc.Run()
	})

	g.Go(func() error {
		tc := a.podmanRun(thumbCmd)
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
		fileData, thumbData, err = a.process(file, ft.sanitizeCommand, ft.thumbnailCommand)
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
	filePath := base + ft.outputExt
	if err := os.WriteFile(filepath.Join(a.cfg.UploadPath, filePath), fileData, 0644); err != nil {
		return savedFile{}, fmt.Errorf("write file: %w", err)
	}

	var thumbPath string
	if thumbData != nil {
		name := base + "_thumb" + ft.thumbnailExt
		if err := os.WriteFile(filepath.Join(a.cfg.UploadPath, name), thumbData, 0644); err != nil {
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

func imageCreatedAt(name string) (time.Time, error) {
	out, err := exec.Command("podman", "image", "inspect",
		"--format", "{{.Created.UnixMilli}}", name).Output()
	if err != nil {
		return time.Time{}, err
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse podman timestamp: %w", err)
	}
	return time.UnixMilli(ms), nil
}
