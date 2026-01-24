package skills

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

// Watcher watches the skills directory for changes.
type Watcher struct {
	manager *Manager
	watcher *fsnotify.Watcher
	done    chan struct{}
	mu      sync.Mutex
	running bool
}

// NewWatcher creates a new skills watcher.
func NewWatcher(manager *Manager) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		manager: manager,
		watcher: w,
		done:    make(chan struct{}),
	}, nil
}

// Start begins watching for file changes.
func (w *Watcher) Start() error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.mu.Unlock()

	skillsDir := w.manager.skillsDir

	// Add skills directory and subdirectories
	if err := w.addRecursive(skillsDir); err != nil {
		log.WithError(err).Warn("failed to watch skills directory")
	}

	go w.watchLoop()

	log.Infof("skills watcher started for %s", skillsDir)
	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() error {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = false
	w.mu.Unlock()

	close(w.done)
	return w.watcher.Close()
}

func (w *Watcher) addRecursive(path string) error {
	return filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return w.watcher.Add(p)
		}
		return nil
	})
}

func (w *Watcher) watchLoop() {
	// Debounce reloads to avoid multiple reloads for batch changes
	var debounceTimer *time.Timer
	var debounceMu sync.Mutex

	reload := func() {
		debounceMu.Lock()
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(500*time.Millisecond, func() {
			log.Info("skills changed, reloading...")
			if err := w.manager.Reload(); err != nil {
				log.WithError(err).Error("failed to reload skills")
			} else {
				log.Info("skills reloaded successfully")
			}
		})
		debounceMu.Unlock()
	}

	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}

			// Handle file events
			switch {
			case event.Op&fsnotify.Write == fsnotify.Write:
				log.Debugf("skills file modified: %s", event.Name)
				reload()
			case event.Op&fsnotify.Create == fsnotify.Create:
				log.Debugf("skills file created: %s", event.Name)
				// Add new directories to watch
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					w.watcher.Add(event.Name)
				}
				reload()
			case event.Op&fsnotify.Remove == fsnotify.Remove:
				log.Debugf("skills file removed: %s", event.Name)
				reload()
			case event.Op&fsnotify.Rename == fsnotify.Rename:
				log.Debugf("skills file renamed: %s", event.Name)
				reload()
			}

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.WithError(err).Error("skills watcher error")
		}
	}
}
