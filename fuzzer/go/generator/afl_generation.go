package generator

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/skia-dev/glog"
	"go.skia.org/infra/fuzzer/go/common"
	"go.skia.org/infra/fuzzer/go/config"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/fileutil"
	"go.skia.org/infra/go/gs"
	storage "google.golang.org/api/storage/v1"
)

var fuzzCounter *int32

// StartBinaryGenerator starts up 1 goroutine running a "master" afl-fuzz and n-1 "slave"
// afl-fuzz processes, where n is specified by config.Generator.NumFuzzProcesses.
// Output goes to config.Generator.AflOutputPath
func StartBinaryGenerator() error {
	executable, err := setup()
	if err != nil {
		return fmt.Errorf("Failed binary generator setup: %s", err)
	}

	masterCmd := &exec.Command{
		Name: "./afl-fuzz",
		Args: []string{"-i", config.Generator.FuzzSamples, "-o", config.Generator.AflOutputPath, "-m", "5000", "-t", "3000", "-M", "fuzzer0", "--", executable, "--src", "skp", "--skps", "@@", "--config", "8888"},
		Dir:  config.Generator.AflRoot,
		Env:  []string{"AFL_SKIP_CPUFREQ=true"}, // Avoids a warning afl-fuzz spits out about dynamic scaling of cpu frequency
	}
	if config.Generator.WatchAFL {
		masterCmd.Stdout = os.Stdout
	}

	go run(masterCmd)

	numFuzzers := int32(config.Generator.NumFuzzProcesses)
	if numFuzzers <= 0 {
		// TODO(kjlubick): Make this actually an intelligent number based on the number of cores.
		numFuzzers = 10
	}

	fuzzCounter = &numFuzzers

	for i := int32(1); i < numFuzzers; i++ {
		fuzzerName := fmt.Sprintf("fuzzer%d", i)
		slaveCmd := &exec.Command{
			Name: "./afl-fuzz",
			Args: []string{"-i", config.Generator.FuzzSamples, "-o", config.Generator.AflOutputPath, "-m", "5000", "-t", "3000", "-S", fuzzerName, "--", executable, "--src", "skp", "--skps", "@@", "--config", "8888"},
			Dir:  config.Generator.AflRoot,
			Env:  []string{"AFL_SKIP_CPUFREQ=true"}, // Avoids a warning afl-fuzz spits out about dynamic scaling of cpu frequency
		}
		go run(slaveCmd)
	}

	return nil
}

// setup clears out previous fuzzing sessions and builds the executable we need to run afl-fuzz.
// The binary is then copied to the working directory as "dm_afl_Release".
func setup() (string, error) {
	// remove previous binaries
	if err := os.RemoveAll(config.Generator.WorkingPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("Failed to remove previous binaries: %s", err)
	}
	if err := os.MkdirAll(config.Generator.WorkingPath, 0755); err != nil {
		return "", fmt.Errorf("Failed to create working directory: %s", err)
	}

	// remove previous fuzz results
	if err := os.RemoveAll(config.Generator.AflOutputPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("Failed to remove previous fuzz results: %s", err)
	}
	if err := os.MkdirAll(config.Generator.AflOutputPath, 0755); err != nil {
		return "", fmt.Errorf("Failed to create fuzz results directory: %s", err)
	}

	// build afl
	if err := common.BuildAflDM("Release"); err != nil {
		return "", fmt.Errorf("Failed to build dm using afl-fuzz %s", err)
	}
	// copy to working directory
	executable := filepath.Join(config.Generator.WorkingPath, common.TEST_HARNESS_NAME+"_afl_Release")
	if err := fileutil.CopyExecutable(filepath.Join(config.Generator.SkiaRoot, "out", "Release", common.TEST_HARNESS_NAME), executable); err != nil {
		return "", err
	}
	return executable, nil
}

// run Runs the command and logs any failures.  Meant to be run as a goroutine.
func run(command *exec.Command) {
	if err := exec.Run(command); err != nil {
		glog.Errorf("Failed afl fuzzer command %#v: %s", command, err)
	}
	// TODO(kjlubick): Keep track of this number via metrics so we can use
	// mon.skia.org and write alerts for it.
	atomic.AddInt32(fuzzCounter, -1)
	glog.Infof("afl fuzzer with args %q ended.  There are %d fuzzers remaining", command.Args, *fuzzCounter)
}

// DownloadBinarySeedFiles downloads the seed skp files stored in Google
// Storage to be used by afl-fuzz.  It places them in
// config.Generator.FuzzSamples after cleaning the folder out.
// It returns an error on failure.
func DownloadBinarySeedFiles(storageService *storage.Service) error {
	if err := os.RemoveAll(config.Generator.FuzzSamples); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Could not clean binary seed path %s: %s", config.Generator.FuzzSamples, err)
	}
	if err := os.MkdirAll(config.Generator.FuzzSamples, 0755); err != nil {
		return fmt.Errorf("Could not create binary seed path %s: %s", config.Generator.FuzzSamples, err)
	}

	samples, err := storageService.Objects.List(config.GS.Bucket).Prefix("skp_samples").Do()
	if err != nil {
		return fmt.Errorf("Problem reading from Google Storage: %v", err)
	}
	glog.Infof("Downloading %d seed skp files from Google Storage", len(samples.Items))

	for _, item := range samples.Items {
		name := item.Name
		// skip the parent folder
		if name == "skp_samples/" {
			continue
		}
		content, err := gs.FileContentsFromGS(storageService, config.GS.Bucket, name)
		if err != nil {
			glog.Errorf("Problem downloading %s from Google Storage, continuing anyway", item.Name)
			continue
		}
		fileName := filepath.Join(config.Generator.FuzzSamples, strings.TrimLeft(name, "skp_samples/"))
		if err = ioutil.WriteFile(fileName, content, 0644); err != nil {
			glog.Errorf("Problem creating binary seed file %s, continuing anyway", fileName)
		}
	}

	return nil
}