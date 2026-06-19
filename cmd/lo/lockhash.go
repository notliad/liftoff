package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

func lockCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheDir, "liftoff", "lock-hashes")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func projectCacheKey(projectPath string) string {
	h := sha256.Sum256([]byte(projectPath))
	return hex.EncodeToString(h[:])
}

func lockFilePath(projectPath string) string {
	candidates := []string{
		"pnpm-lock.yaml",
		"bun.lock",
		"bun.lockb",
		"package-lock.json",
		"yarn.lock",
	}
	for _, name := range candidates {
		path := filepath.Join(projectPath, name)
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func computeLockHash(projectPath string) (string, error) {
	path := lockFilePath(projectPath)
	if path == "" {
		return "", errors.New("no lockfile found")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func cachedLockHash(projectPath string) (string, error) {
	cacheDir, err := lockCacheDir()
	if err != nil {
		return "", err
	}
	key := projectCacheKey(projectPath)
	data, err := os.ReadFile(filepath.Join(cacheDir, key))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func writeLockHash(projectPath string, hash string) error {
	cacheDir, err := lockCacheDir()
	if err != nil {
		return err
	}
	key := projectCacheKey(projectPath)
	return os.WriteFile(filepath.Join(cacheDir, key), []byte(hash), 0644)
}

func needsReinstall(projectPath string) bool {
	lockFile := lockFilePath(projectPath)
	if lockFile == "" {
		return false
	}
	currentHash, err := computeLockHash(projectPath)
	if err != nil {
		return false
	}
	cached, err := cachedLockHash(projectPath)
	if err != nil {
		writeLockHash(projectPath, currentHash)
		return false
	}
	return currentHash != cached
}

func updateLockHash(projectPath string) {
	hash, err := computeLockHash(projectPath)
	if err != nil {
		return
	}
	writeLockHash(projectPath, hash)
}
