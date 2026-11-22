package soundcloud

import (
	"fmt"
	"os"
	"path/filepath"
)

func cacheFile(trackID int64) string {
	return filepath.Join(cacheDir, fmt.Sprintf("%d.mp3", trackID))
}

func readCache(trackID int64, maxBytes int64) ([]byte, bool) {
	path := cacheFile(trackID)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func writeCache(trackID int64, data []byte) {
	path := cacheFile(trackID)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	tmp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.tmp", trackID))
	if err != nil {
		return
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
	}
}
