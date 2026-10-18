package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gox-lang/server/internal/goxsource"
)

type RouteEntry struct {
	Route string
	File  string
}

func (p *Project) Routes() ([]RouteEntry, error) {
	pagesDir := filepath.Join(p.RootDir, "pages")

	var routes []RouteEntry
	seen := map[string]int{}

	err := filepath.WalkDir(pagesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if path != pagesDir && len(d.Name()) > 0 && d.Name()[0] == '@' {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(p.RootDir, path)
		if err != nil {
			return err
		}

		rel = filepath.ToSlash(rel)
		if !goxsource.IsPublicPage(rel) {
			return nil
		}
		route := CleanRoute(rel)

		if index, ok := seen[route]; ok {
			previous := routes[index]
			previousPriority := pageRoutePriority(previous.File, route)
			priority := pageRoutePriority(rel, route)
			if priority == previousPriority {
				return fmt.Errorf("duplicate route %s: %s and %s", route, previous.File, rel)
			}
			if priority < previousPriority {
				routes[index].File = rel
			}
			return nil
		}

		seen[route] = len(routes)
		routes = append(routes, RouteEntry{
			Route: route,
			File:  rel,
		})

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Route < routes[j].Route
	})

	return routes, nil
}

// ResolvePageRoute returns the public page selected for an HTTP route.
// The runtime represents the root page internally as /index, while the
// canonical project route is /. Both forms resolve to pages/index.gox.
func (p *Project) ResolvePageRoute(route string) (RouteEntry, bool, error) {
	route = strings.TrimSpace(route)
	if route == "" {
		return RouteEntry{}, false, nil
	}
	if len(route) > 1 {
		route = strings.TrimRight(route, "/")
	}
	route = strings.ToLower(route)
	if route == "/index" {
		route = "/"
	}

	routes, err := p.Routes()
	if err != nil {
		return RouteEntry{}, false, err
	}
	for _, entry := range routes {
		if entry.Route == route {
			return entry, true, nil
		}
	}
	return RouteEntry{}, false, nil
}

func pageRoutePriority(rel, route string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(rel), "pages/"), ".gox")
	if "/"+strings.ToLower(value) == route {
		return 0
	}
	return 1
}
