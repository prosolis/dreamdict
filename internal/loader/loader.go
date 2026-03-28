package loader

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

type Loader interface {
	Name() string
	Load(db *sql.DB, dataDir string) error
}

func Run(db *sql.DB, dataDir string, loaders []Loader, skip map[string]bool) error {
	for _, l := range loaders {
		if skip[l.Name()] {
			slog.Info("skipping loader", "name", l.Name())
			continue
		}
		slog.Info("running loader", "name", l.Name())
		start := time.Now()
		if err := l.Load(db, dataDir); err != nil {
			return fmt.Errorf("loader %s: %w", l.Name(), err)
		}
		slog.Info("loader complete", "name", l.Name(), "elapsed", time.Since(start).Round(time.Millisecond))
	}
	return nil
}
