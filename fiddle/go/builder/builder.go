package builder

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/skia-dev/glog"
	"go.skia.org/infra/go/buildskia"
	"go.skia.org/infra/go/vcsinfo"
)

// errors
var (
	AlreadyExistsErr = errors.New("Checkout already exists.")
)

var (
	branchRegex = regexp.MustCompile("^refs/heads/chrome/m([0-9]+)$")
)

// branch is used to sort the chrome branches in the Skia repo.
type branch struct {
	N    int
	Name string
	Hash string
}

// branchSlice is a utility class for sorting slices of branch.
type branchSlice []branch

func (p branchSlice) Len() int           { return len(p) }
func (p branchSlice) Less(i, j int) bool { return p[i].N > p[j].N }
func (p branchSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// prepDirectory adds the 'versions' directory to the fiddleRoot
// and returns the full path of that directory.
func prepDirectory(fiddleRoot string) (string, error) {
	versions := path.Join(fiddleRoot, "versions")
	if err := os.MkdirAll(versions, 0777); err != nil {
		return "", fmt.Errorf("Failed to create FIDDLE_ROOT/versions dir: %s", err)
	}
	return versions, nil
}

// buildLib, given a directory that Skia is checked out into, builds libskia.a
// and fiddle_main.o.
func buildLib(checkout string) error {
	glog.Info("Starting CMakeBuild")
	if err := buildskia.CMakeBuild(checkout, buildskia.RELEASE_BUILD); err != nil {
		return fmt.Errorf("Failed cmake build: %s", err)
	}

	glog.Info("Building fiddle_main.o")
	files := []string{
		filepath.Join(checkout, "experimental", "fiddle", "fiddle_main.cpp"),
	}
	if err := buildskia.CMakeCompile(checkout, path.Join(checkout, "cmakeout", "fiddle_main.o"), files, []string{}); err != nil {
		return fmt.Errorf("Failed cmake build of fiddle_main: %s", err)
	}
	return nil
}

// BuildLatestSkia builds the LKGR of Skia in the given fiddleRoot directory.
//
// The library will be checked out into fiddleRoot + "/" + githash, where githash
// is the githash of the LKGR of Skia.
//
//    fiddleRoot - The root directory where fiddle stores its files. See DESIGN.md.
//    depotTools - The directory where depot_tools is checked out.
//    force - If true then checkout and build even if the directory already exists.
//    head - If true then build Skia at HEAD, otherwise build Skia at LKGR.
//    deps - If true then install Skia dependencies.
//
// Returns the commit info for the revision of Skia checked out.
// Returns an error if any step fails, or return AlreadyExistsErr if
// the target checkout directory already exists and force is false.
func BuildLatestSkia(fiddleRoot, depotTools string, force bool, head bool, deps bool) (*vcsinfo.LongCommit, error) {
	versions, err := prepDirectory(fiddleRoot)
	if err != nil {
		return nil, err
	}

	githash := ""
	if head {
		if githash, err = buildskia.GetSkiaHead(nil); err != nil {
			return nil, fmt.Errorf("Failed to retrieve Skia HEAD: %s", err)
		}
	} else {
		if githash, err = buildskia.GetSkiaHash(nil); err != nil {
			return nil, fmt.Errorf("Failed to retrieve Skia LKGR: %s", err)
		}
	}
	checkout := path.Join(versions, githash)

	fi, err := os.Stat(checkout)
	// If the file is present and a directory then only proceed if 'force' is true.
	if err == nil && fi.IsDir() == true && !force {
		return nil, AlreadyExistsErr
	}

	ret, err := buildskia.DownloadSkia("", githash, checkout, depotTools, false, deps)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch: %s", err)
	}

	if err := buildLib(checkout); err != nil {
		return nil, err
	}
	return ret, nil
}

// BuildLatestSkiaChromeBranch builds the most recent branch of Skia for Chrome
// in the given fiddleRoot directory.
//
// The library will be checked out into fiddleRoot + "/" + mNN, where mNN
// is the short name of the branch for Chrome. The mNN is chosen as the largest
// NN from all the branches named refs/heads/chrome/m[0-9]+.
//
// fiddleRoot - The root directory where fiddle stores its files. See DESIGN.md.
// depotTools - The directory where depot_tools is checked out.
// force - If true then checkout and build even if the directory already exists.
//
// Returns the commit info for the revision of Skia checked out.
// Returns an error if any step fails, or return AlreadyExistsErr if
// the target checkout directory already exists and force is false.
func BuildLatestSkiaChromeBranch(fiddleRoot, depotTools string, force bool) (string, *vcsinfo.LongCommit, error) {
	versions, err := prepDirectory(fiddleRoot)
	if err != nil {
		return "", nil, err
	}
	branches, err := buildskia.GetSkiaBranches(nil)
	if err != nil {
		return "", nil, fmt.Errorf("Failed to retrieve branch info: %s", err)
	}
	if len(branches) == 0 {
		return "", nil, fmt.Errorf("There must be at least one branch.")
	}

	branchNums := []branch{}
	for name, br := range branches {
		if match := branchRegex.FindStringSubmatch(name); match != nil {
			n, err := strconv.Atoi(match[1])
			if err != nil {
				glog.Errorf("Failed to parse branch number: %s", err)
				continue
			}
			branchNums = append(branchNums, branch{N: n, Name: name, Hash: br.Value})
		}
	}
	sort.Sort(branchSlice(branchNums))
	if len(branchNums) == 0 {
		return "", nil, fmt.Errorf("Failed to find any appropriate branches.")
	}

	branchName := fmt.Sprintf("m%d", branchNums[0].N)
	glog.Infof("Target branch number is: %d", branchName)

	checkout := path.Join(versions, branchName)

	fi, err := os.Stat(checkout)
	// If the file is present and a directory then only proceed if 'force' is true.
	if err == nil && fi.IsDir() == true && !force {
		return "", nil, AlreadyExistsErr
	}

	res, err := buildskia.DownloadSkia(branchNums[0].Name, branchNums[0].Hash, checkout, depotTools, false, false)
	if err != nil {
		return "", nil, fmt.Errorf("Failed to fetch: %s", err)
	}

	if err := buildLib(checkout); err != nil {
		return "", nil, err
	}
	return branchName, res, nil
}