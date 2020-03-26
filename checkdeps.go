// checkdeps is a tool analyze rust Cargo.lock files and figure out why
// there are multiple versions of some transitive dependencies.
package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml"
)

// Represents a package in the Cargo.lock file.
type Package struct {
	Name string `toml:"name"`
	Ver  string `toml:"version"`
	Src  string `toml:"source"`

	DepStrings []string `toml:"dependencies"`
	Deps       []Dep    `toml:"-"`

	// The number of versions of this package.
	versions int

	// The maximum over number of versions present for the dependencies.
	depVersions int

	// The number of packages which depend on this one.
	incoming int

	// Strictly more packages depend on this version of the package than on
	// other versions.
	popular bool

	// This package is in the transitive dependencies of a package that has
	// more than one version and is not "popular."
	depOfMulti bool
}

// Write the most appropriate URL for this package to the given target.
//
// If specific is true, it will write a URL to the specific version of the
// package, otherwise to the overall project.
//
// baseurl is the URL prefix to use for crates which do not have a source
// specified.  The url will be baseurl/<crate>/Cargo.toml, on the assumption
// that these are local crates within a workspace.
func (pkg *Package) WriteUrl(w *os.File, specific bool, baseurl string) {
	if strings.HasPrefix(pkg.Src, "git+http") {
		rawurl := strings.TrimPrefix(pkg.Src, "git+")
		u, err := url.Parse(rawurl)
		if err != nil {
			panic(err)
		}
		if u.Host == "github.com" && u.Fragment != "" {
			u.RawQuery = ""
			hash := u.Fragment
			u.Fragment = ""
			if specific {
				u.Path = strings.TrimSuffix(u.Path, ".git")
			}
			writeString(w, u.String())
			if specific {
				writeString(w, "/tree/")
				writeString(w, hash)
			}
		} else {
			writeString(w, rawurl)
		}
	} else if pkg.Src == "" && baseurl != "" {
		writeString(w, baseurl)
		if baseurl[len(baseurl)-1] != '/' {
			writeString(w, "/")
		}
		writeString(w, pkg.Name)
		writeString(w, "/Cargo.toml")
	} else if strings.HasPrefix(pkg.Src, "registry") {
		writeString(w, `https://crates.io/crates/`)
		writeString(w, pkg.Name)
		if specific {
			writeString(w, "/")
			writeString(w, pkg.Ver)
		}
	} else {
		writeString(w, `https://crates.io/search?q=`)
		writeString(w, pkg.Name)
	}
}

// Represents a dependency for a package in the Cargo.lock file.  This is parsed
// from a string, e.g.
// "vector_utils 0.1.0 (registry+https://github.com/rust-lang/crates.io-index)"
type Dep struct {
	Name string
	Ver  string
	Src  string

	// Pointer to the corresponding package object.  Populated by makePkgMap.
	Pkg *Package
}

var depsRe = regexp.MustCompile(`(\S+)\s+(\S+)(?:\s\(([^)]+)\))?`)

func (p *Package) ParseDeps() error {
	if len(p.DepStrings) == 0 {
		p.Deps = nil
		return nil
	}
	p.Deps = make([]Dep, 0, len(p.DepStrings))
	for _, d := range p.DepStrings {
		m := depsRe.FindStringSubmatch(d)
		if len(m) > 0 {
			p.Deps = append(p.Deps, Dep{
				Name: m[1],
				Ver:  m[2],
				Src:  m[3],
			})
		} else {
			fmt.Fprintln(os.Stderr, "failed to parse", d)
		}
	}
	return nil
}

func loadCrates(filename string) ([]*Package, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := toml.NewDecoder(f)
	var result struct {
		Packages []*Package `toml:"package"`
	}
	err = dec.Decode(&result)
	if err != nil {
		return result.Packages, err
	}
	for _, pkg := range result.Packages {
		if err := pkg.ParseDeps(); err != nil {
			return result.Packages, err
		}
	}
	return result.Packages, err
}

func main() {
	var dot, trim bool
	flag.BoolVar(&dot, "dot", false,
		"Render deps graph in dot format.")
	flag.BoolVar(&trim, "trim", false,
		"Remove nodes from the graph if they do not depend "+
			"transitively on a package for which more than one version exists.")
	var baseurl string
	flag.StringVar(&baseurl, "baseurl", "",
		"A `url` prefix to use for making hyperlinks for packages in this "+
			"workspace, e.g. https://github.com/<org>/<repo>/blob/master/")
	flag.Parse()
	pkgs, err := loadCrates(flag.Arg(0))
	if err != nil {
		panic(err.Error())
	}
	pkgMap := makePkgMap(pkgs)
	if trim {
		pkgs = trimPkgs(pkgMap, pkgs)
	}

	if dot {
		writeDot(pkgs, baseurl, os.Stdout)
	} else {
		for _, pkg := range pkgs {
			if pkg.versions == 1 && pkg.depVersions > 1 && !pkg.depOfMulti {
				for _, dep := range pkg.Deps {
					if dep.Pkg.versions > 1 {
						fmt.Println(pkg.Name, "@", pkg.Ver, "brings in", dep.Name, "@", dep.Ver)
					}
				}
			}
		}
	}
}

// Generates a lookup table for packages by name and version.
//
// Also populates the private fields of packages and their dependencies.
func makePkgMap(pkgList []*Package) map[string]map[string]*Package {
	pkgs := make(map[string]map[string]*Package, len(pkgList))
	for _, pkg := range pkgList {
		v := pkgs[pkg.Name]
		if v == nil {
			v = make(map[string]*Package, 1)
			pkgs[pkg.Name] = v
		}
		v[pkg.Ver] = pkg
	}
	for _, pkg := range pkgList {
		pkg.versions = len(pkgs[pkg.Name])
		for i, dep := range pkg.Deps {
			d := pkgs[dep.Name]
			p := d[dep.Ver]
			pkg.Deps[i].Pkg = p
			p.incoming++
		}
	}
	for _, pkg := range pkgList {
		if pkg.versions > 1 {
			ov := pkgs[pkg.Name]
			maxIncoming := 0
			for _, op := range ov {
				if op != pkg && op.incoming > maxIncoming {
					maxIncoming = op.incoming
				}
			}
			pkg.popular = pkg.incoming > maxIncoming
		}
	}
	change := true
	for change {
		change = false
		for _, pkg := range pkgList {
			for _, dep := range pkg.Deps {
				if !dep.Pkg.depOfMulti {
					if (pkg.versions > 1 && (dep.Pkg.versions > 1 || !pkg.popular)) || pkg.depOfMulti {
						dep.Pkg.depOfMulti = true
						change = true
					}
				}
			}
		}
	}

	for _, pkg := range pkgList {
		pkg.versions = len(pkgs[pkg.Name])
		for _, dep := range pkg.Deps {
			if !dep.Pkg.popular && dep.Pkg.versions > pkg.depVersions {
				pkg.depVersions = dep.Pkg.versions
			}
		}
	}
	return pkgs
}

func trimPkgs(pkgs map[string]map[string]*Package, pkgList []*Package) []*Package {
	anyChange := false
	change := true
	for change {
		change = false
		for _, pkg := range pkgList {
			newDeps := make([]Dep, 0, len(pkg.Deps))
			for _, dep := range pkg.Deps {
				dv := pkgs[dep.Name]
				if len(dv) > 0 {
					newDeps = append(newDeps, dep)
				}
			}
			if len(newDeps) != len(pkg.Deps) {
				pkg.Deps = newDeps
			}
			v, ok := pkgs[pkg.Name]
			if ok && len(v) < 2 && len(pkg.Deps) == 0 {
				fmt.Fprintln(os.Stderr, "removing", pkg.Name, "from the graph")
				delete(pkgs, pkg.Name)
				change = true
				anyChange = true
			}
		}
	}
	if !anyChange {
		return pkgList
	}
	newPkgs := make([]*Package, 0, (len(pkgList)+len(pkgs))/2)
	for _, pkg := range pkgList {
		if pkgs[pkg.Name][pkg.Ver] != nil {
			newPkgs = append(newPkgs, pkg)
		}
	}
	return newPkgs
}

// panicing convenience wrapper for WriteString.
func writeString(w *os.File, s string) {
	if _, err := w.WriteString(s); err != nil {
		panic(err)
	}
}

func writeDot(pkgs []*Package, baseurl string, w *os.File) {
	writeString(w, "digraph crates {\n")
	writeDotNodes(pkgs, baseurl, w)
	writeDotEdges(pkgs, w)
	writeString(w, "}\n")
}

func writeDotNodes(pkgs []*Package, baseurl string, w *os.File) {
	pkgNames := make(map[string][]*Package, len(pkgs))
	for _, pkg := range pkgs {
		pkgNames[pkg.Name] = append(pkgNames[pkg.Name], pkg)
	}
	for _, pkg := range pkgs {
		versions := pkgNames[pkg.Name]
		if len(versions) == 0 {
			continue
		}
		delete(pkgNames, pkg.Name)
		indent := "  "
		if len(versions) > 1 {
			writeString(w, "  subgraph \"cluster")
			writeString(w, pkg.Name)
			writeString(w, "\" {\n    id = \"")
			writeString(w, pkg.Name)
			writeString(w, "\";\n    rank = \"max\";\n    label = \"")
			writeString(w, pkg.Name)
			writeString(w, "\";\n    URL = \"")
			pkg.WriteUrl(w, false, baseurl)
			writeString(w, "\";\n")
			indent = "    "
		}
		for _, pkg := range versions {
			writeString(w, indent)
			writeString(w, `"`)
			pkg.writeDotId(w)
			writeString(w, `" `)
			pkg.writeNodeDotAttrs(w, baseurl)
		}
		if len(versions) > 1 {
			writeString(w, "  }\n")
		}
	}
}

func (pkg *Package) writeDotId(w *os.File) {
	writeString(w, pkg.Name)
	writeString(w, "@")
	writeString(w, pkg.Ver)
}

func (pkg *Package) writeNodeDotAttrs(w *os.File, baseurl string) {
	writeString(w, `[id="`)
	writeString(w, pkg.Name)
	if pkg.versions > 1 {
		writeString(w, "@")
		writeString(w, pkg.Ver)
		writeString(w, `"; label="`)
		writeString(w, pkg.Ver)
		writeString(w, `"; shape="box`)
	} else if pkg.Src == "" {
		writeString(w, `"; label="`)
		writeString(w, pkg.Name)
	}
	writeString(w, `"; URL="`)
	pkg.WriteUrl(w, true, baseurl)
	if pkg.versions == 1 && pkg.depVersions > 1 && !pkg.depOfMulti {
		writeString(w, "\"; color=\"blue\"; style=\"filled\"; fillcolor=\"yellow")
	} else if pkg.versions > 1 && !pkg.depOfMulti {
		writeString(w, "\"; color=\"red")
	} else if pkg.versions > 1 {
		writeString(w, "\"; color=\"orange")
	} else if pkg.depOfMulti && !pkg.popular {
		writeString(w, "\"; color=\"yellow")
	}
	writeString(w, "\"];\n")
}

func writeDotEdges(pkgs []*Package, w *os.File) {
	for _, pkg := range pkgs {
		pkg.writeDotEdges(w)
	}
}

func (pkg *Package) writeDotEdges(w *os.File) {
	for _, dep := range pkg.Deps {
		writeString(w, "  ")
		writeString(w, `"`)
		pkg.writeDotId(w)
		writeString(w, "\" -> \"")
		dep.Pkg.writeDotId(w)
		if pkg.versions == 1 && !pkg.depOfMulti && dep.Pkg.versions > 1 {
			if dep.Pkg.popular {
				writeString(w, "\" [color=\"blue\"; penwidth=2];\n")
			} else {
				writeString(w, "\" [color=\"red\"; penwidth=3];\n")
			}
		} else if dep.Pkg.versions > 1 {
			writeString(w, "\" [color=\"orange\"];\n")
		} else if pkg.versions > 1 {
			writeString(w, "\" [color=\"blue\"];\n")
		} else {
			writeString(w, "\" [penwidth=1.5];\n")
		}
	}
}
