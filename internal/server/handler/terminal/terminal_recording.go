package terminal

import (
	"compress/gzip"
	"errors"
	"os"
	"path"
	"path/filepath"
	"time"
	"uuid"

	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// replayFile owns the local storage and compression for one WebShell cast.
// finish is called after xterm.Run has stopped writing.
type replayFile struct {
	file  *os.File
	zip   *gzip.Writer
	path  string
	date  string
	id    string
	wrote bool
}

func (h *Handler) newReplayFile() (*replayFile, error) {
	date := time.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(h.ReplayDir, date)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	id := uuid.NewV7().String()
	path := filepath.Join(dir, id+castSuffix)
	file, err := os.OpenFile(path+".partial", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	return &replayFile{file: file, zip: gzip.NewWriter(file), path: path, date: date, id: id}, nil
}

func (r *replayFile) Write(p []byte) (int, error) {
	n, err := r.zip.Write(p)
	r.wrote = r.wrote || n > 0
	return n, err
}

func (r *replayFile) finish(publish bool) error {
	err := errors.Join(r.zip.Close(), r.file.Close())
	if publish && r.wrote && err == nil {
		err = os.Rename(r.file.Name(), r.path)
		if err == nil {
			info, statErr := os.Stat(r.path)
			if statErr != nil {
				return statErr
			}
			if dbErr := storage.ReplayCreate(&models.SReplay{
				Date: r.date, ReplayID: r.id, FilePath: path.Join(r.date, r.id+castSuffix), Size: info.Size(),
			}); dbErr != nil {
				return dbErr
			}
			return nil
		}
	}
	return errors.Join(err, os.Remove(r.file.Name()))
}
