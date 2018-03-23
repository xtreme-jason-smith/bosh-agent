package lgpo

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	boshlog "github.com/cloudfoundry/bosh-utils/logger"
	"github.com/prometheus/common/log"
)

const (
	logTag         = "LGPO"
	PolicyLockFile = `C:\bosh\applied_policy.txt`
	RepoURL        = "https://github.com/greenhouse-org/lgpo-test.git"
)

func ApplyPolicy(log boshlog.Logger) error {
	start := time.Now()
	log.Debug(logTag, "STARTING")

	// Check for lock file

	if _, err := os.Stat(PolicyLockFile); err == nil {
		log.Debug(logTag, "Found PolicyLockFile (%s) - policies are already applied", PolicyLockFile)
		return nil
	}

	// Download and apply policies

	tmpdir, err := ioutil.TempDir("", "LGPO-")
	if err != nil {
		log.Error(logTag, "Error (TempDir): %s", err)
		return err
	}

	if err := WaitForNetwork(log); err != nil {
		log.Error(logTag, "Error (WaitForNetwork): %s", err)
		return err
	}

	repoDir, err := CloneRepo(RepoURL, tmpdir, log)
	if err != nil {
		log.Error(logTag, "Error (CloneRepo): %s", err)
		return err
	}

	policyPath := filepath.Join(repoDir, "policies")
	if _, err := os.Stat(policyPath); err != nil {
		log.Error(logTag, "ApplyPolicy: invalid POLICY PATH (%s): %s", policyPath, err)
		return err
	}

	log.Debug(logTag, "ApplyPolicy: running RunLGPO...")
	if err := RunLGPO(policyPath, log); err != nil {
		log.Error(logTag, "Error (RunLGPO): %s", err)
		return err
	}

	log.Debug(logTag, "ApplyPolicy: creating lock file")
	if err := CreatePolicyLockFile(log); err != nil {
		log.Error(logTag, "Error (CreatePolicyLockFile): %s", err)
		return err
	}

	log.Debug(logTag, "SUCCESSFULLY APPLIED POLICIES: %s", time.Since(start))

	log.Debug(logTag, "ApplyPolicy: restarting computer")
	if err := RestartComputer(log); err != nil {
		log.Error(logTag, "Error (RestartComputer): %s", err)
		return err
	}

	return nil
}

func WaitForNetwork(log boshlog.Logger) error {
	const RetryCount = 6
	const URL = "http://www.example.com/"

	start := time.Now()
	client := http.Client{
		Timeout: time.Second * 20,
	}
	for i := 0; i < RetryCount; i++ {
		log.Error(logTag, "WaitForNetwork attempt: #%d", i)
		_, err := client.Get(URL)
		if err == nil {
			log.Error(logTag, "WaitForNetwork success")
			return nil
		}
		log.Error(logTag, "WaitForNetwork attempt #%d: %s", i, err)
		time.Sleep(time.Second * 15)
	}
	return fmt.Errorf("WaitForNetwork: failed to access (%s) after #%d attempts and %s",
		URL, RetryCount, time.Since(start))
}

func FindGitExe() (string, error) {
	if s, err := exec.LookPath("git.exe"); err == nil {
		return s, nil
	}
	paths := []string{
		`C:\Git\cmd\git.exe`,
		`C:\Program Files\Git\cmd\git.exe`,
		`C:\Program Files (x86)\Git\cmd\git.exe`,
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return path, nil
		}
	}
	return "", errors.New("git.exe not found")
}

func CloneRepo(repoURL, dirname string, log boshlog.Logger) (string, error) {
	if err := os.MkdirAll(dirname, 0744); err != nil {
		return "", err
	}

	// Clone the repo

	// just in case git.exe is not on the system path, search
	// common install locations
	gitexe, err := FindGitExe()
	if err != nil {
		return "", err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command(gitexe, "clone", repoURL)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = dirname

	log.Debug(logTag, "Cloning repo (%s) with command: %+v", repoURL, cmd)
	if err := cmd.Run(); err != nil {
		log.Error(logTag, "Running command %v: %s\n"+
			"#### STDERR START ####\n"+
			"%s\n"+
			"#### STDERR END ####",
			append([]string{cmd.Path}, cmd.Args...), err, stderr.String())
		return "", err
	}
	log.Debug(logTag, "Successfully ran command: %v\n"+
		"#### STDOUT START ####\n"+
		"%s\n"+
		"#### STDOUT END ####",
		append([]string{cmd.Path}, cmd.Args...), stdout.String())

	// Find the directory created by 'git clone'

	list, err := ioutil.ReadDir(dirname)
	if err != nil {
		return "", err
	}

	// Expect 'git clone' to create only 1 directory
	if len(list) != 1 {
		names := make([]string, len(list))
		for i, fi := range list {
			names[i] = fi.Name()
		}
		log.Error(logTag, "Clone Repo: expected to find 1 file found (%d): %v", len(list), names)
		return "", errors.New("Clone Repo: cloning created too many directories")
	}
	if !list[0].IsDir() {
		log.Error(logTag, "Clone Repo: git created a file but it is not a directory: %v", list[0].Name())
		return "", errors.New("Clone Repo: expected directory")
	}

	path := filepath.Join(dirname, list[0].Name())
	return path, nil
}

func RunCommand(cmd *exec.Cmd) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Debug(logTag, "Running command: %+v", cmd)
	if err := cmd.Run(); err != nil {
		log.Error(logTag, "Running command %v: %s\n"+
			"#### STDERR START ####\n"+
			"%s\n"+
			"#### STDERR END ####",
			append([]string{cmd.Path}, cmd.Args...), err, stderr.String())
		return err
	}

	log.Debug(logTag, "Successfully ran command: %v\n"+
		"#### STDOUT START ####\n"+
		"%s\n"+
		"#### STDOUT END ####",
		append([]string{cmd.Path}, cmd.Args...), stdout.String())

	return nil
}

func RunLGPO(dirname string, log boshlog.Logger) error {
	const BackupPath = `C:\Windows\LGPO.exe`

	path, err := exec.LookPath("LGPO.exe")
	if err != nil {
		log.Error(logTag, "RunLGPO: failed to lookup LGPO.exe - falling back to (%s)", BackupPath)
		path = BackupPath
	}

	// Machine
	{
		log.Debug(logTag, "RunLGPO: creating MACHINE policy")

		machineDir := filepath.Join(dirname, "DomainSysvol", "GPO", "Machine")
		if _, err := os.Stat(machineDir); err != nil {
			log.Error(logTag, "RunLGPO missing MACHINE file (%s): %s", machineDir, err)
			return err
		}

		cmd := exec.Command(path,
			"/r", filepath.Join(machineDir, "registry.txt"),
			"/w", filepath.Join(machineDir, "registry.pol"))
		if err := RunCommand(cmd); err != nil {
			log.Error(logTag, "RunLGPO: ERROR creating MACHINE policy", err)
			return err
		}
	}

	// User
	{
		log.Debug(logTag, "RunLGPO: creating USER policy")

		userDir := filepath.Join(dirname, "DomainSysvol", "GPO", "User")
		if _, err := os.Stat(userDir); err != nil {
			log.Error(logTag, "RunLGPO missing USER file (%s): %s", userDir, err)
			return err
		}

		cmd := exec.Command(path,
			"/r", filepath.Join(userDir, "registry.txt"),
			"/w", filepath.Join(userDir, "registry.pol"))
		if err := RunCommand(cmd); err != nil {
			log.Error(logTag, "RunLGPO: ERROR creating USER policy", err)
			return err
		}
	}

	{
		log.Debug(logTag, "RunLGPO: applying policy: %s", dirname)
		cmd := exec.Command(path, "/g", dirname)
		if err := RunCommand(cmd); err != nil {
			log.Error(logTag, "RunLGPO: ERROR applying policy", err)
			return err
		}
	}

	return nil
}

func CreatePolicyLockFile(log boshlog.Logger) error {
	if _, err := os.Stat(PolicyLockFile); err == nil {
		return nil
	}
	log.Debug(logTag, "Creating PolicyLockFile: %s", PolicyLockFile)
	f, err := os.Create(PolicyLockFile)
	if err != nil {
		log.Error(logTag, "Creating PolicyLockFile (%s): %s", PolicyLockFile, err)
		return errors.New("PolicyLockFile: " + err.Error())
	}
	f.Close()
	log.Debug(logTag, "Successfully created PolicyLockFile: %s", PolicyLockFile)
	return nil
}

func RestartComputer(log boshlog.Logger) error {
	if err := CreatePolicyLockFile(log); err != nil {
		return err
	}

	cmd := exec.Command("shutdown.exe", "/r", "/f", "/t", "5")
	log.Debug(logTag, "Restarting computer with command: %+v", cmd)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error(logTag, "Running command %v: %s\n"+
			"#### OUTPUT START ####\n"+
			"%s\n"+
			"#### OUTPUT END ####",
			append([]string{cmd.Path}, cmd.Args...), err, string(out))
	}
	log.Debug(logTag, "Successfully initiated restart")

	start := time.Now()
	tick := time.NewTicker(time.Second)
	timeout := time.After(time.Minute)

	for {
		select {
		case <-tick.C:
			log.Debug(logTag, "Waiting for shutdown: %s", time.Since(start))
		case <-timeout:
			log.Error(logTag, "Timed out waiting for shutdown!!!: %s", time.Since(start))
			return errors.New("Timed out waiting for shutdown")
		}
	}

	return nil
}

/*
func downloadFile(url, filename string, log boshlog.Logger) error {
	log.Debug(logTag, "Preparing to download %s to %s", url, filename)
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if _, err := io.Copy(f, res.Body); err != nil {
		return err
	}
	log.Debug(logTag, "Successfully downloaded %s to %s", url, filename)
	return nil
}
func DownloadPolicy(downloadURL string, log boshlog.Logger) (string, error) {
	const RetryCount = 6
	tmpdir, err := ioutil.TempDir("", "lgpo-")
	if err != nil {
		return "", err
	}
	filename := filepath.Join(tmpdir, "policy.tgz")
	for i := 0; i < RetryCount; i++ {
		// reuse error variable
		err = downloadFile(downloadURL, filename, log)
		if err == nil {
			break
		}
		log.Error(logTag, "DownloadPolicy attempt #%d: %s", i, err)
		time.Sleep(time.Second * 15)
	}
	if err != nil {
		log.Error(logTag, "DownloadPolicy FAILED after %d attempts", RetryCount)
	}
	// Extract archive
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.Command("tar.exe", "xzvf", filename)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = tmpdir
	log.Debug(logTag, "Extracting policy archive (%s) with command: %+v", filename, cmd)
	if err := cmd.Run(); err != nil {
		log.Error(logTag, "Running command %v: %s\n"+
			"#### STDERR START ####\n"+
			"%s\n"+
			"#### STDERR END ####",
			append([]string{cmd.Path}, cmd.Args...), err, stderr.String())
		return "", err
	}
	log.Debug(logTag, "Successfully ran command: %v\n"+
		"#### STDOUT START ####\n"+
		"%s\n"+
		"#### STDOUT END ####",
		append([]string{cmd.Path}, cmd.Args...), stdout.String())
	// find the file that is not policy.tgz
	names, err := ioutil.ReadDir(tmpdir)
	if err != nil {
		return "", err
	}
	_ = names
	return "", nil
}
*/