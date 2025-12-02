// Package watcher provides file watching with debouncing for hot-reload.
package watcher

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
)

// Watcher monitors files for changes with debouncing.
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	callbacks map[string]func()
	debounce  time.Duration
	timers    map[string]*time.Timer
	mu        sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

// Config holds watcher configuration.
type Config struct {
	// DebounceInterval is the time to wait before triggering callback after last change.
	// This prevents multiple rapid saves from triggering multiple reloads.
	DebounceInterval time.Duration
}

// DefaultConfig returns default watcher configuration.
//
// Returns:
//   - *Config: default configuration with 500ms debounce
func DefaultConfig() *Config {
	return &Config{
		DebounceInterval: 500 * time.Millisecond,
	}
}

// New creates a new file watcher.
//
// Parameters:
//   - cfg (*Config): watcher configuration
//
// Returns:
//   - *Watcher: initialized watcher
//   - error: nil on success, initialization error on failure
func New(cfg *Config) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		fsWatcher: fsWatcher,
		callbacks: make(map[string]func()),
		debounce:  cfg.DebounceInterval,
		timers:    make(map[string]*time.Timer),
		done:      make(chan struct{}),
	}, nil
}

// Watch adds a file to watch with a callback for changes.
//
// Parameters:
//   - path (string): file path to watch
//   - callback (func()): function to call on file change
//
// Returns:
//   - error: nil on success, watch error on failure
func (w *Watcher) Watch(path string, callback func()) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	// Watch the directory containing the file (handles editor atomic saves)
	dir := filepath.Dir(absPath)
	if err := w.fsWatcher.Add(dir); err != nil {
		return err
	}

	w.mu.Lock()
	w.callbacks[absPath] = callback
	w.mu.Unlock()

	log.Debug().
		Str("file", absPath).
		Str("dir", dir).
		Msg("watching file for changes")

	return nil
}

// Start begins watching for file changes.
// This method blocks until Close() is called.
//
// Returns:
//   - error: nil on normal shutdown, error on watcher failure
func (w *Watcher) Start() error {
	for {
		select {
		case <-w.done:
			return nil

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return nil
			}

			// Handle write and create events (create handles atomic saves)
			if event.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				absPath, err := filepath.Abs(event.Name)
				if err != nil {
					continue
				}

				w.mu.Lock()
				callback, exists := w.callbacks[absPath]
				if exists {
					// Debounce: cancel existing timer and set new one
					if timer, ok := w.timers[absPath]; ok {
						timer.Stop()
					}

					w.timers[absPath] = time.AfterFunc(w.debounce, func() {
						log.Info().
							Str("file", filepath.Base(absPath)).
							Msg("file changed, triggering reload")
						callback()
					})
				}
				w.mu.Unlock()
			}

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return nil
			}
			log.Error().Err(err).Msg("watcher error")
		}
	}
}

// Close stops the watcher. Safe to call multiple times.
//
// Returns:
//   - error: nil on success, close error on failure
func (w *Watcher) Close() error {
	var err error
	w.closeOnce.Do(func() {
		close(w.done)

		// Stop all pending timers
		w.mu.Lock()
		for _, timer := range w.timers {
			timer.Stop()
		}
		w.mu.Unlock()

		err = w.fsWatcher.Close()
	})
	return err
}
