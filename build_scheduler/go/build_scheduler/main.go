package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"runtime"

	"github.com/gorilla/mux"
	"github.com/skia-dev/glog"
	"go.skia.org/infra/build_scheduler/go/blacklist"
	"go.skia.org/infra/build_scheduler/go/bot_map"
	"go.skia.org/infra/build_scheduler/go/build_queue"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/buildbot"
	"go.skia.org/infra/go/buildbucket"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/git/repograph"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/human"
	"go.skia.org/infra/go/influxdb"
	"go.skia.org/infra/go/login"
	"go.skia.org/infra/go/skiaversion"
	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/util"
)

const (
	// APP_NAME is the name of this app.
	APP_NAME = "buildbot_scheduler"
)

var (
	// "Constants"

	// MASTERS determines which masters we poll for builders.
	MASTERS = []string{
		"client.skia",
		"client.skia.android",
		"client.skia.compile",
		"client.skia.fyi",
		//"client.skia.internal",
	}

	// REPOS are the repositories to query.
	REPOS = []string{
		common.REPO_SKIA,
		common.REPO_SKIA_INFRA,
	}

	// HTML templates.
	blacklistTemplate *template.Template = nil
	mainTemplate      *template.Template = nil
	triggerTemplate   *template.Template = nil

	// Build Scheduler instance.
	bs *BuildScheduler

	// Git repo objects.
	repos repograph.Map

	// Flags.
	buildbotDbHost = flag.String("buildbot_db_host", "skia-datahopper2:8000", "Where the Skia buildbot database is hosted.")
	host           = flag.String("host", "localhost", "HTTP service host")
	port           = flag.String("port", ":8000", "HTTP service port (e.g., ':8000')")
	useMetadata    = flag.Bool("use_metadata", true, "Load sensitive values from metadata not from flags.")
	local          = flag.Bool("local", false, "Whether we're running on a dev machine vs in production.")
	resourcesDir   = flag.String("resources_dir", "", "The directory to find templates, JS, and CSS files. If blank the current directory will be used.")
	scoreDecay24Hr = flag.Float64("scoreDecay24Hr", 0.9, "Build candidate scores are penalized using exponential time decay, starting at 1.0. This is the desired value after 24 hours. Setting it to 1.0 causes commits not to be prioritized according to commit time.")
	scoreThreshold = flag.Float64("scoreThreshold", build_queue.DEFAULT_SCORE_THRESHOLD, "Don't schedule builds with scores below this threshold.")
	timePeriod     = flag.String("timePeriod", "4d", "Time period to use.")
	workdir        = flag.String("workdir", "workdir", "Working directory to use.")

	influxHost     = flag.String("influxdb_host", influxdb.DEFAULT_HOST, "The InfluxDB hostname.")
	influxUser     = flag.String("influxdb_name", influxdb.DEFAULT_USER, "The InfluxDB username.")
	influxPassword = flag.String("influxdb_password", influxdb.DEFAULT_PASSWORD, "The InfluxDB password.")
	influxDatabase = flag.String("influxdb_database", influxdb.DEFAULT_DATABASE, "The InfluxDB database.")
)

func reloadTemplates() {
	// Change the current working directory to two directories up from this source file so that we
	// can read templates and serve static (res/) files.
	if *resourcesDir == "" {
		_, filename, _, _ := runtime.Caller(0)
		*resourcesDir = filepath.Join(filepath.Dir(filename), "../..")
	}
	blacklistTemplate = template.Must(template.ParseFiles(
		filepath.Join(*resourcesDir, "templates/blacklist.html"),
		filepath.Join(*resourcesDir, "templates/header.html"),
		filepath.Join(*resourcesDir, "templates/footer.html"),
	))
	mainTemplate = template.Must(template.ParseFiles(
		filepath.Join(*resourcesDir, "templates/main.html"),
		filepath.Join(*resourcesDir, "templates/header.html"),
		filepath.Join(*resourcesDir, "templates/footer.html"),
	))
	triggerTemplate = template.Must(template.ParseFiles(
		filepath.Join(*resourcesDir, "templates/trigger.html"),
		filepath.Join(*resourcesDir, "templates/header.html"),
		filepath.Join(*resourcesDir, "templates/footer.html"),
	))
}

func Init() {
	reloadTemplates()
}

func blacklistHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Don't use cached templates in testing mode.
	if *local {
		reloadTemplates()
	}
	page := struct {
		Blacklist *blacklist.Blacklist
		Builders  []string
		Commits   []string
	}{
		Blacklist: bs.GetBlacklist(),
		Builders:  bs.Builders(),
		Commits:   bs.Commits(),
	}
	if err := blacklistTemplate.Execute(w, page); err != nil {
		httputils.ReportError(w, r, err, "Failed to execute template.")
		return
	}
}

func mainHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Don't use cached templates in testing mode.
	if *local {
		reloadTemplates()
	}
	if err := mainTemplate.Execute(w, bs.Status()); err != nil {
		httputils.ReportError(w, r, err, "Failed to execute template.")
		return
	}
}

func triggerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	// Don't use cached templates in testing mode.
	if *local {
		reloadTemplates()
	}
	page := struct {
		Builders []string
		Commits  []string
	}{
		Builders: bs.Builders(),
		Commits:  bs.Commits(),
	}
	if err := triggerTemplate.Execute(w, page); err != nil {
		httputils.ReportError(w, r, err, "Failed to execute template.")
		return
	}
}

func jsonBlacklistHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !login.IsGoogler(r) {
		errStr := "Cannot modify the blacklist; user is not a logged-in Googler."
		httputils.ReportError(w, r, fmt.Errorf(errStr), errStr)
		return
	}

	if r.Method == http.MethodDelete {
		var msg struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			httputils.ReportError(w, r, err, fmt.Sprintf("Failed to decode request body: %s", err))
			return
		}
		defer util.Close(r.Body)
		if err := bs.GetBlacklist().RemoveRule(msg.Name); err != nil {
			httputils.ReportError(w, r, err, fmt.Sprintf("Failed to delete blacklist rule: %s", err))
			return
		}
	} else if r.Method == http.MethodPost {
		var rule blacklist.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			httputils.ReportError(w, r, err, fmt.Sprintf("Failed to decode request body: %s", err))
			return
		}
		defer util.Close(r.Body)
		rule.AddedBy = login.LoggedInAs(r)
		if len(rule.Commits) == 2 {
			rangeRule, err := blacklist.NewCommitRangeRule(rule.Name, rule.AddedBy, rule.Description, rule.BuilderPatterns, rule.Commits[0], rule.Commits[1], repos)
			if err != nil {
				httputils.ReportError(w, r, err, fmt.Sprintf("Failed to create commit range rule: %s", err))
				return
			}
			rule = *rangeRule
		}
		if err := bs.GetBlacklist().AddRule(&rule, repos); err != nil {
			httputils.ReportError(w, r, err, fmt.Sprintf("Failed to add blacklist rule: %s", err))
			return
		}
	}
	if err := json.NewEncoder(w).Encode(bs.GetBlacklist()); err != nil {
		httputils.ReportError(w, r, err, fmt.Sprintf("Failed to encode response: %s", err))
		return
	}
}

func jsonTriggerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !login.IsGoogler(r) {
		errStr := "Cannot trigger builds; user is not a logged-in Googler."
		httputils.ReportError(w, r, fmt.Errorf(errStr), errStr)
		return
	}

	var msg struct {
		Builders []string `json:"builders"`
		Commit   string   `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		httputils.ReportError(w, r, err, fmt.Sprintf("Failed to decode request body: %s", err))
		return
	}
	defer util.Close(r.Body)
	triggered, err := bs.Trigger(msg.Builders, msg.Commit)
	if err != nil {
		httputils.ReportError(w, r, err, "Failed to trigger builds.")
		return
	}
	// Return info about the triggered builds.
	for _, b := range triggered {
		glog.Infof("    %s", b.Id)
	}

	if err := json.NewEncoder(w).Encode(triggered); err != nil {
		httputils.ReportError(w, r, err, "Failed to encode response.")
		return
	}
}

func runServer(serverURL string) {
	r := mux.NewRouter()
	r.HandleFunc("/", mainHandler)
	r.HandleFunc("/blacklist", blacklistHandler)
	r.HandleFunc("/trigger", triggerHandler)
	r.HandleFunc("/json/blacklist", jsonBlacklistHandler).Methods(http.MethodPost, http.MethodDelete)
	r.HandleFunc("/json/trigger", jsonTriggerHandler).Methods(http.MethodPost)
	r.HandleFunc("/json/version", skiaversion.JsonHandler)
	r.PathPrefix("/res/").HandlerFunc(httputils.MakeResourceHandler(*resourcesDir))

	r.HandleFunc("/logout/", login.LogoutHandler)
	r.HandleFunc("/loginstatus/", login.StatusHandler)
	r.HandleFunc("/oauth2callback/", login.OAuth2CallbackHandler)

	http.Handle("/", httputils.LoggingGzipRequestResponse(r))
	glog.Infof("Ready to serve on %s", serverURL)
	glog.Fatal(http.ListenAndServe(*port, nil))
}

func main() {
	defer common.LogPanic()

	// Global init.
	common.InitWithMetrics2(APP_NAME, influxHost, influxUser, influxPassword, influxDatabase, local)

	Init()

	v, err := skiaversion.GetVersion()
	if err != nil {
		glog.Fatal(err)
	}
	glog.Infof("Version %s, built at %s", v.Commit, v.Date)

	// Parse the time period.
	period, err := human.ParseDuration(*timePeriod)
	if err != nil {
		glog.Fatal(err)
	}

	// Initialize the buildbot database.
	db, err := buildbot.NewRemoteDB(*buildbotDbHost)
	if err != nil {
		glog.Fatal(err)
	}

	// Initialize the BuildBucket client.
	c, err := auth.NewClient(*local, path.Join(*workdir, "oauth_token_cache"), buildbucket.DEFAULT_SCOPES...)
	if err != nil {
		glog.Fatal(err)
	}
	bb := buildbucket.NewClient(c)

	// Initialize Swarming client, start BotMap metrics.
	swarm, err := swarming.NewApiClient(c)
	if err != nil {
		glog.Fatal(err)
	}
	bot_map.StartMetrics(swarm)

	serverURL := "https://" + *host
	if *local {
		serverURL = "http://" + *host + *port
	}

	var redirectURL = serverURL + "/oauth2callback/"
	if err := login.InitFromMetadataOrJSON(redirectURL, login.DEFAULT_SCOPE, login.DEFAULT_DOMAIN_WHITELIST); err != nil {
		glog.Fatal(err)
	}

	repos, err = repograph.NewMap(REPOS, *workdir)
	if err != nil {
		glog.Fatal(err)
	}
	if err := repos.Update(); err != nil {
		glog.Fatal(err)
	}

	bs = StartNewBuildScheduler(period, *scoreThreshold, *scoreDecay24Hr, db, bb, repos, *workdir, *local)

	runServer(serverURL)
}
