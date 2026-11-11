package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func (s *Server) sftpDownloadFile(w http.ResponseWriter, r *http.Request) {
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
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		writeError(w, http.StatusBadRequest, "SFTP_NOT_FILE", "remote path is not a regular file")
		return
	}
	name := path.Base(remotePath)
	if !validDirectoryEntryName(name) {
		writeError(w, http.StatusBadRequest, "SFTP_INVALID_FILE", "file name is invalid")
		return
	}
	downloadRoot, err := s.defaultDownloadDirectory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	destination, err := availableFilePath(downloadRoot, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DOWNLOAD_PATH_FAILED", err.Error())
		return
	}
	progress := found.NewTransfer("download", remotePath, info.Size())
	progress.SetCurrentPath(remotePath)
	file := directoryDownloadFile{remotePath: remotePath, localPath: destination, size: info.Size(), mode: info.Mode().Perm()}
	if err := copyDirectoryFile(r.Context(), client, file, progress); err != nil {
		progress.Finish(err)
		writeError(w, http.StatusBadGateway, "SFTP_DOWNLOAD_FAILED", err.Error())
		return
	}
	progress.Finish(nil)
	writeJSON(w, http.StatusOK, map[string]any{"path": destination, "bytes": info.Size()})
}

func availableFilePath(root, name string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if stem == "" {
		stem = name
		extension = ""
	}
	for index := 0; ; index++ {
		candidate := filepath.Join(root, name)
		if index > 0 {
			candidate = filepath.Join(root, fmt.Sprintf("%s-%d%s", stem, index, extension))
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
