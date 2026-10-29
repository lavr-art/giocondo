package provision

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"time"
)

// File is an agent installation file, with a path relative to the installation directory.
type File struct {
	Path string
	Mode int64
	Data []byte
}

// TarGz packs the files into directory dir inside the archive.
func TarGz(dir string, files []File, mtime time.Time) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Name: dir + "/" + f.Path, Mode: f.Mode, Size: int64(len(f.Data)), ModTime: mtime}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
