package utility

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	
	"github.com/netresearch/go-cron"
	
	"github.com/xmapst/taskflow/pkg/osext"
	"github.com/xmapst/taskflow/pkg/selfupdate"
)

// Bound both checksum and binary downloads, including response body reads.
var updateHTTPClient = &http.Client{
	Timeout: 5 * time.Minute,
}

type selfUpdate struct {
	sHash    []byte
	sURL     string
	cron     *cron.Cron
	skipFunc func() bool
}

// StartSelfUpdate checks for releases every 30 minutes and skips replacement
// while skipFunc reports that the service is busy.
func StartSelfUpdate(uri string, skipFunc func() bool) {
	s := &selfUpdate{
		sURL:     strings.TrimSuffix(uri, "/"),
		cron:     cron.New(cron.WithSeconds()),
		skipFunc: skipFunc,
	}
	s.localSha256sum()
	_, err := s.cron.AddFunc("@every 30m", func() {
		s.doUpdate()
	})
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	s.cron.Start()
}

func (p *selfUpdate) doUpdate() {
	if p.sURL == "" {
		slog.Debug("automatic updates are not turned on")
		return
	}
	
	if p.sHash == nil {
		p.localSha256sum()
	}
	
	checksum, name := p.remoteSha256sum(fmt.Sprintf("%s/latest.sha256sum", p.sURL))
	if checksum == nil || p.sHash == nil || bytes.Equal(p.sHash, checksum) {
		return
	}
	
	opts := selfupdate.Options{
		Checksum: checksum,
	}
	if err := opts.CheckPermissions(); err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	
	resp, err := updateHTTPClient.Get(fmt.Sprintf("%s/%s", p.sURL, name))
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	if resp == nil {
		slog.Warn("resp is nil")
		return
	}
	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			slog.Error(
				"operation failed",
				slog.Any("error", err),
			)
		}
	}(resp.Body)
	
	if resp.StatusCode != http.StatusOK {
		slog.Warn(
			"binary download failed",
			slog.Int("status", resp.StatusCode),
		)
		return
	}
	
	err = selfupdate.PrepareAndCheckBinary(resp.Body, opts)
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	
	if p.skipFunc() {
		return
	}
	
	err = selfupdate.CommitBinary(opts)
	if err != nil {
		slog.Error(
			"operation failed",
			slog.Any("error", err),
		)
		return
	}
	
	// Exit so the service supervisor can start the new executable.
	os.Exit(200)
}

func (p *selfUpdate) localSha256sum() {
	path, err := osext.Executable()
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	hash := sha256.New()
	file, err := os.Open(path)
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	defer func(file *os.File) {
		err = file.Close()
		if err != nil {
			slog.Error(
				"operation failed",
				slog.Any("error", err),
			)
		}
	}(file)
	_, err = io.Copy(hash, file)
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return
	}
	p.sHash = hash.Sum([]byte{})
	return
}

func (p *selfUpdate) remoteSha256sum(url string) ([]byte, string) {
	resp, err := updateHTTPClient.Get(url)
	if err != nil {
		slog.Warn(
			"operation warning",
			slog.Any("error", err),
		)
		return nil, ""
	}
	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			slog.Error(
				"operation failed",
				slog.Any("error", err),
			)
		}
	}(resp.Body)
	if resp.StatusCode != http.StatusOK {
		slog.Warn(
			"checksum download failed",
			slog.Int("status", resp.StatusCode),
		)
		return nil, ""
	}
	// The final checksum entry may not end with a newline.
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(runtime.GOOS)) || !bytes.Contains(line, []byte(runtime.GOARCH)) {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) != 2 {
			continue
		}
		checksum, err := hex.DecodeString(string(fields[0]))
		if err != nil || len(checksum) != sha256.Size {
			continue
		}
		return checksum, filepath.Base(string(fields[1]))
	}
	if err := scanner.Err(); err != nil {
		slog.Warn(
			"read checksum response failed",
			slog.Any("error", err),
		)
	}
	return nil, ""
}
