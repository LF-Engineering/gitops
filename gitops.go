package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	jsoniter "github.com/json-iterator/go"
)

type gitOps struct {
	basePath        string
	cachePath       string
	upToDate        bool
	errored         bool
	followHierarchy bool
	cache           map[string]interface{}
	gitURL          string
	cacheFileName   string
}

func (g *gitOps) init(url string) {
	g.basePath = os.Getenv("DA_GIT_REPOS_PATH")
	if g.basePath == "" {
		g.basePath = os.Getenv("HOME") + "/.perceval/repositories"
	}
	g.upToDate = false
	g.errored = false
	g.followHierarchy = false
	g.cache = make(map[string]interface{})
	g.gitURL = g.getProcessedURI(url)
	g.cacheFileName = "stats.json"
}

func (g *gitOps) getProcessedURI(uri string) string {
	removal := ".git"
	if strings.HasSuffix(uri, removal) {
		return uri[:len(uri)-4]
	}
	return uri
}

func (g *gitOps) cachePathFunc() string {
	g.cachePath = os.Getenv("DA_GIT_CACHE_PATH")
	if g.cachePath == "" {
		g.cachePath = os.Getenv("HOME") + "/.perceval/cache"
	}
	_, err := os.Stat(g.cachePath)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(g.cachePath, 0777)
	}
	return g.cachePath
}

func (g *gitOps) sanitizeURL(path string) string {
	if strings.HasPrefix(path, "/r/") {
		path = strings.Replace(path, "/r/", "", 1)
	} else if strings.HasPrefix(path, "/gerrit/") {
		path = strings.Replace(path, "/gerrit/", "", 1)
	}
	return strings.TrimLeft(path, "/")
}

func (g *gitOps) buildOrgName(path string, gitSource bool) string {
	sanitizePath := g.sanitizeURL(path)
	if !gitSource {
		ary := strings.Split(sanitizePath, ".")
		return ary[1]
	}
	ary := strings.Split(sanitizePath, "/")
	return ary[0]
}

func (g *gitOps) buildRepoName(path, orgName string) string {
	sanitizePath := g.sanitizeURL(path)
	if strings.Contains(sanitizePath, orgName) {
		sanitizePath = strings.Replace(sanitizePath, orgName+"/", "", 1)
	}
	if !g.followHierarchy {
		sanitizePath = strings.Replace(sanitizePath, "/", "-", 1)
		sanitizePath = strings.Replace(sanitizePath, "_", "-", 1)
		sanitizePath = strings.Replace(sanitizePath, "/.", "", 1)
		sanitizePath = strings.Replace(sanitizePath, ".", "", 1)
	}
	return sanitizePath
}

func (g *gitOps) isGitSource(host string) bool {
	if strings.Contains(host, "github.com") || strings.Contains(host, "gitlab.com") || strings.Contains(host, "bitbucket.org") {
		return true
	}
	return false
}

func (g *gitOps) orgName() string {
	parser, err := url.Parse(g.gitURL)
	if err != nil {
		panic(err)
	}
	if g.isGitSource(parser.Host) {
		return g.buildOrgName(parser.Path, true)
	}
	return g.buildOrgName(parser.Host, false)
}

func (g *gitOps) repoName() string {
	parser, err := url.Parse(g.gitURL)
	if err != nil {
		panic(err)
	}
	return g.buildRepoName(parser.Path, g.orgName())
}

func (g *gitOps) getCachePath() string {
	basePath := g.cachePathFunc()
	path := path.Join(basePath, g.orgName())
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		_ = os.MkdirAll(path, 0777)
	}
	return path
}

func (g *gitOps) buildEmptyStatsData() map[string]interface{} {
	statsData := make(map[string]interface{})
	r := make(map[string]interface{})
	r["loc"] = 0
	r["pls"] = []interface{}{}
	r["timestamp"] = nil
	statsData[g.repoName()] = r
	return statsData
}

func (g *gitOps) writeJSONFile(data map[string]interface{}, filePath, fileName string) {
	path := path.Join(filePath, fileName)
	file, _ := jsoniter.MarshalIndent(data, "", " ")
	err := ioutil.WriteFile(path, file, 0666)
	if err != nil {
		fmt.Printf("cannot write JSON object to %s: %+v\n", path, err)
		g.errored = true
	}
}

func (g *gitOps) readJSONFile(filePath, fileName string) map[string]interface{} {
	path := path.Join(filePath, fileName)
	bts, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("cannot read JSON file %s: %+v\n", path, err)
		// g.errored = true
		return g.buildEmptyStatsData()
	}
	var obj map[string]interface{}
	err = jsoniter.Unmarshal(bts, &obj)
	if err != nil {
		fmt.Printf("cannot unmarshal JSON file %s: %+v\n", path, err)
		// g.errored = true
		return g.buildEmptyStatsData()
	}
	return obj
}

func (g *gitOps) loadCache() {
	path := path.Join(g.getCachePath(), g.cacheFileName)
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		statsData := g.buildEmptyStatsData()
		g.cache = statsData
		g.writeJSONFile(statsData, g.getCachePath(), g.cacheFileName)
	} else {
		g.cache = g.readJSONFile(g.getCachePath(), g.cacheFileName)
		_, ok := g.cache[g.repoName()]
		if !ok {
			statsData := g.buildEmptyStatsData()
			g.cache[g.repoName()] = statsData[g.repoName()]
			g.writeJSONFile(g.cache, g.getCachePath(), g.cacheFileName)
		}
	}
	// fmt.Printf("%s: %+v\n", path, g.cache)
}

func (g *gitOps) repoPath() string {
	if g.followHierarchy {
		return path.Join(g.basePath, g.orgName()+"/"+g.repoName())
	}
	return path.Join(g.basePath, g.orgName()+"-"+g.repoName())
}

func (g *gitOps) clone() {
	args := []string{"git", "clone", g.gitURL, g.repoPath()}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	// fmt.Printf("executed %+v\n", args)
}

func (g *gitOps) fetch() {
	path, err := filepath.Abs(g.repoPath())
	if err != nil {
		fmt.Printf("error absolute path %s: %+v\n", path, err)
		return
	}
	err = os.Chdir(path)
	if err != nil {
		fmt.Printf("error chdir to %s: %+v\n", path, err)
		return
	}
	args := []string{"git", "fetch"}
	cmd := exec.Command(args[0], args[1:]...)
	env := append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	// fmt.Printf("executed %+v\n", args)
	args = append(args, "-p")
	cmd = exec.Command(args[0], args[1:]...)
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	// fmt.Printf("executed %+v\n", args)
}

func (g *gitOps) pull() bool {
	path, err := filepath.Abs(g.repoPath())
	if err != nil {
		fmt.Printf("error absolute path %s: %+v\n", path, err)
		return false
	}
	err = os.Chdir(path)
	if err != nil {
		fmt.Printf("error chdir to %s: %+v\n", path, err)
		return false
	}
	var (
		status bool
		outb   bytes.Buffer
		branch string
	)
	args := []string{"git", "remote", "set-head", "origin", "--auto"}
	cmd := exec.Command(args[0], args[1:]...)
	env := append(os.Environ(), "LANG=C", "HOME="+os.Getenv("HOME"))
	cmd.Env = env
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	}
	// fmt.Printf("executed %+v\n", args)
	args = []string{"git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD"}
	cmd = exec.Command(args[0], args[1:]...)
	cmd.Env = env
	cmd.Stdout = &outb
	err = cmd.Run()
	if err != nil {
		fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
		g.errored = true
	} else {
		result := outb.String()
		branch = strings.TrimSpace(strings.Replace(result, "origin/", "", 1))
	}
	// fmt.Printf("executed %+v, got %s branch\n", args, branch)
	if branch != "" {
		args = []string{"git", "checkout", branch}
		cmd = exec.Command(args[0], args[1:]...)
		cmd.Env = env
		cmd.Stdout = nil
		err = cmd.Run()
		if err != nil {
			fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
			g.errored = true
		}
		// fmt.Printf("executed %+v\n", args)
		var ob bytes.Buffer
		args = []string{"git", "pull", "origin", branch}
		cmd = exec.Command(args[0], args[1:]...)
		cmd.Env = env
		cmd.Stdout = &ob
		err = cmd.Run()
		if err != nil {
			fmt.Printf("error executing %s command: %+v\n", strings.Join(args, " "), err)
			g.errored = true
		} else {
			result := ob.String()
			if strings.Contains(result, "Already up to date.") {
				status = true
			}
			// fmt.Printf("executed %+v, got %s -> %v\n", args, result, status)
		}
	}
	return status
}

func (g *gitOps) load() {
	_, err := os.Stat(g.repoPath())
	if os.IsNotExist(err) {
		g.clone()
	} else {
		g.fetch()
		g.upToDate = g.pull()
	}
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
		return
	}
	gitops := gitOps{}
	gitops.init(os.Args[1])
	gitops.loadCache()
	gitops.load()
}
