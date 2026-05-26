package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/buildhost/internal/auth"
)

func init() {
	rootCmd.AddCommand(routesCmd)
}

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Print all registered HTTP routes",
	Run: func(_ *cobra.Command, _ []string) {
		type route struct {
			path   string
			method string
		}
		var routes []route
		for _, pat := range auth.Patterns() {
			method, path, ok := strings.Cut(pat, " ")
			if !ok {
				routes = append(routes, route{path: method})
			} else {
				routes = append(routes, route{path: path, method: method})
			}
		}
		slices.SortFunc(routes, func(a, b route) int {
			if c := strings.Compare(a.path, b.path); c != 0 {
				return c
			}
			return strings.Compare(a.method, b.method)
		})

		grouped := map[string][]string{}
		var paths []string
		for _, r := range routes {
			if _, seen := grouped[r.path]; !seen {
				paths = append(paths, r.path)
			}
			if r.method != "" {
				grouped[r.path] = append(grouped[r.path], r.method)
			}
		}
		for _, p := range paths {
			if methods := grouped[p]; len(methods) > 0 {
				fmt.Printf("%s {%s}\n", p, strings.Join(methods, ","))
			} else {
				fmt.Printf("%s {*}\n", p)
			}
		}
	},
}
