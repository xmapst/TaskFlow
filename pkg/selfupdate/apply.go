package selfupdate

import (
	"bytes"
	"crypto"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/xmapst/taskflow/pkg/osext"
)

// Apply updates the running executable, or the file at opts.TargetPath, using
// the supplied update. Use [RollbackError] to check whether a failed update
// needs manual recovery.
func Apply(update io.Reader, opts Options) error {
	err := PrepareAndCheckBinary(update, opts)
	if err != nil {
		return err
	}
	return CommitBinary(opts)
}

// PrepareAndCheckBinary reads a replacement executable or applies a binary
// patch using opts.Patcher. It verifies opts.Checksum if set, then writes the
// result to .<target>.new next to the target with the configured file mode.
func PrepareAndCheckBinary(update io.Reader, opts Options) error {
	// get target path
	targetPath, err := opts.getPath()
	if err != nil {
		return err
	}
	var newBytes []byte
	if opts.Patcher != nil {
		if newBytes, err = opts.applyPatch(update, targetPath); err != nil {
			return err
		}
	} else {
		if newBytes, err = io.ReadAll(update); err != nil {
			return err
		}
	}

	// verify checksum if requested
	if opts.Checksum != nil {
		if err = opts.verifyChecksum(newBytes); err != nil {
			return err
		}
	}

	// get the directory the executable exists in
	updateDir := filepath.Dir(targetPath)
	filename := filepath.Base(targetPath)

	// Copy the contents of newbinary to a new executable file
	newPath := filepath.Join(updateDir, fmt.Sprintf(".%s.new", filename))
	fp, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, opts.getMode())
	if err != nil {
		return err
	}
	defer fp.Close()

	_, err = io.Copy(fp, bytes.NewReader(newBytes))
	if err != nil {
		return err
	}

	// if we don't call fp.Close(), windows won't let us move the new executable
	// because the file will still be "in use"
	_ = fp.Close()
	return nil
}

// CommitBinary installs the replacement written by [PrepareAndCheckBinary]. It
// moves the existing executable to opts.OldSavePath or, if that is empty, to
// .<target>.old next to the target. If installation succeeds and no save path
// was set, it attempts to remove the old file. On Windows, it attempts to hide
// the file if removal fails.
//
// If installation fails, CommitBinary attempts to restore the old executable.
// Use [RollbackError] to check whether restoration failed.
func CommitBinary(opts Options) error {
	// get the directory the file exists in
	targetPath, err := opts.getPath()
	if err != nil {
		return err
	}

	updateDir := filepath.Dir(targetPath)
	filename := filepath.Base(targetPath)
	newPath := filepath.Join(updateDir, fmt.Sprintf(".%s.new", filename))

	// this is where we'll move the executable to so that we can swap in the updated replacement
	oldPath := opts.OldSavePath
	removeOld := opts.OldSavePath == ""
	if removeOld {
		oldPath = filepath.Join(updateDir, fmt.Sprintf(".%s.old", filename))
	}

	// delete any existing old exec file - this is necessary on Windows for two reasons:
	// 1. after a successful update, Windows can't remove the .old file because the process is still running
	// 2. windows rename operations fail if the destination file already exists
	_ = os.Remove(oldPath)

	// move the existing executable to a new file in the same directory
	err = os.Rename(targetPath, oldPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// move the new exectuable in to become the new program
	err = os.Rename(newPath, targetPath)

	if err != nil {
		// move unsuccessful
		//
		// The filesystem is now in a bad state. We have successfully
		// moved the existing binary to a new location, but we couldn't move the new
		// binary to take its place. That means there is no file where the current executable binary
		// used to be!
		// Try to rollback by restoring the old binary to its original path.
		rerr := os.Rename(oldPath, targetPath)
		if rerr != nil {
			return &rollbackErr{
				error:       err,
				rollbackErr: rerr,
			}
		}

		return err
	}

	// move successful, remove the old binary if needed
	if removeOld {
		errRemove := os.Remove(oldPath)

		// windows has trouble with removing old binaries, so hide it instead
		if errRemove != nil {
			_ = hideFile(oldPath)
		}
	}

	return nil
}

// RollbackError takes an error value returned by Apply and returns the error, if any,
// that occurred when attempting to roll back from a failed update. Applications should
// always call this function on any non-nil errors returned by Apply.
//
// If no rollback was needed or if the rollback was successful, RollbackError returns nil,
// otherwise it returns the error encountered when trying to roll back.
func RollbackError(err error) error {
	if err == nil {
		return nil
	}
	if rerr, ok := err.(*rollbackErr); ok {
		return rerr.rollbackErr
	}
	return nil
}

type rollbackErr struct {
	error             // original error
	rollbackErr error // error encountered while rolling back
}

// Options configures how an update is verified and installed.
type Options struct {
	// TargetPath is the file to update. If empty, use the running executable.
	TargetPath string

	// TargetMode is the replacement file's mode. If zero, use 0o755.
	TargetMode os.FileMode

	// Checksum is the expected checksum of the new binary. If nil, skip verification.
	Checksum []byte

	// Hash selects the checksum algorithm. If zero, use SHA-256.
	Hash crypto.Hash

	// Patcher applies a binary patch. If nil, treat the update as a complete replacement.
	Patcher Patcher

	// OldSavePath is where the previous executable is saved. If empty,
	// CommitBinary attempts to remove the previous executable after installation.
	OldSavePath string
}

// CheckPermissions tests whether the process can open a probe file for writing
// next to the update target. It does not verify that the target can be replaced.
func (o *Options) CheckPermissions() error {
	// get the directory the file exists in
	path, err := o.getPath()
	if err != nil {
		return err
	}

	fileDir := filepath.Dir(path)
	fileName := filepath.Base(path)

	// attempt to open a file in the file's directory
	newPath := filepath.Join(fileDir, fmt.Sprintf(".%s.check-perm", fileName))
	fp, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, o.getMode())
	if err != nil {
		return err
	}
	_ = fp.Close()

	_ = os.Remove(newPath)
	return nil
}

func (o *Options) getPath() (string, error) {
	if o.TargetPath == "" {
		return osext.Executable()
	} else {
		return o.TargetPath, nil
	}
}

func (o *Options) getMode() os.FileMode {
	if o.TargetMode == 0 {
		return 0755
	}
	return o.TargetMode
}

func (o *Options) getHash() crypto.Hash {
	if o.Hash == 0 {
		o.Hash = crypto.SHA256
	}
	return o.Hash
}

func (o *Options) applyPatch(patch io.Reader, targetPath string) ([]byte, error) {
	// open the file to patch
	old, err := os.Open(targetPath)
	if err != nil {
		return nil, err
	}
	defer old.Close()

	// apply the patch
	var applied bytes.Buffer
	if err = o.Patcher.Patch(old, &applied, patch); err != nil {
		return nil, err
	}
	return applied.Bytes(), nil
}

func (o *Options) verifyChecksum(updated []byte) error {
	checksum, err := checksumFor(o.getHash(), updated)
	if err != nil {
		return err
	}

	if !bytes.Equal(o.Checksum, checksum) {
		return fmt.Errorf("updated file has wrong checksum. Expected: %x, got: %x", o.Checksum, checksum)
	}
	return nil
}

func checksumFor(h crypto.Hash, payload []byte) ([]byte, error) {
	if !h.Available() {
		return nil, errors.New("requested hash function not available")
	}
	hash := h.New()
	hash.Write(payload) // guaranteed not to error
	return hash.Sum([]byte{}), nil
}
