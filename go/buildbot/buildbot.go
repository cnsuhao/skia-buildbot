package buildbot

/*
	Tools for loading data from Buildbot's JSON interface.
*/

const (
	BUILDBOT_URL  = "http://build.chromium.org/p/"
	LOAD_ATTEMPTS = 3
)

// BuildStep contains information about a build step.
type BuildStep struct {
	BuilderName string `db:"builder"`
	MasterName  string `db:"master"`
	BuildNumber int    `db:"buildNumber"`
	Name        string `db:"name"`
	Times       []float64
	Number      int           `json:"step_number" db:"number"`
	Results     int           `db:"results"`
	ResultsRaw  []interface{} `json:"results"`
	Started     float64       `db:"started"`
	Finished    float64       `db:"finished"`
}

// Build contains information about a single build.
type Build struct {
	BuilderName   string `db:"builder"`
	BuildSlave    string `db:"buildslave"`
	Branch        string `db:"branch"`
	Commits       []string
	GotRevision   string          `db:"gotRevision"`
	MasterName    string          `db:"master"`
	Number        int             `db:"number"`
	Properties    [][]interface{} `db:"_"`
	PropertiesStr string          `db:"properties"`
	Results       int             `db:"results"`
	Steps         []*BuildStep
	Times         []float64
	Started       float64 `db:"started"`
	Finished      float64 `db:"finished"`
}

// GetProperty returns the key/value pair for a build property, if it exists,
// and nil otherwise.
func (b Build) GetProperty(property string) interface{} {
	for _, propVal := range b.Properties {
		if propVal[0].(string) == property {
			return propVal
		}
	}
	return nil
}

// GotRevision returns the revision to which a build was synced, or the empty
// string if none.
func (b Build) gotRevision() string {
	gotRevision := b.GetProperty("got_revision")
	if gotRevision == nil {
		return ""
	}
	if gotRevision.([]interface{})[1] == nil {
		return ""
	}
	return gotRevision.([]interface{})[1].(string)
}

// Branch returns the branch whose commit(s) triggered this build.
func (b Build) branch() string {
	branch := b.GetProperty("branch")
	if branch == nil {
		return ""
	}
	if branch.([]interface{})[1] == nil {
		return ""
	}
	return branch.([]interface{})[1].(string)
}

// Finished indicates whether the build has finished.
func (b Build) IsFinished() bool {
	return b.Finished != 0.0
}
