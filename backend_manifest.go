package kvlite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	backendManifestFile    = "KVLITE-MANIFEST.json"
	backendManifestVersion = 1
)

type backendManifest struct {
	KVLiteFormatVersion int     `json:"kvlite_format_version"`
	Backend             Backend `json:"backend"`
	Driver              string  `json:"driver"`
	DriverFormat        string  `json:"driver_format"`
	DriverVersion       string  `json:"driver_version"`
}

func prepareBackendManifest(path string, driver registeredDriver, allowLegacyRocksDB bool) (legacyManifest bool, err error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return false, fmt.Errorf("kvlite: create database directory: %w", err)
	}

	manifest, found, err := readBackendManifest(path)
	if err != nil {
		return false, err
	}
	if found {
		return false, validateBackendManifest(manifest, driver)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("kvlite: inspect database directory: %w", err)
	}
	if len(entries) == 0 {
		if err := createBackendManifest(path, driver); err != nil {
			return false, err
		}
		return false, nil
	}
	if allowLegacyRocksDB {
		// Older KVLite releases always opened RocksDB and consequently did not
		// write a backend manifest. Preserve that one default upgrade path, but
		// only write metadata after RocksDB has opened the legacy directory.
		return true, nil
	}
	return false, fmt.Errorf(
		"%w: %q is non-empty and has no %s; migrate it into a new KVLite directory instead of selecting %q",
		ErrBackendManifestMissing,
		path,
		backendManifestFile,
		driver.info.Backend,
	)
}

func readBackendManifest(path string) (backendManifest, bool, error) {
	data, err := os.ReadFile(filepath.Join(path, backendManifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return backendManifest{}, false, nil
	}
	if err != nil {
		return backendManifest{}, false, fmt.Errorf("kvlite: read backend manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest backendManifest
	if err := decoder.Decode(&manifest); err != nil {
		return backendManifest{}, false, fmt.Errorf("%w: %v", ErrBackendManifestInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return backendManifest{}, false, fmt.Errorf("%w: multiple JSON values", ErrBackendManifestInvalid)
	}
	return manifest, true, nil
}

func validateBackendManifest(manifest backendManifest, driver registeredDriver) error {
	if manifest.KVLiteFormatVersion != backendManifestVersion || manifest.Backend == "" || manifest.Driver == "" || manifest.DriverFormat == "" || manifest.DriverVersion == "" {
		return fmt.Errorf("%w: expected format version %d", ErrBackendManifestInvalid, backendManifestVersion)
	}
	if manifest.Backend != driver.info.Backend || manifest.Driver != driver.info.Implementation || manifest.DriverFormat != driver.info.Format {
		return fmt.Errorf(
			"%w: path was initialized for backend %q (%s format %s), requested %q (%s format %s)",
			ErrBackendMismatch,
			manifest.Backend,
			manifest.Driver,
			manifest.DriverFormat,
			driver.info.Backend,
			driver.info.Implementation,
			driver.info.Format,
		)
	}
	return nil
}

func createBackendManifest(path string, driver registeredDriver) error {
	manifest := backendManifest{
		KVLiteFormatVersion: backendManifestVersion,
		Backend:             driver.info.Backend,
		Driver:              driver.info.Implementation,
		DriverFormat:        driver.info.Format,
		DriverVersion:       driver.info.Version,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("kvlite: encode backend manifest: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(filepath.Join(path, backendManifestFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		existing, found, readErr := readBackendManifest(path)
		if readErr != nil {
			return readErr
		}
		if !found {
			return fmt.Errorf("%w: manifest disappeared while opening the database", ErrBackendManifestInvalid)
		}
		return validateBackendManifest(existing, driver)
	}
	if err != nil {
		return fmt.Errorf("kvlite: create backend manifest: %w", err)
	}
	defer file.Close()
	written, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("kvlite: write backend manifest: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("kvlite: write backend manifest: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("kvlite: sync backend manifest: %w", err)
	}
	return nil
}
