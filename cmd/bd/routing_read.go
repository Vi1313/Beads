package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/routing"
	"github.com/steveyegge/beads/internal/storage"
)

var routingConfigKeys = []string{
	"routing.mode",
	"routing.contributor",
	"routing.default",
	"routing.maintainer",
	"contributor.auto_route",
	"contributor.planning_repo",
}

func resolveRoutingConfigValue(key string, dbValues map[string]string) string {
	if src := config.GetValueSource(key); src != config.SourceDefault {
		if value := strings.TrimSpace(config.GetString(key)); value != "" {
			return value
		}
	}
	return strings.TrimSpace(dbValues[key])
}

func getRoutingConfigValue(ctx context.Context, store storage.DoltStorage, key string) string {
	if src := config.GetValueSource(key); src != config.SourceDefault {
		if value := strings.TrimSpace(config.GetString(key)); value != "" {
			return value
		}
	}
	if store == nil {
		return ""
	}
	dbValue, err := store.GetConfig(ctx, key)
	if err != nil {
		debug.Logf("DEBUG: failed to read config %q from store: %v\n", key, err)
		return ""
	}
	return strings.TrimSpace(dbValue)
}

func determineAutoRoutedRepoPath(ctx context.Context, store storage.DoltStorage) string {
	userRole, err := routing.DetectUserRole(".")
	if err != nil {
		debug.Logf("Warning: failed to detect user role: %v\n", err)
	}

	var dbValues map[string]string
	if store != nil {
		all, allErr := store.GetAllConfig(ctx)
		if allErr != nil {
			debug.Logf("DEBUG: failed to read config from store: %v\n", allErr)
		} else {
			dbValues = make(map[string]string, len(routingConfigKeys))
			for _, key := range routingConfigKeys {
				if v, ok := all[key]; ok {
					dbValues[key] = v
				}
			}
		}
	}

	routingMode := resolveRoutingConfigValue("routing.mode", dbValues)
	contributorRepo := resolveRoutingConfigValue("routing.contributor", dbValues)

	if routingMode == "" {
		if resolveRoutingConfigValue("contributor.auto_route", dbValues) == "true" {
			routingMode = "auto"
		}
	}
	if contributorRepo == "" {
		contributorRepo = resolveRoutingConfigValue("contributor.planning_repo", dbValues)
	}

	routingConfig := &routing.RoutingConfig{
		Mode:             routingMode,
		DefaultRepo:      resolveRoutingConfigValue("routing.default", dbValues),
		MaintainerRepo:   resolveRoutingConfigValue("routing.maintainer", dbValues),
		ContributorRepo:  contributorRepo,
		ExplicitOverride: "",
	}

	return routing.DetermineTargetRepo(routingConfig, userRole, ".")
}

// openRoutedReadStore opens the auto-routed target store for read commands.
// Returns routed=false when reads should stay in the current store.
//
// For write-intent callers (bd close / bd reopen / bd update / etc. targeting
// an issue that lives in the contributor planning store), use
// openRoutedWriteStore instead — opening read-only here is what produces
// `embeddeddolt: store is read-only` when the write transaction commits
// (beads-rgw: bd close Notes-935.10.7 from a contributor workspace whose
// .beads/config.yaml declares `additional: ~/.beads-planning` and has no
// routes.jsonl).
func openRoutedReadStore(ctx context.Context, store storage.DoltStorage) (storage.DoltStorage, bool, error) {
	return openRoutedStore(ctx, store, false)
}

// openRoutedWriteStore opens the auto-routed target store writable so a
// mutating command (bd close, bd reopen, bd update, …) can commit on the
// target store's head. Mirrors the prefix-routed write-intent path
// resolveViaPrefixRoutingMode(_, _, /*forWrite=*/true) (#4141) so contributor
// auto-routing has the same write-through behavior — the original GH#2345
// design always intended close/update to write through the routed target
// (cmd/bd/info.go: "FIX: Contributor auto-routing fallback for show/update/close
// (GH#2345)").
func openRoutedWriteStore(ctx context.Context, store storage.DoltStorage) (storage.DoltStorage, bool, error) {
	return openRoutedStore(ctx, store, true)
}

// openRoutedStore is the shared implementation behind openRoutedReadStore and
// openRoutedWriteStore. Returns routed=false (with a nil store and nil error)
// when no auto-routing is configured, so callers can keep operating on the
// local store.
func openRoutedStore(ctx context.Context, store storage.DoltStorage, forWrite bool) (storage.DoltStorage, bool, error) {
	repoPath := determineAutoRoutedRepoPath(ctx, store)
	if repoPath == "" || repoPath == "." {
		return nil, false, nil
	}

	targetRepoPath := routing.ExpandPath(repoPath)
	targetBeadsDir := filepath.Join(targetRepoPath, ".beads")

	open := newReadOnlyStoreFromConfig
	if forWrite {
		open = newDoltStoreFromConfig
	}
	targetStore, err := open(ctx, targetBeadsDir)
	if err != nil {
		return nil, false, fmt.Errorf("failed to open routed store at %s: %w", targetRepoPath, err)
	}
	return targetStore, true, nil
}
