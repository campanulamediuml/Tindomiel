package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"tindomiel/internal/session"

	"github.com/pkg/sftp"
)

type directoryDownloadFile struct {
	remotePath string
	localPath  string
	size       int64
	mode       os.FileMode
}

func (s *Server) sftpDownloadDirectory(w http.ResponseWriter, r *http.Request) {
	found := s.listSession(w, r)
	if found == nil {
		return
	}
	var request struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	client, ok := s.sftpClient(w, r)
	if !ok {
		return
	}

	remotePath, err := client.RealPath(request.Path)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_REALPATH_FAILED", err.Error())
		return
	}
	info, err := client.Lstat(remotePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_STAT_FAILED", err.Error())
		return
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		writeError(w, http.StatusBadRequest, "SFTP_NOT_DIRECTORY", "remote path is not a directory")
		return
	}
	name := path.Base(remotePath)
	if name == "." || name == "/" || name == "" {
		writeError(w, http.StatusBadRequest, "SFTP_INVALID_DIRECTORY", "directory name is invalid")
		return
	}
	downloadRoot, err := s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	destination, err := availableDirectoryPath(downloadRoot, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}

	files := []directoryDownloadFile{}
	var total int64
	if err := collectDirectoryFiles(client, remotePath, destination, &files, &total); err != nil {
		writeError(w, http.StatusBadGateway, "SFTP_DIRECTORY_SCAN_FAILED", err.Error())
		return
	}
	progress := found.NewTransfer("download", remotePath, total)
	for _, file := range files {
		progress.SetCurrentPath(file.remotePath)
		if err := copyDirectoryFile(r.Context(), client, file, progress); err != nil {
			progress.Finish(err)
			writeError(w, http.StatusBadGateway, "SFTP_DIRECTORY_DOWNLOAD_FAILED", err.Error())
			return
		}
	}
	progress.Finish(nil)
	writeJSON(w, http.StatusOK, map[string]any{"path": destination, "files": len(files), "bytes": total})
}

func collectDirectoryFiles(client *sftp.Client, remoteDirectory, localDirectory string, files *[]directoryDownloadFile, total *int64) error {
	if err := os.MkdirAll(localDirectory, 0o755); err != nil {
		return err
	}
	entries, err := client.ReadDir(remoteDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !validDirectoryEntryName(entry.Name()) {
			return fmt.Errorf("unsafe remote entry name: %q", entry.Name())
		}
		remotePath := path.Join(remoteDirectory, entry.Name())
		localPath := filepath.Join(localDirectory, entry.Name())
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() {
			if err := collectDirectoryFiles(client, remotePath, localPath, files, total); err != nil {
				return err
			}
			continue
		}
		if !mode.IsRegular() {
			continue
		}
		*files = append(*files, directoryDownloadFile{remotePath: remotePath, localPath: localPath, size: entry.Size(), mode: mode.Perm()})
		*total += entry.Size()
	}
	return nil
}

func validDirectoryEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/')
}

func availableDirectoryPath(root, name string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	for index := 0; ; index++ {
		candidate := filepath.Join(root, name)
		if index > 0 {
			candidate = filepath.Join(root, fmt.Sprintf("%s-%d", name, index))
		}
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func copyDirectoryFile(ctx context.Context, client *sftp.Client, file directoryDownloadFile, progress *session.TransferProgress) error {
	source, err := client.Open(file.remotePath)
	if err != nil {
		return err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(file.localPath), ".download-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(file.mode); err != nil {
		temporary.Close()
		return err
	}
	reader := &transferContextReader{ctx: ctx, reader: source}
	if _, err := io.CopyBuffer(progress.Writer(temporary), reader, make([]byte, 128*1024)); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, file.localPath)
}

type transferContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *transferContextReader) Read(data []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(data)
	}
}
