package httpx

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// UploadName preserves directory uploads without accepting paths outside the root.
func UploadName(file *multipart.FileHeader) (string, error) {
	_, params, err := mime.ParseMediaType(file.Header.Get("Content-Disposition"))
	if err != nil {
		return "", err
	}
	name := params["filename"]
	// Treat backslashes as separators on every platform, not just Windows.
	name = strings.ReplaceAll(name, "\\", "/")
	if name == "" || strings.Contains(name, ":") || !filepath.IsLocal(name) || filepath.Clean(name) == "." {
		return "", fmt.Errorf("invalid upload filename %q", name)
	}
	if slices.Contains(strings.Split(name, "/"), "..") {
		return "", fmt.Errorf("invalid upload filename %q", name)
	}
	return filepath.Clean(name), nil
}

// SaveToTarFile writes the multipart files to an uncompressed tar archive.
func SaveToTarFile(r *http.Request, path string, maxMemory int64) (err error) {
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		return err
	}
	form := r.MultipartForm
	defer form.RemoveAll()
	files := form.File["files"]
	if len(files) == 0 {
		return fmt.Errorf("files is null")
	}
	fw, err := os.Create(path)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(fw)
	defer func() { err = errors.Join(err, tw.Close(), fw.Close()) }()
	for _, file := range files {
		name, err := UploadName(file)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(name),
			Mode: 0644,
			Size: file.Size,
		}); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, src)
		if err := errors.Join(copyErr, src.Close()); err != nil {
			return err
		}
	}
	return nil
}

// SaveFiles creates the destination directory and saves the multipart files in it.
func SaveFiles(r *http.Request, path string, maxMemory int64, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	return SaveFilesRoot(r, root, ".", maxMemory, perm)
}

// SaveFilesRoot confines all filesystem operations, including symlink resolution,
// to root. The caller owns root.
func SaveFilesRoot(r *http.Request, root *os.Root, path string, maxMemory int64, perm os.FileMode) error {
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		return err
	}
	form := r.MultipartForm
	defer form.RemoveAll()
	files := form.File["files"]
	if len(files) == 0 {
		return fmt.Errorf("files is null")
	}
	for _, file := range files {
		name, err := UploadName(file)
		if err != nil {
			return err
		}
		name = filepath.Join(path, name)
		if err = root.MkdirAll(filepath.Dir(name), perm); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dst, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return errors.Join(err, src.Close())
		}
		_, copyErr := io.Copy(dst, src)
		if err = errors.Join(copyErr, dst.Close(), src.Close()); err != nil {
			return err
		}
	}
	return nil
}
