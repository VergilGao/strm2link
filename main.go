package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sgtdi/fswatcher"
)

var (
	sourceRoot = getEnv("STRM_SOURCE_DIR", "/strm")
	linkRoot   = getEnv("STRM_LINK_DIR", "/link")
)

var strmLinkCache sync.Map

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func main() {
	log.Printf("Starting strm-watcher: source=%s, link=%s", sourceRoot, linkRoot)

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if err := os.MkdirAll(linkRoot, 0755); err != nil {
		return err
	}

	w, err := fswatcher.New(
		fswatcher.WithPath(sourceRoot, fswatcher.WithDepth(fswatcher.WatchNested)),
		fswatcher.WithCooldown(200*time.Millisecond),
	)
	if err != nil {
		return err
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := w.Watch(ctx); err != nil {
			log.Printf("Watch error: %v", err)
		}
	}()

	// 事件处理循环（独立 goroutine）
	go func() {
		for event := range w.Events() {
			handleEvent(event)
		}
	}()

	// 全量扫描（现有文件）
	if err := fullSync(); err != nil {
		log.Printf("Full sync error: %v", err)
	}

	log.Printf("Ready. Watching %s", sourceRoot)
	select {}
}

func fullSync() error {
	log.Println("Full sync started...")
	strmLinkCache.Range(func(key, value interface{}) bool {
		strmLinkCache.Delete(key)
		return true
	})

	var files []string
	err := filepath.WalkDir(sourceRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".strm") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, f := range files {
		if err := processStrmFile(f); err != nil {
			log.Printf("Error processing %s: %v", f, err)
		}
	}
	log.Printf("Full sync finished. Processed %d .strm files", len(files))
	return nil
}

func handleEvent(event fswatcher.WatchEvent) {
	path := event.Path
	if !strings.HasSuffix(path, ".strm") {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if linkPath, ok := strmLinkCache.Load(path); ok {
				deleteLink(linkPath.(string))
				strmLinkCache.Delete(path)
				log.Printf("Removed cached link for %s", path)
			} else {
				deleteLinkByFilename(path)
			}
		}
		return
	}
	if info.IsDir() {
		return
	}
	if err := processStrmFile(path); err != nil {
		log.Printf("Error processing %s: %v", path, err)
	}
}

func processStrmFile(strmPath string) error {
	data, err := os.ReadFile(strmPath)
	if err != nil {
		return err
	}
	target := strings.TrimSpace(string(data))
	if target == "" {
		log.Printf("Empty target in %s", strmPath)
		return nil
	}
	if !filepath.IsAbs(target) {
		log.Printf("Target not absolute in %s: %s", strmPath, target)
		return nil
	}
	relDir, err := filepath.Rel(sourceRoot, filepath.Dir(strmPath))
	if err != nil {
		return err
	}
	if relDir == "." {
		relDir = ""
	}
	linkDir := filepath.Join(linkRoot, relDir)
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		return err
	}
	linkPath := filepath.Join(linkDir, filepath.Base(target))

	if oldLink, ok := strmLinkCache.Load(strmPath); ok {
		if old := oldLink.(string); old != linkPath {
			deleteLink(old)
			strmLinkCache.Delete(strmPath)
		}
	}
	if isSymlinkTo(linkPath, target) {
		if _, ok := strmLinkCache.Load(strmPath); !ok {
			strmLinkCache.Store(strmPath, linkPath)
		}
		return nil
	}
	if err := deleteLink(linkPath); err != nil {
		log.Printf("Warning: failed to delete %s: %v", linkPath, err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return err
	}
	log.Printf("Created/Updated link: %s -> %s", linkPath, target)
	strmLinkCache.Store(strmPath, linkPath)
	return nil
}

func deleteLink(linkPath string) error {
	info, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(linkPath); err != nil {
			return err
		}
		log.Printf("Deleted link: %s", linkPath)
	}
	return nil
}

func deleteLinkByFilename(strmPath string) {
	base := strings.TrimSuffix(filepath.Base(strmPath), ".strm")
	pattern := filepath.Join(linkRoot, "**", base+".*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, m := range matches {
		if isSymlink(m) {
			if err := os.Remove(m); err == nil {
				log.Printf("Deleted link (by pattern): %s", m)
			}
		}
	}
}

func isSymlinkTo(p, target string) bool {
	dest, err := os.Readlink(p)
	return err == nil && dest == target
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
