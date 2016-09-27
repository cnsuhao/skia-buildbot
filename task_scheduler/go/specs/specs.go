package specs

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.skia.org/infra/go/gitinfo"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/task_scheduler/go/db"
)

const (
	TASKS_CFG_FILE = "infra/bots/tasks.json"
)

// ParseTasksCfg parses the given task cfg file contents and returns the config.
func ParseTasksCfg(contents string) (*TasksCfg, error) {
	var rv TasksCfg
	if err := json.Unmarshal([]byte(contents), &rv); err != nil {
		return nil, fmt.Errorf("Failed to read tasks cfg: could not parse file: %s", err)
	}

	for _, t := range rv.Tasks {
		if err := t.Validate(&rv); err != nil {
			return nil, err
		}
	}

	if err := findCycles(rv.Tasks, rv.Jobs); err != nil {
		return nil, err
	}

	return &rv, nil
}

// ReadTasksCfg reads the task cfg file from the given repo and returns it.
func ReadTasksCfg(repo *gitinfo.GitInfo, commit string) (*TasksCfg, error) {
	contents, err := repo.GetFile(TASKS_CFG_FILE, commit)
	if err != nil {
		return nil, fmt.Errorf("Failed to read tasks cfg: could not read file: %s", err)
	}
	return ParseTasksCfg(contents)
}

// TasksCfg is a struct which describes all Swarming tasks for a repo at a
// particular commit.
type TasksCfg struct {
	// Jobs is a map whose keys are JobSpec names and values are JobSpecs
	// which describe sets of tasks to run.
	Jobs map[string]*JobSpec `json:"jobs"`

	// Tasks is a map whose keys are TaskSpec names and values are TaskSpecs
	// detailing the Swarming tasks which may be run.
	Tasks map[string]*TaskSpec `json:"tasks"`
}

// TaskSpec is a struct which describes a Swarming task to run.
// Be sure to add any new fields to the Copy() method.
type TaskSpec struct {
	// CipdPackages are CIPD packages which should be installed for the task.
	CipdPackages []*CipdPackage `json:"cipd_packages"`

	// Dependencies are names of other TaskSpecs for tasks which need to run
	// before this task.
	Dependencies []string `json:"dependencies"`

	// Dimensions are Swarming bot dimensions which describe the type of bot
	// which may run this task.
	Dimensions []string `json:"dimensions"`

	// Environment is a set of environment variables needed by the task.
	Environment map[string]string `json:"environment"`

	// ExtraArgs are extra command-line arguments to pass to the task.
	ExtraArgs []string `json:"extra_args"`

	// Isolate is the name of the isolate file used by this task.
	Isolate string `json:"isolate"`

	// Priority indicates the relative priority of the task, with 0 < p <= 1
	Priority float64 `json:"priority"`
}

// Validate ensures that the TaskSpec is defined properly.
func (t *TaskSpec) Validate(cfg *TasksCfg) error {
	// Ensure that CIPD packages are specified properly.
	for _, p := range t.CipdPackages {
		if p.Name == "" || p.Path == "" {
			return fmt.Errorf("CIPD packages must have a name, path, and version.")
		}
	}

	// Ensure that the dimensions are specified properly.
	for _, d := range t.Dimensions {
		split := strings.SplitN(d, ":", 2)
		if len(split) != 2 {
			return fmt.Errorf("Dimension %q does not contain a colon!", d)
		}
	}

	// Isolate file is required.
	if t.Isolate == "" {
		return fmt.Errorf("Isolate file is required.")
	}

	return nil
}

// Copy returns a copy of the TaskSpec.
func (t *TaskSpec) Copy() *TaskSpec {
	var cipdPackages []*CipdPackage
	if len(t.CipdPackages) > 0 {
		cipdPackages = make([]*CipdPackage, 0, len(t.CipdPackages))
		pkgs := make([]CipdPackage, len(t.CipdPackages))
		for i, p := range t.CipdPackages {
			pkgs[i] = *p
			cipdPackages = append(cipdPackages, &pkgs[i])
		}
	}
	deps := util.CopyStringSlice(t.Dependencies)
	dims := util.CopyStringSlice(t.Dimensions)
	environment := util.CopyStringMap(t.Environment)
	extraArgs := util.CopyStringSlice(t.ExtraArgs)
	return &TaskSpec{
		CipdPackages: cipdPackages,
		Dependencies: deps,
		Dimensions:   dims,
		Environment:  environment,
		ExtraArgs:    extraArgs,
		Isolate:      t.Isolate,
		Priority:     t.Priority,
	}
}

// CipdPackage is a struct representing a CIPD package which needs to be
// installed on a bot for a particular task.
type CipdPackage struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// JobSpec is a struct which describes a set of TaskSpecs to run as part of a
// larger effort.
type JobSpec struct {
	Priority  float64  `json:"priority"`
	TaskSpecs []string `json:"tasks"`
}

// Copy returns a copy of the JobSpec.
func (j *JobSpec) Copy() *JobSpec {
	var taskSpecs []string
	if j.TaskSpecs != nil {
		taskSpecs = make([]string, len(j.TaskSpecs))
		copy(taskSpecs, j.TaskSpecs)
	}
	return &JobSpec{
		TaskSpecs: taskSpecs,
	}
}

// TaskCfgCache is a struct used for caching tasks cfg files. The user should
// periodically call Cleanup() to remove old entries.
type TaskCfgCache struct {
	cache           map[db.RepoState]*TasksCfg
	mtx             sync.Mutex
	recentCommits   map[string]time.Time
	recentJobSpecs  map[string]time.Time
	recentMtx       sync.RWMutex
	recentTaskSpecs map[string]time.Time
	repos           *gitinfo.RepoMap
}

// NewTaskCfgCache returns a TaskCfgCache instance.
func NewTaskCfgCache(repos *gitinfo.RepoMap) *TaskCfgCache {
	return &TaskCfgCache{
		cache:           map[db.RepoState]*TasksCfg{},
		mtx:             sync.Mutex{},
		recentCommits:   map[string]time.Time{},
		recentJobSpecs:  map[string]time.Time{},
		recentMtx:       sync.RWMutex{},
		recentTaskSpecs: map[string]time.Time{},
		repos:           repos,
	}
}

// readTasksCfg reads the task cfg file from the given repo and returns it.
// Stores a cache of already-read task cfg files. Syncs the repo and reads the
// file if needed. Assumes the caller holds c.mtx.
// TODO(borenet): Make readTasksCfg apply patches as needed.
func (c *TaskCfgCache) readTasksCfg(rs db.RepoState) (*TasksCfg, error) {
	rv, ok := c.cache[rs]
	if ok {
		return rv, nil
	}

	// We haven't seen this RepoState before, or it's scrolled our of our
	// window. Read it.
	r, err := c.repos.Repo(rs.Repo)
	if err != nil {
		return nil, fmt.Errorf("Could not read task cfg; failed to check out repo: %s", err)
	}
	cfg, err := ReadTasksCfg(r, rs.Revision)
	if err != nil {
		// The tasks.cfg file may not exist for a particular commit.
		if strings.Contains(err.Error(), "does not exist in") || strings.Contains(err.Error(), "exists on disk, but not in") {
			// In this case, use an empty config.
			cfg = &TasksCfg{
				Tasks: map[string]*TaskSpec{},
			}
		} else {
			return nil, err
		}
	}
	c.recentMtx.Lock()
	defer c.recentMtx.Unlock()
	c.cache[rs] = cfg
	d, err := r.Details(rs.Revision, false)
	if err != nil {
		return nil, err
	}
	ts := d.Timestamp
	if ts.After(c.recentCommits[rs.Revision]) {
		c.recentCommits[rs.Revision] = ts
	}
	for name, _ := range cfg.Tasks {
		if ts.After(c.recentTaskSpecs[name]) {
			c.recentTaskSpecs[name] = ts
		}
	}
	for name, _ := range cfg.Jobs {
		if ts.After(c.recentJobSpecs[name]) {
			c.recentJobSpecs[name] = ts
		}
	}
	return cfg, nil
}

// ReadTasksCfg reads the task cfg file from the given RepoState and returns it.
// Stores a cache of already-read task cfg files. Syncs the repo and reads the
// file if needed.
func (c *TaskCfgCache) ReadTasksCfg(rs db.RepoState) (*TasksCfg, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	return c.readTasksCfg(rs)
}

// GetTaskSpecsForRepoStates returns a set of TaskSpecs for each of the
// given set of RepoStates, keyed by RepoState and TaskSpec name.
func (c *TaskCfgCache) GetTaskSpecsForRepoStates(rs []db.RepoState) (map[db.RepoState]map[string]*TaskSpec, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	rv := map[db.RepoState]map[string]*TaskSpec{}
	for _, s := range rs {
		cfg, err := c.readTasksCfg(s)
		if err != nil {
			return nil, err
		}
		// Make a copy of the task specs.
		subMap := make(map[string]*TaskSpec, len(cfg.Tasks))
		for name, task := range cfg.Tasks {
			subMap[name] = task.Copy()
		}
		rv[s] = subMap
	}
	return rv, nil
}

// GetTaskSpec returns the TaskSpec at the given RepoState, or an error if no
// such TaskSpec exists.
func (c *TaskCfgCache) GetTaskSpec(rs db.RepoState, name string) (*TaskSpec, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	cfg, err := c.readTasksCfg(rs)
	if err != nil {
		return nil, err
	}
	t, ok := cfg.Tasks[name]
	if !ok {
		return nil, fmt.Errorf("No such task spec: %s @ %s", name, rs)
	}
	return t.Copy(), nil
}

// GetJobSpec returns the JobSpec at the given RepoState, or an error if no such
// JobSpec exists.
func (c *TaskCfgCache) GetJobSpec(rs db.RepoState, name string) (*JobSpec, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	cfg, err := c.readTasksCfg(rs)
	if err != nil {
		return nil, err
	}
	j, ok := cfg.Jobs[name]
	if !ok {
		return nil, fmt.Errorf("No such job spec: %s @ %s", name, rs)
	}
	return j.Copy(), nil
}

// Cleanup removes cache entries which are outside of our scheduling window.
func (c *TaskCfgCache) Cleanup(period time.Duration) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	periodStart := time.Now().Add(-period)
	for repoState, _ := range c.cache {
		repo, err := c.repos.Repo(repoState.Repo)
		if err != nil {
			return err
		}
		details, err := repo.Details(repoState.Revision, false)
		if err != nil {
			return err
		}
		if details.Timestamp.Before(periodStart) {
			delete(c.cache, repoState)
		}
	}
	c.recentMtx.Lock()
	defer c.recentMtx.Unlock()
	for k, ts := range c.recentCommits {
		if ts.Before(periodStart) {
			delete(c.recentCommits, k)
		}
	}
	for k, ts := range c.recentTaskSpecs {
		if ts.Before(periodStart) {
			delete(c.recentTaskSpecs, k)
		}
	}
	for k, ts := range c.recentJobSpecs {
		if ts.Before(periodStart) {
			delete(c.recentJobSpecs, k)
		}
	}
	return nil
}

func stringMapKeys(m map[string]time.Time) []string {
	rv := make([]string, 0, len(m))
	for k, _ := range m {
		rv = append(rv, k)
	}
	return rv
}

// RecentSpecsAndCommits returns lists of recent job and task spec names and
// commit hashes.
func (c *TaskCfgCache) RecentSpecsAndCommits() ([]string, []string, []string) {
	c.recentMtx.RLock()
	defer c.recentMtx.RUnlock()
	return stringMapKeys(c.recentJobSpecs), stringMapKeys(c.recentTaskSpecs), stringMapKeys(c.recentCommits)
}

// findCycles searches for cyclical dependencies in the task specs and returns
// an error if any are found. Also ensures that all task specs are reachable
// from at least one job spec and that all jobs specs' dependencies are valid.
func findCycles(tasks map[string]*TaskSpec, jobs map[string]*JobSpec) error {
	// Create vertex objects with metadata for the depth-first search.
	type vertex struct {
		active  bool
		name    string
		ts      *TaskSpec
		visited bool
	}
	vertices := make(map[string]*vertex, len(tasks))
	for name, t := range tasks {
		vertices[name] = &vertex{
			active:  false,
			name:    name,
			ts:      t,
			visited: false,
		}
	}

	// visit performs a depth-first search of the graph, starting at v.
	var visit func(*vertex) error
	visit = func(v *vertex) error {
		v.active = true
		v.visited = true
		for _, dep := range v.ts.Dependencies {
			e := vertices[dep]
			if e == nil {
				return fmt.Errorf("Task %q has unknown task %q as a dependency.", v.name, dep)
			}
			if !e.visited {
				if err := visit(e); err != nil {
					return err
				}
			} else if e.active {
				return fmt.Errorf("Found a circular dependency involving %q and %q", v.name, e.name)
			}
		}
		v.active = false
		return nil
	}

	// Perform a DFS, starting at each of the jobs' dependencies.
	for jobName, j := range jobs {
		for _, d := range j.TaskSpecs {
			v, ok := vertices[d]
			if !ok {
				return fmt.Errorf("Job %q has unknown task %q as a dependency.", jobName, d)
			}
			if !v.visited {
				if err := visit(v); err != nil {
					return err
				}
			}
		}
	}

	// If any vertices have not been visited, then there are tasks which
	// no job has as a dependency. Report an error.
	for _, v := range vertices {
		if !v.visited {
			return fmt.Errorf("Task %q is not reachable by any Job!", v.name)
		}
	}
	return nil
}