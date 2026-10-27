package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// ImportLegacyDatabase publishes a consistent SQLite snapshot only when the
// destination does not exist. The previous database and its WAL are preserved.
func ImportLegacyDatabase(ctx context.Context, source, destination string) (bool, error) {
	if _, err := os.Stat(destination); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("legacy database is not a regular file: %s", source)
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".dgx-hub-import-*.db")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	if err = temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return false, err
	}
	defer os.Remove(temporaryPath)
	source, err = filepath.Abs(source)
	if err != nil {
		return false, err
	}
	sourceURLPath := filepath.ToSlash(source)
	if filepath.VolumeName(source) != "" {
		sourceURLPath = "/" + sourceURLPath
	}
	uri := url.URL{Scheme: "file", Path: sourceURLPath}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return false, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// SQLite reads committed WAL data as well as the main file. A plain file
	// copy can lose recent settings when the old app used WAL journal mode.
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", temporaryPath); err != nil {
		return false, fmt.Errorf("snapshot legacy database: %w", err)
	}
	if err = os.Chmod(temporaryPath, 0600); err != nil {
		return false, err
	}
	// The temporary file is on the same filesystem. An exclusive hard link
	// publishes the complete snapshot atomically and cannot replace new data.
	if err = os.Link(temporaryPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("publish imported database: %w", err)
	}
	return true, nil
}
